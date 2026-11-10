package reportenhance

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Autumn-27/artex/db"
)

var (
	stepNumRe  = regexp.MustCompile(`^\s*(\d{1,2})\s*[.、)）]\s*(.+)$`)
	stepWordRe = regexp.MustCompile(`步骤\s*#?\s*(\d{1,2})\s*[.、:：]?\s*(.*)$`)
)

// StepCaptions 从详细报告的「复现步骤」小节抽取 步骤号 -> 首行说明。
// 找不到小节时退化为全文匹配「步骤 N」。
func StepCaptions(md string) map[int]string {
	out := map[int]string{}
	_, sections := SplitSections(md)
	body := ""
	for k, v := range sections {
		if strings.Contains(k, "复现") || strings.Contains(k, "验证步骤") {
			body = v
			break
		}
	}
	if body != "" {
		step := 0
		for _, line := range strings.Split(body, "\n") {
			m := stepNumRe.FindStringSubmatch(line)
			if len(m) != 3 {
				continue
			}
			step++
			if _, ok := out[step]; !ok {
				out[step] = truncateRunes(cleanInline(m[2]), 60)
			}
		}
	}
	if len(out) == 0 {
		for _, line := range strings.Split(md, "\n") {
			if m := stepWordRe.FindStringSubmatch(line); len(m) == 3 {
				n := 0
				fmt.Sscanf(m[1], "%d", &n)
				if n > 0 {
					if _, ok := out[n]; !ok {
						out[n] = truncateRunes(cleanInline(m[2]), 60)
					}
				}
			}
		}
	}
	return out
}

// PoCCaption 生成 PoC 截图图注：优先复现步骤原文，其次漏洞名称、类别。
func PoCCaption(md string, step int, f *db.DBFinding) string {
	if step > 0 {
		if c := StepCaptions(md)[step]; c != "" {
			return c
		}
	}
	if f != nil {
		if s := strings.TrimSpace(f.Name); s != "" {
			return truncateRunes(s, 60)
		}
		if s := strings.TrimSpace(f.VulnClass); s != "" {
			return truncateRunes(s, 60)
		}
	}
	return "PoC 验证"
}
