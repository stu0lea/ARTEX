package md2docx

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	imageWidthEMU = 5688000 // 158mm，与 Word 正文栏宽一致
)

var (
	controlRe        = regexp.MustCompile(`^\{%\s*(?:p\s+)?(for|if|elif|else|endfor|endif)\b(.*?)%\}$`)
	tagRe            = regexp.MustCompile(`\{\{\s*(.*?)\s*\}\}`)
	rIDRe            = regexp.MustCompile(`Id="rId(\d+)"`)
	groupIdxRe       = regexp.MustCompile(`groups\[(\d+)\]`)
	groupLengthRe    = regexp.MustCompile(`groups\|length\s*>\s*(\d+)`)
	word14IdentityRe = regexp.MustCompile(`\s+w14:(?:paraId|textId)="[^"]*"`)
)

type control struct {
	kind string
	rest string
}

func parseControl(text string) (control, bool) {
	m := controlRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return control{}, false
	}
	return control{kind: m[1], rest: strings.TrimSpace(m[2])}, true
}

type docxPackage struct {
	entries  map[string][]byte
	order    []string
	rels     []imageRel
	mediaSeq int
	relSeq   int
	docPrSeq int
}

type imageRel struct {
	id     string
	target string
}

var contentDefaultTypes = map[string]string{
	"png":  "image/png",
	"jpeg": "image/jpeg",
	"gif":  "image/gif",
}

func readPackage(template []byte) (*docxPackage, error) {
	zr, err := zip.NewReader(bytes.NewReader(template), int64(len(template)))
	if err != nil {
		return nil, fmt.Errorf("模板不是有效的 docx: %w", err)
	}
	p := &docxPackage{entries: map[string][]byte{}}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		if _, exists := p.entries[f.Name]; !exists {
			p.order = append(p.order, f.Name)
		}
		p.entries[f.Name] = b
	}
	if _, ok := p.entries["word/document.xml"]; !ok {
		return nil, fmt.Errorf("模板缺少 word/document.xml")
	}
	if rels, ok := p.entries["word/_rels/document.xml.rels"]; ok {
		for _, m := range rIDRe.FindAllSubmatch(rels, -1) {
			if n, err := strconv.Atoi(string(m[1])); err == nil && n > p.relSeq {
				p.relSeq = n
			}
		}
	}
	return p, nil
}

func (p *docxPackage) set(name string, data []byte) {
	if _, exists := p.entries[name]; !exists {
		p.order = append(p.order, name)
	}
	p.entries[name] = data
}

// addImage 把图片写进 word/media，并登记关系；返回 r:embed 与像素尺寸。
func (p *docxPackage) addImage(path string) (rid string, cfg image.Config, ext string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", image.Config{}, "", false
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return "", image.Config{}, "", false
	}
	switch format {
	case "png", "jpeg", "gif":
		ext = format
	default:
		return "", image.Config{}, "", false
	}
	p.mediaSeq++
	name := fmt.Sprintf("word/media/image%d.%s", p.mediaSeq, ext)
	p.set(name, data)
	p.relSeq++
	rid = fmt.Sprintf("rId%d", p.relSeq)
	p.rels = append(p.rels, imageRel{id: rid, target: "media/" + filepath.Base(name)})
	p.docPrSeq++
	return rid, cfg, ext, true
}

