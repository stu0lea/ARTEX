package md2docx

import "strings"

// 本文件只做最小的 OOXML 文本操作：定位元素、提取段落文字、生成 run/pPr。
// 模板由 Word 生成，结构规范，因此不需要完整的 XML DOM。

// xmlEscape 转义要写入 <w:t> 的动态文本。
func xmlEscape(s string) string {
	// Markdown 里可能混入终端控制字符（如 0x03），XML 1.0 不允许，
	// 不清理会导致 Word 打开文档时内容为空或提示修复。
	s = sanitizeXML(s)
	if !strings.ContainsAny(s, `&<>`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func invalidXMLRune(r rune) bool {
	if r == '\t' || r == '\n' || r == '\r' {
		return false
	}
	return r < 0x20 || r == 0xFFFE || r == 0xFFFF
}

func sanitizeXML(s string) string {
	if strings.IndexFunc(s, invalidXMLRune) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if invalidXMLRune(r) {
			return -1
		}
		return r
	}, s)
}

func isXMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// findTagEnd 返回标签结束 '>' 之后的位置，跳过属性里的引号内容。
func findTagEnd(s string, start int) int {
	var quote byte
	for i := start + 1; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '>':
			return i + 1
		}
	}
	return -1
}

type tagInfo struct {
	name        string
	closing     bool
	selfClosing bool
	end         int
}

// parseTag 解析从 s[start]=='<' 开始的一个标签。
func parseTag(s string, start int) (tagInfo, bool) {
	if start >= len(s) || s[start] != '<' {
		return tagInfo{}, false
	}
	if strings.HasPrefix(s[start:], "<!--") {
		if j := strings.Index(s[start+4:], "-->"); j >= 0 {
			return tagInfo{end: start + 4 + j + 3}, true
		}
		return tagInfo{}, false
	}
	if strings.HasPrefix(s[start:], "<![CDATA[") {
		if j := strings.Index(s[start+9:], "]]>"); j >= 0 {
			return tagInfo{end: start + 9 + j + 3}, true
		}
		return tagInfo{}, false
	}
	end := findTagEnd(s, start)
	if end < 0 {
		return tagInfo{}, false
	}
	i := start + 1
	for i < end && isXMLSpace(s[i]) {
		i++
	}
	closing := false
	if i < end && s[i] == '/' {
		closing = true
		i++
	}
	j := i
	for j < end && !isXMLSpace(s[j]) && s[j] != '>' && s[j] != '/' {
		j++
	}
	name := s[i:j]
	self := false
	k := end - 2
	for k >= start && isXMLSpace(s[k]) {
		k--
	}
	if k >= start && s[k] == '/' {
		self = true
	}
	return tagInfo{name: name, closing: closing, selfClosing: self, end: end}, true
}

// findElementEnd 返回与 start 处元素匹配的结束标签之后的位置。
func findElementEnd(s string, start int, name string) int {
	depth := 0
	for i := start; i < len(s); {
		idx := strings.IndexByte(s[i:], '<')
		if idx < 0 {
			return -1
		}
		i += idx
		info, ok := parseTag(s, i)
		if !ok {
			i++
			continue
		}
		if info.name == name {
			switch {
			case info.closing:
				depth--
				if depth == 0 {
					return info.end
				}
			case info.selfClosing:
				if depth == 0 {
					return info.end
				}
			default:
				depth++
			}
		}
		i = info.end
	}
	return -1
}

// hasOpenTag 判断 s[i:] 是否以 `<name` 开头，且 name 后是合法边界。
func hasOpenTag(s string, i int, name string) bool {
	if i >= len(s) || s[i] != '<' {
		return false
	}
	if !strings.HasPrefix(s[i:], "<"+name) {
		return false
	}
	j := i + 1 + len(name)
	if j >= len(s) {
		return true
	}
	c := s[j]
	return isXMLSpace(c) || c == '>' || c == '/'
}

type nodeKind int

const (
	nodeParagraph nodeKind = iota
	nodeTable
	nodeRaw
)

type bodyNode struct {
	kind nodeKind
	xml  string
}

// splitBody 把 <w:body> 里的顶层段落 / 表格 / 其它元素切开。
func splitBody(body string) []bodyNode {
	var out []bodyNode
	for i := 0; i < len(body); {
		idx := strings.IndexByte(body[i:], '<')
		if idx < 0 {
			break
		}
		i += idx
		switch {
		case hasOpenTag(body, i, "w:p"):
			end := findElementEnd(body, i, "w:p")
			if end < 0 {
				out = append(out, bodyNode{kind: nodeRaw, xml: body[i:]})
				return out
			}
			out = append(out, bodyNode{kind: nodeParagraph, xml: body[i:end]})
			i = end
		case hasOpenTag(body, i, "w:tbl"):
			end := findElementEnd(body, i, "w:tbl")
			if end < 0 {
				out = append(out, bodyNode{kind: nodeRaw, xml: body[i:]})
				return out
			}
			out = append(out, bodyNode{kind: nodeTable, xml: body[i:end]})
			i = end
		case hasOpenTag(body, i, "w:sectPr"):
			end := findElementEnd(body, i, "w:sectPr")
			if end < 0 {
				out = append(out, bodyNode{kind: nodeRaw, xml: body[i:]})
				return out
			}
			out = append(out, bodyNode{kind: nodeRaw, xml: body[i:end]})
			i = end
		default:
			info, ok := parseTag(body, i)
			if !ok || info.end <= i {
				out = append(out, bodyNode{kind: nodeRaw, xml: body[i:]})
				return out
			}
			out = append(out, bodyNode{kind: nodeRaw, xml: body[i:info.end]})
			i = info.end
		}
	}
	return out
}

