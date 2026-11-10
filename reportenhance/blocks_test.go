package reportenhance

import (
	"strings"
	"testing"
)

func TestParseTable(t *testing.T) {
	md := "## 概述\n\n| 字段 | 值 |\n| --- | --- |\n| 资产 | a.com |\n| 端口 | 80 |\n\n正文"
	bs := ParseReport(md)
	for _, b := range bs {
		if b.Kind == "table" {
			if len(b.Rows) != 3 {
				t.Fatalf("rows=%d", len(b.Rows))
			}
			return
		}
	}
	t.Fatal("no table block")
}

func TestBlocksToParasTable(t *testing.T) {
	out := blocksToParas([]Block{{Kind: "table", Rows: [][]string{{"a", "b"}, {"1", "2"}}}})
	if len(out) != 1 || !strings.HasPrefix(out[0], "\uE005") {
		t.Fatalf("bad: %q", out)
	}
}

func TestSplitAtEvidenceKeepsOnlyThreeSections(t *testing.T) {
	md := "# 标题\n\n- 目标：x\n\n## 概述\n\naaa\n\n## 影响与危害\n\nbbb\n\n## 复现步骤\n\nccc\n\n## 证据\n\nddd\n\n## 根因分析\n\neee\n\n## PoC\n\nfff\n\n## 修复建议\n\nggg"
	bs := trimIntro(ParseReport(md))
	desc, after := splitAtEvidence(bs)
	d := strings.Join(desc, "\n")
	a := strings.Join(after, "\n")
	for _, want := range []string{"概述", "aaa", "复现步骤", "ccc", "证据", "ddd"} {
		if !strings.Contains(d, want) {
			t.Fatalf("desc missing %q: %q", want, d)
		}
	}
	for _, drop := range []string{"影响与危害", "bbb", "受影响范围", "根因分析", "eee", "PoC", "fff", "修复建议", "ggg"} {
		if strings.Contains(d, drop) {
			t.Fatalf("desc should drop %q: %q", drop, d)
		}
	}
	if !strings.Contains(a, "ggg") || strings.Contains(a, "修复建议") {
		t.Fatalf("after bad: %q", a)
	}
}