func (p *docxPackage) bytes() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range p.order {
		w, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(p.entries[name]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type renderer struct {
	pkg    *docxPackage
	figSeq int
}

// Render 把 DocData 套进 docx 模板，返回新的 docx 字节。
func Render(template []byte, data *DocData) ([]byte, error) {
	pkg, err := readPackage(template)
	if err != nil {
		return nil, err
	}
	r := &renderer{pkg: pkg}
	doc := string(pkg.entries["word/document.xml"])
	head, body, tail, ok := bodyParts(doc)
	if !ok {
		return nil, fmt.Errorf("模板 document.xml 缺少 w:body")
	}
	ctx := newValueCtx(data)
	rendered, err := r.renderNodes(splitBody(body), ctx)
	if err != nil {
		return nil, err
	}
	pkg.set("word/document.xml", []byte(head+rendered+tail))
	if err := r.updateRels(); err != nil {
		return nil, err
	}
	if err := r.updateContentTypes(); err != nil {
		return nil, err
	}
	r.enableUpdateFields()
	return pkg.bytes()
}

// Convert 直接从内存中的 Markdown 生成 docx 字节，供上层服务或工具集成。
// baseDir 用于解析 Markdown 中相对路径的图片；模板由调用方传入。
func Convert(md, baseDir string, opts ConvertOptions, template []byte) ([]byte, error) {
	return Render(template, FromMarkdown(md, baseDir, opts))
}

// RenderToFile 生成 docx 并落盘。
func RenderToFile(template []byte, data *DocData, out string) error {
	b, err := Render(template, data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(out, b, 0o644)
}

func (r *renderer) renderNodes(nodes []bodyNode, ctx *valueCtx) (string, error) {
	var out strings.Builder
	for i := 0; i < len(nodes); i++ {
		n := nodes[i]
		switch n.kind {
		case nodeParagraph:
			text := strings.TrimSpace(paragraphText(n.xml))
			if ctrl, ok := parseControl(text); ok {
				switch ctrl.kind {
				case "for":
					name, collExpr, ok := parseFor(ctrl.rest)
					if !ok {
						continue
					}
					end, _ := findBlockEnd(nodes, i+1, ctrl.kind)
					items := ctx.eval(collExpr)
					var buf strings.Builder
					if err := eachItem(items, func(item any) error {
						body, err := r.renderNodes(nodes[i+1:end], ctx.child(name, item))
						if err != nil {
							return err
						}
						buf.WriteString(body)
						return nil
					}); err != nil {
						return "", err
					}
					out.WriteString(buf.String())
					i = end
					continue
				case "if":
					end, elseIdx := findBlockEnd(nodes, i+1, ctrl.kind)
					take := truthy(ctx.eval(ctrl.rest))
					start, stop := i+1, end
					if elseIdx >= 0 {
						if take {
							stop = elseIdx
						} else {
							start = elseIdx + 1
						}
					} else if !take {
						start = end
					}
					if start < stop {
						body, err := r.renderNodes(nodes[start:stop], ctx)
						if err != nil {
							return "", err
						}
						out.WriteString(body)
					}
					i = end
					continue
				default:
					// 孤立的 endfor/endif 直接丢弃
					continue
				}
			}
			s, err := r.renderParagraph(n.xml, ctx)
			if err != nil {
				return "", err
			}
			out.WriteString(s)
		case nodeTable:
			out.WriteString(r.renderTable(n.xml, ctx))
		default:
			out.WriteString(n.xml)
		}
	}
	return out.String(), nil
}

func parseFor(rest string) (name, collection string, ok bool) {
	idx, found := findTopWord(rest, "in")
	if !found {
		return "", "", false
	}
	name = strings.TrimSpace(rest[:idx])
	collection = strings.TrimSpace(rest[idx+2:])
	if name == "" || collection == "" {
		return "", "", false
	}
	return name, collection, true
}

func eachItem(v any, fn func(any) error) error {
	if v == nil {
		return nil
	}
	switch list := v.(type) {
	case []System:
		for i := range list {
			if err := fn(list[i]); err != nil {
				return err
			}
		}
		return nil
	case []Vuln:
		for i := range list {
			if err := fn(list[i]); err != nil {
				return err
			}
		}
		return nil
	case []string:
		for _, s := range list {
			if err := fn(s); err != nil {
				return err
			}
		}
		return nil
	case []Shot:
		for i := range list {
			if err := fn(list[i]); err != nil {
				return err
			}
		}
		return nil
	case []ScopeRow:
		for i := range list {
			if err := fn(list[i]); err != nil {
				return err
			}
		}
		return nil
	case []Group:
		for i := range list {
			if err := fn(list[i]); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

// findBlockEnd 找到与 start 处循环/条件配对的结束节点，返回 endfor/endif 下标。
func findBlockEnd(nodes []bodyNode, start int, kind string) (end int, elseIdx int) {
	end = len(nodes)
	elseIdx = -1
	depth := 0
	for j := start; j < len(nodes); j++ {
		if nodes[j].kind != nodeParagraph {
			continue
		}
		ctrl, ok := parseControl(strings.TrimSpace(paragraphText(nodes[j].xml)))
		if !ok {
			continue
		}
		switch ctrl.kind {
		case "for", "if":
			depth++
		case "endfor", "endif":
			if depth == 0 {
				return j, elseIdx
			}
			depth--
		case "else":
			if depth == 0 {
				elseIdx = j
			}
		}
	}
	return end, elseIdx
}

func (r *renderer) renderParagraph(p string, ctx *valueCtx) (string, error) {
	text := paragraphText(p)
	if text == "" {
		return addWordWrapToParagraph(p), nil
	}
	if strings.Contains(text, "{{") {
		if expr, ok := singleTag(text); ok {
			v := ctx.eval(expr)
			if img, ok := v.(imageRef); ok {
				return r.imageParagraph(p, img.path)
			}
			if s, ok := v.(string); ok {
				switch {
				case strings.HasPrefix(s, codeSentinel):
					return r.codeParagraph(strings.TrimPrefix(s, codeSentinel)), nil
				case strings.HasPrefix(s, headSentinel):
					return r.headParagraph(strings.TrimPrefix(s, headSentinel)), nil
				case strings.HasPrefix(s, captionSentinel):
					return r.captionParagraph(strings.TrimPrefix(s, captionSentinel)), nil
				case strings.HasPrefix(s, tableSentinel):
					return r.tableFromSentinel(s)
				}
			}
		}
		return r.substituteParagraph(p, text, ctx), nil
	}
	return addWordWrapToParagraph(p), nil
}

// singleTag 判断段落是否只有一个 `{{ expr }}` 标签（去掉首尾空白）。
func singleTag(text string) (string, bool) {
	text = strings.TrimSpace(text)
	m := tagRe.FindStringSubmatch(text)
	if m == nil || m[0] != text {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

func (r *renderer) substituteParagraph(p, text string, ctx *valueCtx) string {
	parts := parseParagraph(p)
	type segment struct {
		text    string
		dynamic bool
	}
	var segments []segment
	last := 0
	for _, loc := range tagRe.FindAllStringSubmatchIndex(text, -1) {
		if loc[0] > last {
			segments = append(segments, segment{text: text[last:loc[0]]})
		}
		expr := strings.TrimSpace(text[loc[2]:loc[3]])
		segments = append(segments, segment{text: displayValue(ctx.eval(expr)), dynamic: true})
		last = loc[1]
	}
	if last < len(text) {
		segments = append(segments, segment{text: text[last:]})
	}

	var runs strings.Builder
	for _, seg := range segments {
		if seg.text == "" {
			continue
		}
		if seg.dynamic {
			runs.WriteString(inlineRuns(seg.text, parts.rPr))
		} else {
			runs.WriteString("<w:r>" + parts.rPr + `<w:t xml:space="preserve">` + seg.text + "</w:t></w:r>")
		}
	}
	return parts.open + withWordWrap(parts.pPr) + runs.String() + "</w:p>"
}

// inlineRuns 把带行内代码标记的文字拆成普通 run 与等宽 run。
func inlineRuns(text, baseRPr string) string {
	var out strings.Builder
	for text != "" {
		start := strings.Index(text, inlineCodeStart)
		if start < 0 {
			out.WriteString(runXML(baseRPr, text))
			break
		}
		if start > 0 {
			out.WriteString(runXML(baseRPr, text[:start]))
		}
		rest := text[start+len(inlineCodeStart):]
		end := strings.Index(rest, inlineCodeEnd)
		if end < 0 {
			out.WriteString(runXML(inlineCodeRPr, rest))
			break
		}
		out.WriteString(runXML(inlineCodeRPr, rest[:end]))
		text = rest[end+len(inlineCodeEnd):]
	}
	return out.String()
}

const codePPr = `<w:pPr><w:pBdr><w:left w:val="single" w:sz="8" w:space="6" w:color="D0D7DE"/></w:pBdr><w:shd w:val="clear" w:color="auto" w:fill="F7F8FA"/><w:wordWrap w:val="true"/><w:spacing w:before="0" w:after="0"/><w:ind w:left="283" w:right="170" w:firstLine="0"/><w:jc w:val="left"/></w:pPr>`

const codeRPr = `<w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:eastAsia="等线" w:cs="Consolas"/><w:sz w:val="17"/><w:szCs w:val="17"/></w:rPr>`

func (r *renderer) codeParagraph(text string) string {
	text = strings.TrimRight(text, "\r\n")
	return "<w:p>" + codePPr + runXML(codeRPr, text) + "</w:p>"
}

func (r *renderer) headParagraph(text string) string {
	text = strings.TrimSpace(text)
	pPr := `<w:pPr><w:wordWrap w:val="true"/><w:spacing w:before="160" w:after="80"/></w:pPr>`
	return "<w:p>" + pPr + runXML(boldRPr, text) + "</w:p>"
}

func (r *renderer) captionParagraph(text string) string {
	text = strings.TrimSpace(text)
	r.figSeq++
	pPr := `<w:pPr><w:wordWrap w:val="true"/><w:spacing w:after="200"/><w:jc w:val="center"/></w:pPr>`
	return "<w:p>" + pPr + runXML(captionRPr, fmt.Sprintf("图 %d  %s", r.figSeq, text)) + "</w:p>"
}

const (
	drawingNS = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
)

func (r *renderer) imageParagraph(p, path string) (string, error) {
	rid, cfg, _, ok := r.pkg.addImage(path)
	if !ok {
		return "", nil // 图片缺失/损坏时跳过，整份报告继续
	}
	width := imageWidthEMU
	height := width * cfg.Height / cfg.Width
	id := r.pkg.docPrSeq
	drawing := fmt.Sprintf(`<w:drawing %s><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="%d" cy="%d"/><wp:effectExtent l="0" t="0" r="0" b="0"/><wp:docPr id="%d" name="Picture %d"/><wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:nvPicPr><pic:cNvPr id="%d" name="image%d"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing>`,
		drawingNS, width, height, id, id, id, id, rid, width, height)
	parts := parseParagraph(p)
	return parts.open + withWordWrap(parts.pPr) + "<w:r>" + drawing + "</w:r></w:p>", nil
}

func (r *renderer) tableFromSentinel(s string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(s, tableSentinel)))
	if err != nil {
		return "", nil
	}
	var rows [][]string
	if err := json.Unmarshal(raw, &rows); err != nil {
		return "", nil
	}
	return buildTable(rows), nil
}

func buildTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	cols := 0
	for _, row := range rows {
		if len(row) > cols {
			cols = len(row)
		}
	}
	if cols == 0 {
		return ""
	}
	// 固定布局并均分正文宽度，避免 Word/WPS 按最长单元格重排后挤扁其他列。
	const tableWidth = 9000
	colWidth := tableWidth / cols
	widths := make([]int, cols)
	for i := range widths {
		widths[i] = colWidth
	}
	widths[cols-1] += tableWidth - colWidth*cols
	var b strings.Builder
	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="9000" w:type="dxa"/><w:jc w:val="center"/>`)
	b.WriteString(`<w:tblBorders>`)
	for _, edge := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		fmt.Fprintf(&b, `<w:%s w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/>`, edge)
	}
	b.WriteString(`</w:tblBorders><w:tblLayout w:type="fixed"/></w:tblPr><w:tblGrid>`)
	for i := 0; i < cols; i++ {
		fmt.Fprintf(&b, `<w:gridCol w:w="%d"/>`, widths[i])
	}
	b.WriteString(`</w:tblGrid>`)
	for ri, row := range rows {
		b.WriteString("<w:tr>")
		if ri == 0 {
			b.WriteString(`<w:trPr><w:tblHeader w:val="true"/></w:trPr>`)
		}
		for ci := 0; ci < cols; ci++ {
			value := ""
			if ci < len(row) {
				value = row[ci]
			}
			value = strings.ReplaceAll(value, inlineCodeStart, "")
			value = strings.ReplaceAll(value, inlineCodeEnd, "")
			b.WriteString(`<w:tc><w:tcPr>`)
			fmt.Fprintf(&b, `<w:tcW w:w="%d" w:type="dxa"/>`, widths[ci])
			if ri == 0 {
				b.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="F2F2F2"/>`)
			}
			b.WriteString(`</w:tcPr><w:p><w:pPr><w:wordWrap w:val="true"/><w:spacing w:before="0" w:after="0"/></w:pPr>`)
			rPr := `<w:rPr><w:sz w:val="21"/><w:szCs w:val="21"/></w:rPr>`
			if ri == 0 {
				rPr = `<w:rPr><w:b/><w:bCs/><w:sz w:val="21"/><w:szCs w:val="21"/></w:rPr>`
			}
			b.WriteString(runXML(rPr, value))
			b.WriteString(`</w:p></w:tc>`)
		}
		b.WriteString("</w:tr>")
	}
	b.WriteString("</w:tbl>")
	return b.String()
}

func (r *renderer) renderTable(tbl string, ctx *valueCtx) string {
	if groups, ok := ctx.eval("groups").([]Group); ok {
		tbl = expandGroupTable(tbl, len(groups))
	}
	return replaceTagsInXML(tbl, ctx)
}

// expandGroupTable 按实际漏洞类别数扩展模板里的分类汇总行。
// 模板默认预留 4 行，类别更多时复制最后一行并替换 groups[N] 下标。
func expandGroupTable(tbl string, count int) string {
	maxIdx := -1
	masterStart, masterEnd := -1, -1
	for i := 0; i < len(tbl); {
		idx := strings.Index(tbl[i:], "<w:tr")
		if idx < 0 {
			break
		}
		start := i + idx
		if !hasOpenTag(tbl, start, "w:tr") {
			i = start + len("<w:tr")
			continue
		}
		end := findElementEnd(tbl, start, "w:tr")
		if end < 0 {
			break
		}
		for _, m := range groupIdxRe.FindAllStringSubmatch(tbl[start:end], -1) {
			n, _ := strconv.Atoi(m[1])
			if n > maxIdx {
				maxIdx = n
				masterStart, masterEnd = start, end
			}
		}
		i = end
	}
	if maxIdx < 0 || count <= maxIdx+1 {
		return tbl
	}

	master := tbl[masterStart:masterEnd]
	var add strings.Builder
	for j := maxIdx + 1; j < count; j++ {
		row := strings.ReplaceAll(master, fmt.Sprintf("groups[%d]", maxIdx), fmt.Sprintf("groups[%d]", j))
		row = groupLengthRe.ReplaceAllStringFunc(row, func(s string) string {
			m := groupLengthRe.FindStringSubmatch(s)
			if m != nil && m[1] == strconv.Itoa(maxIdx) {
				return fmt.Sprintf("groups|length > %d", j)
			}
			return s
		})
		row = word14IdentityRe.ReplaceAllString(row, "")
		add.WriteString(row)
	}
	return tbl[:masterEnd] + add.String() + tbl[masterEnd:]
}

// replaceTagsInXML 在表格单元格里替换标量标签，保留原有 run 格式。
func replaceTagsInXML(xmlText string, ctx *valueCtx) string {
	var out strings.Builder
	rest := xmlText
	for {
		idx := strings.Index(rest, "<w:t")
		if idx < 0 {
			out.WriteString(rest)
			break
		}
		j := idx + len("<w:t")
		if j < len(rest) && !(isXMLSpace(rest[j]) || rest[j] == '>' || rest[j] == '/') {
			out.WriteString(rest[:j])
			rest = rest[j:]
			continue
		}
		openEnd := findTagEnd(rest, idx)
		if openEnd < 0 {
			out.WriteString(rest)
			break
		}
		closeIdx := strings.Index(rest[openEnd:], "</w:t>")
		if closeIdx < 0 {
			out.WriteString(rest)
			break
		}
		out.WriteString(rest[:openEnd])
		text := rest[openEnd : openEnd+closeIdx]
		out.WriteString(replaceTagText(text, ctx))
		out.WriteString("</w:t>")
		rest = rest[openEnd+closeIdx+len("</w:t>"):]
	}
	return out.String()
}

func replaceTagText(text string, ctx *valueCtx) string {
	if !strings.Contains(text, "{{") {
		return text
	}
	var b strings.Builder
	last := 0
	for _, loc := range tagRe.FindAllStringSubmatchIndex(text, -1) {
		b.WriteString(text[last:loc[0]])
		expr := strings.TrimSpace(text[loc[2]:loc[3]])
		b.WriteString(xmlEscape(displayValue(ctx.eval(expr))))
		last = loc[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

func (r *renderer) updateRels() error {
	if len(r.pkg.rels) == 0 {
		return nil
	}
	name := "word/_rels/document.xml.rels"
	raw, ok := r.pkg.entries[name]
	if !ok {
		return fmt.Errorf("模板缺少 %s", name)
	}
	end := strings.LastIndex(string(raw), "</Relationships>")
	if end < 0 {
		return fmt.Errorf("模板关系文件格式异常")
	}
	var add strings.Builder
	for _, rel := range r.pkg.rels {
		fmt.Fprintf(&add, `<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="%s"/>`,
			rel.id, rel.target)
	}
	r.pkg.set(name, []byte(string(raw[:end])+add.String()+string(raw[end:])))
	return nil
}

func (r *renderer) updateContentTypes() error {
	used := map[string]bool{}
	for _, rel := range r.pkg.rels {
		ext := strings.TrimPrefix(filepath.Ext(rel.target), ".")
		used[ext] = true
	}
	if len(used) == 0 {
		return nil
	}
	raw := string(r.pkg.entries["[Content_Types].xml"])
	var add strings.Builder
	for ext := range used {
		if content, ok := contentDefaultTypes[ext]; ok && !strings.Contains(raw, `Extension="`+ext+`"`) {
			fmt.Fprintf(&add, `<Default Extension="%s" ContentType="%s"/>`, ext, content)
		}
	}
	if add.Len() == 0 {
		return nil
	}
	idx := strings.LastIndex(raw, "</Types>")
	if idx < 0 {
		return fmt.Errorf("模板 [Content_Types].xml 格式异常")
	}
	r.pkg.set("[Content_Types].xml", []byte(raw[:idx]+add.String()+raw[idx:]))
	return nil
}

func (r *renderer) enableUpdateFields() {
	raw, ok := r.pkg.entries["word/settings.xml"]
	if !ok {
		return
	}
	s := string(raw)
	if strings.Contains(s, "<w:updateFields") {
		return
	}
	idx := strings.LastIndex(s, "</w:settings>")
	if idx < 0 {
		return
	}
	r.pkg.set("word/settings.xml", []byte(s[:idx]+`<w:updateFields w:val="true"/>`+s[idx:]))
}

// ValidateXML 是测试辅助：确认生成的 XML 结构良好。
func ValidateXML(b []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