// bodyParts 拆出 body 前后内容，保留文档声明与 sectPr。
func bodyParts(doc string) (head, body, tail string, ok bool) {
	start := strings.Index(doc, "<w:body")
	if start < 0 {
		return "", "", "", false
	}
	openEnd := findTagEnd(doc, start)
	if openEnd < 0 {
		return "", "", "", false
	}
	end := strings.LastIndex(doc, "</w:body>")
	if end < openEnd {
		return "", "", "", false
	}
	return doc[:openEnd], doc[openEnd:end], doc[end:], true
}

// paragraphText 拼接段落里所有 <w:t> 的文字。
func paragraphText(p string) string {
	var b strings.Builder
	rest := p
	for {
		idx := strings.Index(rest, "<w:t")
		if idx < 0 {
			break
		}
		j := idx + len("<w:t")
		if j < len(rest) && !(isXMLSpace(rest[j]) || rest[j] == '>' || rest[j] == '/') {
			rest = rest[j:]
			continue
		}
		openEnd := findTagEnd(rest, idx)
		if openEnd < 0 {
			break
		}
		closeIdx := strings.Index(rest[openEnd:], "</w:t>")
		if closeIdx < 0 {
			break
		}
		b.WriteString(rest[openEnd : openEnd+closeIdx])
		rest = rest[openEnd+closeIdx+len("</w:t>"):]
	}
	return b.String()
}

type paraParts struct {
	open string
	pPr  string
	body string // pPr 之后的全部内容
	rPr  string // 第一个 run 的 rPr
}

func parseParagraph(p string) paraParts {
	openEnd := findTagEnd(p, 0)
	if openEnd < 0 {
		return paraParts{open: p}
	}
	inner := p[openEnd:]
	if strings.HasSuffix(inner, "</w:p>") {
		inner = inner[:len(inner)-len("</w:p>")]
	}
	parts := paraParts{open: p[:openEnd], body: inner}
	trimmed := strings.TrimLeft(inner, " \t\r\n")
	if strings.HasPrefix(trimmed, "<w:pPr") {
		start := strings.Index(inner, "<w:pPr")
		if end := findElementEnd(inner, start, "w:pPr"); end > start {
			parts.pPr = inner[start:end]
			parts.body = inner[:start] + inner[end:]
		}
	}
	body := parts.body
	for i := 0; i < len(body); {
		idx := strings.Index(body[i:], "<w:r")
		if idx < 0 {
			break
		}
		i += idx
		if !hasOpenTag(body, i, "w:r") {
			i += len("<w:r")
			continue
		}
		end := findElementEnd(body, i, "w:r")
		if end < 0 {
			break
		}
		run := body[i:end]
		if rPrStart := strings.Index(run, "<w:rPr"); rPrStart >= 0 {
			if rPrEnd := findElementEnd(run, rPrStart, "w:rPr"); rPrEnd > rPrStart {
				parts.rPr = run[rPrStart:rPrEnd]
				break
			}
		}
		i = end
	}
	return parts
}

// wrapSuccessors 是 pPr 中排在 wordWrap 之后的元素，插入时必须放在它们之前。
var wrapSuccessors = []string{
	"w:overflowPunct", "w:topLinePunct", "w:autoSpaceDE", "w:autoSpaceDN",
	"w:bidi", "w:adjustRightInd", "w:snapToGrid", "w:spacing", "w:ind",
	"w:contextualSpacing", "w:mirrorIndents", "w:suppressOverlap", "w:jc",
	"w:textDirection", "w:textAlignment", "w:textboxTightWrap",
	"w:outlineLvl", "w:divId", "w:cnfStyle", "w:rPr", "w:sectPr", "w:pPrChange",
}

// withWordWrap 给 pPr 补上 wordWrap，长 URL / base64 不会顶出右边距。
func withWordWrap(pPr string) string {
	if pPr == "" {
		return `<w:pPr><w:wordWrap w:val="true"/></w:pPr>`
	}
	openEnd := findTagEnd(pPr, 0)
	if openEnd < 0 {
		return pPr
	}
	inner := pPr[openEnd:]
	if strings.HasSuffix(inner, "</w:pPr>") {
		inner = inner[:len(inner)-len("</w:pPr>")]
	}
	if strings.Contains(inner, "<w:wordWrap") {
		return pPr
	}
	insertAt := len(inner)
	for _, name := range wrapSuccessors {
		if idx := strings.Index(inner, "<"+name); idx >= 0 && idx < insertAt {
			insertAt = idx
		}
	}
	return pPr[:openEnd] + inner[:insertAt] + `<w:wordWrap w:val="true"/>` + inner[insertAt:] + "</w:pPr>"
}

// addWordWrapToParagraph 在保留原段落内容的前提下补 wordWrap。
func addWordWrapToParagraph(p string) string {
	parts := parseParagraph(p)
	if parts.pPr != "" {
		return parts.open + withWordWrap(parts.pPr) + parts.body + "</w:p>"
	}
	return parts.open + withWordWrap("") + parts.body + "</w:p>"
}

// runXML 生成一个文本 run。
func runXML(rPr, text string) string {
	if text == "" {
		return ""
	}
	return "<w:r>" + rPr + `<w:t xml:space="preserve">` + xmlEscape(text) + "</w:t></w:r>"
}

const (
	inlineCodeRPr = `<w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:eastAsia="等线"/><w:sz w:val="18"/><w:szCs w:val="18"/><w:shd w:val="clear" w:color="auto" w:fill="F2F2F2"/></w:rPr>`
	boldRPr       = `<w:rPr><w:b/><w:bCs/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr>`
	captionRPr    = `<w:rPr><w:color w:val="595959"/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr>`
)
