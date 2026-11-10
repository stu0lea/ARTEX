package reportenhance

import (
	"regexp"
	"strings"
)

// Block 是详细报告 Markdown 拆出来的一个排版单元。
// Kind: text（普通段落）/ code（等宽代码块）。图片不在这里处理，
// PoC 截图按文件命名约定单独收集。
type Block struct {
	Kind  string
	Text  string
	Level int // 仅 heading 使用：1 = #，2 = ## …
}

var (
	mdImageRe   = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)
	mdLinkRe    = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	mdTableSep  = regexp.MustCompile(`^\s*\|?[\s:|-]+\|[\s:|-]*$`)
	mdHTMLTagRe = regexp.MustCompile(`<[^>]{1,80}>`)
)

// ParseReport 把 finding.Report 的 Markdown 转成顺序段落列表。
// 只做报告排版需要的轻量处理：标题、列表、表格、代码块、去内联标记。
func ParseReport(md string) []Block {
	const maxBlocks = 160
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	out := make([]Block, 0, 64)
	var code []string
	inCode := false

	flushCode := func() {
		if len(code) > 0 {
			out = append(out, Block{Kind: "code", Text: strings.Join(code, "\n")})
			code = nil
		}
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				flushCode()
				inCode = false
			} else {
				inCode = true
			}
			continue
		}
		if inCode {
			code = append(code, line)
			continue
		}
		if trimmed == "" || trimmed == "---" || trimmed == "***" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if t := cleanInline(trimmed); t != "" {
				lvl := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
				out = append(out, Block{Kind: "heading", Text: t, Level: lvl})
			}
			continue
		}
		if mdTableSep.MatchString(trimmed) && strings.Contains(trimmed, "|") {
			continue // 表格分隔行
		}
		if strings.Contains(trimmed, "|") && strings.HasPrefix(trimmed, "|") {
			cells := splitTableRow(trimmed)
			if len(cells) > 0 {
				out = append(out, Block{Kind: "text", Text: strings.Join(cells, " ｜ ")})
			}
		} else {
			out = append(out, Block{Kind: "text", Text: cleanInline(trimmed)})
		}
		if len(out) >= maxBlocks {
			break
		}
	}
	flushCode()
	return out
}

func splitTableRow(line string) []string {
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := cleanInline(strings.TrimSpace(p)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// cleanInline 去掉不影响阅读的 Markdown 标记，保留文字与链接目标。
func cleanInline(s string) string {
	s = mdImageRe.ReplaceAllString(s, "")
	s = mdLinkRe.ReplaceAllString(s, "$1（$2）")
	s = mdHTMLTagRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.ReplaceAll(s, "`", "")
	if strings.HasPrefix(s, "#") {
		s = strings.TrimSpace(strings.TrimLeft(s, "#"))
	}
	// 列表项统一成圆点，避免 Word 里出现裸 "-"。
	if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") {
		s = "• " + strings.TrimSpace(s[2:])
	} else if strings.HasPrefix(s, "+ ") {
		s = "• " + strings.TrimSpace(s[2:])
	}
	return strings.TrimSpace(s)
}

// SplitSections 按二级标题（## ）把 Markdown 切成小节，返回 标题 -> 正文。
// 用于把「修复建议」单独拿出来放进模板的漏洞建议一栏。
func SplitSections(md string) (intro string, sections map[string]string) {
	sections = map[string]string{}
	var current string
	var buf []string
	var pre []string
	flush := func() {
		if current == "" {
			intro = strings.Join(buf, "\n")
		} else {
			sections[current] = strings.Join(buf, "\n")
		}
		buf = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "## ") {
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "## "))
			current = strings.TrimSpace(strings.TrimLeft(current, "#"))
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "# ") && current == "" {
			pre = append(pre, line)
			continue
		}
		buf = append(buf, line)
	}
	flush()
	if len(pre) > 0 {
		intro = strings.Join(pre, "\n") + "\n" + intro
	}
	return intro, sections
}
