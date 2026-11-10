package md2docx

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// blockKind 是 Markdown 解析后的排版单元类型。
type blockKind int

const (
	blockParagraph blockKind = iota
	blockHeading
	blockCode
	blockList
	blockTable
	blockQuote
	blockImage
	blockRule
)

// block 是 Markdown 里可以独立排版的一块内容。
type block struct {
	kind    blockKind
	level   int      // 标题级别 / 列表缩进层级
	text    string   // 段落、标题、代码、引用正文
	lang    string   // 代码块语言（仅记录，不影响排版）
	marker  string   // 列表原始标记，如 "-"、"1."
	ordered bool     // 有序列表
	rows    [][]string
	aligns  []string // 表格列对齐：left/center/right
	image   string   // 图片路径
	alt     string   // 图片替代文字
}

var (
	headingRe  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	listRe     = regexp.MustCompile(`^(\s*)([-+*]|\d+[.)])\s+(.*)$`)
	hrRe       = regexp.MustCompile(`^\s*((\*\s*){3,}|(-\s*){3,}|(_\s*){3,})$`)
	imageRe    = regexp.MustCompile(`^!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)$`)
	setextRe   = regexp.MustCompile(`^\s*(=+|-+)\s*$`)
	tableSepRe = regexp.MustCompile(`^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)+\|?\s*$`)
)

// parseMarkdown 把 Markdown 文本拆成顺序排列的排版块。
// 只覆盖报告导出实际会用到的语法，不做完整 CommonMark 实现。
func parseMarkdown(md string) []block {
	md = strings.TrimPrefix(md, "\ufeff")
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	out := make([]block, 0, 64)
	var para []string

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		if text := joinLines(para); text != "" {
			out = append(out, block{kind: blockParagraph, text: text})
		}
		para = nil
	}

	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			flushPara()
			continue
		}

		// 围栏代码块
		if fence, ok := fenceMarker(trimmed); ok {
			flushPara()
			var code []string
			i++
			for ; i < len(lines); i++ {
				if strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
					break
				}
				code = append(code, strings.TrimRight(lines[i], " \t\r"))
			}
			out = append(out, block{
				kind: blockCode,
				text: strings.Join(code, "\n"),
				lang: strings.TrimSpace(strings.TrimPrefix(trimmed, fence)),
			})
			continue
		}

		// ATX 标题
		if m := headingRe.FindStringSubmatch(trimmed); m != nil {
			flushPara()
			text := strings.TrimSpace(strings.TrimRight(m[2], "#"))
			out = append(out, block{kind: blockHeading, level: len(m[1]), text: text})
			continue
		}

		// Setext 标题（正文下一行是 === 或 ---）
		if i+1 < len(lines) {
			next := strings.TrimSpace(lines[i+1])
			if !strings.Contains(trimmed, "|") && setextRe.MatchString(next) {
				flushPara()
				level := 1
				if strings.HasPrefix(next, "-") {
					level = 2
				}
				out = append(out, block{kind: blockHeading, level: level, text: trimmed})
				i++
				continue
			}
		}

		// 分隔线
		if hrRe.MatchString(trimmed) {
			flushPara()
			out = append(out, block{kind: blockRule})
			continue
		}

		// 表格：当前行是表头，下一行是分隔行
		if strings.Contains(trimmed, "|") && i+1 < len(lines) &&
			tableSepRe.MatchString(strings.TrimSpace(lines[i+1])) {
			flushPara()
			rows := [][]string{splitTableCells(trimmed)}
			aligns := parseAligns(strings.TrimSpace(lines[i+1]))
			i += 2
			for ; i < len(lines); i++ {
				line := strings.TrimSpace(lines[i])
				if line == "" || !strings.Contains(line, "|") {
					break
				}
				if tableSepRe.MatchString(line) {
					continue
				}
				rows = append(rows, splitTableCells(line))
			}
			i--
			out = append(out, block{kind: blockTable, rows: rows, aligns: aligns})
			continue
		}

		// 引用
		if strings.HasPrefix(trimmed, ">") {
			flushPara()
			var quote []string
			for ; i < len(lines); i++ {
				q := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(q, ">") {
					break
				}
				quote = append(quote, strings.TrimSpace(strings.TrimPrefix(q, ">")))
			}
			i--
			out = append(out, block{kind: blockQuote, text: joinLines(quote)})
			continue
		}

		// 列表项
		if m := listRe.FindStringSubmatch(raw); m != nil {
			flushPara()
			level := indentWidth(m[1]) / 2
			if level > 5 {
				level = 5
			}
			marker := m[2]
			ordered := marker[0] >= '0' && marker[0] <= '9'
			out = append(out, block{
				kind:    blockList,
				level:   level,
				marker:  marker,
				ordered: ordered,
				text:    strings.TrimSpace(m[3]),
			})
			continue
		}

		// 独占一行的图片
		if m := imageRe.FindStringSubmatch(trimmed); m != nil {
			flushPara()
			out = append(out, block{kind: blockImage, alt: m[1], image: m[2]})
			continue
		}

		para = append(para, trimmed)
	}
	flushPara()
	return out
}

func fenceMarker(line string) (string, bool) {
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(line, f) {
			return f, true
		}
	}
	return "", false
}

func indentWidth(s string) int {
	n := 0
	for _, r := range s {
		if r == '\t' {
			n += 4
		} else {
			n++
		}
	}
	return n
}

// splitTableCells 按 | 拆分单元格，支持 \| 转义。
func splitTableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) && line[i+1] == '|' {
			cur.WriteByte('|')
			i++
			continue
		}
		if line[i] == '|' {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(line[i])
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells
}

func parseAligns(sep string) []string {
	cells := splitTableCells(sep)
	out := make([]string, len(cells))
	for i, c := range cells {
		c = strings.TrimSpace(c)
		left := strings.HasPrefix(c, ":")
		right := strings.HasSuffix(c, ":")
		switch {
		case left && right:
			out[i] = "center"
		case right:
			out[i] = "right"
		case left:
			out[i] = "left"
		}
	}
	return out
}

// joinLines 把跨行段落拼成一行：中文之间不补空格，西文之间补一个空格。
func joinLines(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString(line)
			continue
		}
		prev, _ := utf8.DecodeLastRuneInString(b.String())
		next, _ := utf8.DecodeRuneInString(line)
		if isCJK(prev) || isCJK(next) || strings.HasSuffix(b.String(), "（") ||
			strings.HasPrefix(line, "）") {
			b.WriteString(line)
		} else {
			b.WriteByte(' ')
			b.WriteString(line)
		}
	}
	return b.String()
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// inlineStyle 是一段文字的行内样式。
type inlineStyle struct {
	bold, italic, strike, code bool
	color                      string
}

type run struct {
	text  string
	style inlineStyle
}

// parseInline 把一段 Markdown 行内文本拆成带样式的 run 列表。
// 支持 **加粗**、*斜体*、~~删除线~~、`行内代码`、[链接](url)。
func parseInline(s string) []run {
	return parseInlineStyle(s, inlineStyle{})
}

func parseInlineStyle(s string, st inlineStyle) []run {
	out := make([]run, 0, 8)
	var buf strings.Builder

	flush := func() {
		if buf.Len() > 0 {
			out = append(out, run{text: buf.String(), style: st})
			buf.Reset()
		}
	}

	for i := 0; i < len(s); {
		switch {
		case s[i] == '\\' && i+1 < len(s):
			buf.WriteByte(s[i+1])
			i += 2

		case s[i] == '`':
			if j := strings.IndexByte(s[i+1:], '`'); j >= 0 {
				flush()
				code := st
				code.code = true
				out = append(out, run{text: s[i+1 : i+1+j], style: code})
				i += j + 2
			} else {
				buf.WriteByte(s[i])
				i++
			}

		case strings.HasPrefix(s[i:], "**"):
			if j := strings.Index(s[i+2:], "**"); j >= 0 {
				flush()
				bold := st
				bold.bold = true
				out = append(out, parseInlineStyle(s[i+2:i+2+j], bold)...)
				i += j + 4
			} else {
				buf.WriteString("**")
				i += 2
			}

		case strings.HasPrefix(s[i:], "__") && boundary(s, i):
			if j := strings.Index(s[i+2:], "__"); j >= 0 {
				flush()
				bold := st
				bold.bold = true
				out = append(out, parseInlineStyle(s[i+2:i+2+j], bold)...)
				i += j + 4
			} else {
				buf.WriteString("__")
				i += 2
			}

		case strings.HasPrefix(s[i:], "~~"):
			if j := strings.Index(s[i+2:], "~~"); j >= 0 {
				flush()
				strike := st
				strike.strike = true
				out = append(out, parseInlineStyle(s[i+2:i+2+j], strike)...)
				i += j + 4
			} else {
				buf.WriteString("~~")
				i += 2
			}

		case s[i] == '*':
			if j := strings.IndexByte(s[i+1:], '*'); j >= 0 {
				flush()
				italic := st
				italic.italic = true
				out = append(out, parseInlineStyle(s[i+1:i+1+j], italic)...)
				i += j + 2
			} else {
				buf.WriteByte(s[i])
				i++
			}

		case s[i] == '_' && boundary(s, i):
			if j := strings.IndexByte(s[i+1:], '_'); j >= 0 {
				flush()
				italic := st
				italic.italic = true
				out = append(out, parseInlineStyle(s[i+1:i+1+j], italic)...)
				i += j + 2
			} else {
				buf.WriteByte(s[i])
				i++
			}

		case s[i] == '!' && i+1 < len(s) && s[i+1] == '[':
			alt, target, next, ok := parseLinkTarget(s, i+1)
			if !ok {
				buf.WriteByte(s[i])
				i++
				continue
			}
			flush()
			if alt != "" {
				buf.WriteString(alt)
				flush()
			}
			if target != "" {
				out = append(out, run{text: "（" + target + "）", style: st})
			}
			i = next

		case s[i] == '[':
			text, target, next, ok := parseLinkTarget(s, i)
			if !ok {
				buf.WriteByte(s[i])
				i++
				continue
			}
			flush()
			out = append(out, parseInlineStyle(text, st)...)
			if target != "" && !strings.HasPrefix(target, "#") {
				out = append(out, run{text: "（" + target + "）", style: st})
			}
			i = next

		default:
			buf.WriteByte(s[i])
			i++
		}
	}
	flush()
	return out
}

// parseLinkTarget 解析 [text](url) 结构，返回文本、地址和结束位置。
func parseLinkTarget(s string, start int) (text, target string, next int, ok bool) {
	if start >= len(s) || s[start] != '[' {
		return "", "", 0, false
	}
	closeBracket := strings.IndexByte(s[start+1:], ']')
	if closeBracket < 0 {
		return "", "", 0, false
	}
	textEnd := start + 1 + closeBracket
	paren := textEnd + 1
	if paren >= len(s) || s[paren] != '(' {
		return "", "", 0, false
	}
	closeParen := strings.IndexByte(s[paren+1:], ')')
	if closeParen < 0 {
		return "", "", 0, false
	}
	target = strings.TrimSpace(s[paren+1 : paren+1+closeParen])
	// 去掉链接 title：url "标题"
	if sp := strings.IndexAny(target, " \t"); sp >= 0 {
		target = target[:sp]
	}
	return s[start+1 : textEnd], target, paren + 1 + closeParen + 1, true
}

func boundary(s string, i int) bool {
	if i == 0 {
		return true
	}
	prev, _ := utf8.DecodeLastRuneInString(s[:i])
	return unicode.IsSpace(prev) || strings.ContainsRune("（(【[，,。.：:；;、", prev)
}

// plainText 去掉行内 Markdown 标记，只保留文字（用于文档属性里的标题）。
func plainText(s string) string {
	var b strings.Builder
	for _, r := range parseInline(s) {
		b.WriteString(r.text)
	}
	return strings.TrimSpace(b.String())
}
