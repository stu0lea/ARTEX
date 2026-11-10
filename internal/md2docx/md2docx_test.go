package md2docx

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 12, G: 34, B: 56, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

const sampleFinding = "# [HIGH] SQL 注入\n\n" +
	"- **类别**：SQL注入\n" +
	"- **严重等级**：high\n" +
	"- **所属任务**：某单位门户网站渗透测试\n" +
	"- **发现时间**：2026-11-01 10:00:00\n\n" +
	"## 概述\n\n简要说明。\n\n" +
	"## 详细报告\n\n" +
	"# 登录接口 SQL 注入\n\n" +
	"- **类型**：SQL注入\n" +
	"- **等级**：高危\n\n" +
	"## 概述\n\n登录接口 http://example.com/login 的 `username` 参数存在注入。\n\n" +
	"## 复现步骤\n\n1. 访问登录页\n2. 提交 payload\n\n" +
	"| 参数 | 值 |\n| --- | --- |\n| username | `' or 1=1--` |\n\n" +
	"## 证据\n\n```http\nPOST /login HTTP/1.1\nHost: example.com\n```\n\n" +
	"![注入回显](shot.png)\n\n" +
	"## 影响与危害\n\n不应出现在 Word 里。\n\n" +
	"## 修复建议\n\n使用参数化查询，参考 `PreparedStatement`。\n"

func TestFromMarkdown(t *testing.T) {
	dir := t.TempDir()
	shot := filepath.Join(dir, "shot.png")
	writePNG(t, shot, 40, 20)

	data := FromMarkdown(sampleFinding, dir, ConvertOptions{})
	if data.ProjectName != "某单位门户网站渗透测试" {
		t.Fatalf("project = %q", data.ProjectName)
	}
	if data.DateCN != "2026年11月1日" {
		t.Fatalf("date = %q", data.DateCN)
	}
	if len(data.Systems) != 1 || len(data.Systems[0].Vulns) != 1 {
		t.Fatalf("systems = %+v", data.Systems)
	}
	v := data.Systems[0].Vulns[0]
	if v.Title != "SQL 注入" || v.Severity != "高危" {
		t.Fatalf("vuln = %+v", v)
	}
	if v.Location != "http://example.com/login" {
		t.Fatalf("location = %q", v.Location)
	}
	joined := strings.Join(v.Desc, "\n")
	for _, want := range []string{headSentinel + "概述", codeSentinel + "POST /login", tableSentinel, inlineCodeStart + "username" + inlineCodeEnd} {
		if !strings.Contains(joined, want) {
			t.Fatalf("desc missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "影响与危害") || strings.Contains(joined, "不应出现") {
		t.Fatalf("dropped section leaked: %s", joined)
	}
	if len(v.Shots) != 1 || !strings.HasSuffix(v.Shots[0].Path, "shot.png") {
		t.Fatalf("shots = %+v", v.Shots)
	}
	if !strings.HasPrefix(v.Shots[0].Caption, captionSentinel) {
		t.Fatalf("caption = %q", v.Shots[0].Caption)
	}
	after := strings.Join(v.After, "\n")
	if strings.Contains(after, "修复建议") {
		t.Fatalf("fix heading should be dropped: %s", after)
	}
	if !strings.Contains(after, inlineCodeStart+"PreparedStatement"+inlineCodeEnd) {
		t.Fatalf("after = %q", after)
	}
}

func TestRenderFallbackTemplate(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "shot.png"), 60, 30)
	data := FromMarkdown(sampleFinding, dir, ConvertOptions{})

	out, err := Render(FallbackTemplate(), data)
	if err != nil {
		t.Fatal(err)
	}
	files := unzip(t, out)
	doc := files["word/document.xml"]
	if doc == nil {
		t.Fatal("missing document.xml")
	}
	if err := ValidateXML(doc); err != nil {
		t.Fatalf("document.xml invalid: %v", err)
	}
	text := string(doc)
	for _, bad := range []string{"{{", "{%", "}}", "%}"} {
		if strings.Contains(text, bad) {
			t.Fatalf("template tag left in document: %s", bad)
		}
	}
	for _, want := range []string{"某单位门户网站渗透测试", "SQL 注入", "高危", "图 1  注入回显", "<w:tbl>"} {
		if !strings.Contains(text, want) {
			t.Fatalf("document missing %q", want)
		}
	}
	if _, ok := files["word/media/image1.png"]; !ok {
		t.Fatalf("image media missing, files=%v", keys(files))
	}
	rels := string(files["word/_rels/document.xml.rels"])
	if !strings.Contains(rels, "relationships/image") || !strings.Contains(rels, "media/image1.png") {
		t.Fatalf("image relationship missing: %s", rels)
	}
	if !strings.Contains(string(files["[Content_Types].xml"]), `Extension="png"`) {
		t.Fatal("png content type missing")
	}
	for name, b := range files {
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
			if err := ValidateXML(b); err != nil {
				t.Fatalf("%s invalid: %v", name, err)
			}
		}
	}
}

func TestRenderEscapesMarkup(t *testing.T) {
	data := &DocData{
		ProjectName: `<script>&"test"`,
		DateCN:      "2026年11月10日",
		Total:       1,
		High:        1,
		Risk:        "预警状态",
		Systems: []System{{Name: "a<&b", Vulns: []Vuln{{
			Title:    `<b>标题</b>`,
			Location: "http://x/?a=1&b=2",
			Desc:     []string{`正文 <br> & 符号`},
			Severity: "高危",
		}}}},
		Scope:  []ScopeRow{{Name: "a<&b", URL: "http://x/?a=1&b=2"}},
		Groups: []Group{{Name: "SQL<注入>", Count: 1, Severity: "高危"}},
	}
	out, err := Render(FallbackTemplate(), data)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(unzip(t, out)["word/document.xml"])
	if err := ValidateXML([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, "&lt;script&gt;&amp;") {
		t.Fatal("dynamic text was not escaped")
	}
}

func TestRenderSkipsMissingImage(t *testing.T) {
	data := &DocData{
		ProjectName: "p",
		Systems: []System{{Vulns: []Vuln{{
			Title: "t",
			Shots: []Shot{{Path: filepath.Join(t.TempDir(), "missing.png"), Caption: captionSentinel + "图"}},
		}}}},
	}
	out, err := Render(FallbackTemplate(), data)
	if err != nil {
		t.Fatal(err)
	}
	files := unzip(t, out)
	if _, ok := files["word/media/image1.png"]; ok {
		t.Fatal("unexpected media for missing image")
	}
	if strings.Contains(string(files["word/document.xml"]), "{{") {
		t.Fatal("leftover tag")
	}
}

func TestExpressions(t *testing.T) {
	data := &DocData{
		Total:  2,
		Groups: []Group{{Name: "SQL注入", Count: 2, Severity: "高危"}},
		Scope:  []ScopeRow{{Name: "门户", URL: "http://example.com"}},
		Systems: []System{{Vulns: []Vuln{{
			Title: "t",
			Desc:  []string{"a"},
			After: []string{"b"},
		}}}},
	}
	ctx := newValueCtx(data)
	ctx = ctx.child("v", data.Systems[0].Vulns[0])
	cases := map[string]string{
		`groups[0].name if groups|length > 0 else ""`: "SQL注入",
		`groups[3].name if groups|length > 3 else ""`: "",
		`scope[0].url`: "http://example.com",
		`v.title`:      "t",
		`total`:        "2",
		`"固定"`:         "固定",
	}
	for expr, want := range cases {
		if got := displayValue(ctx.eval(expr)); got != want {
			t.Fatalf("eval %q = %q, want %q", expr, got, want)
		}
	}
	if !truthy(ctx.eval("v.after")) {
		t.Fatal("non-empty after should be truthy")
	}
	ctx = ctx.child("v", Vuln{Title: "x"})
	if truthy(ctx.eval("v.after")) {
		t.Fatal("empty after should be falsy")
	}
}

func TestControlBlocks(t *testing.T) {
	p := func(text string) bodyNode {
		return bodyNode{kind: nodeParagraph, xml: `<w:p><w:r><w:t>` + text + `</w:t></w:r></w:p>`}
	}
	withAfter := []bodyNode{
		p(`{%p if v.after %}`), p("HAS-AFTER"), p(`{%p endif %}`),
	}
	r := &renderer{pkg: &docxPackage{}}
	ctx := newValueCtx(&DocData{})
	ctx = ctx.child("v", Vuln{After: []string{"x"}})
	out, err := r.renderNodes(withAfter, ctx)
	if err != nil || !strings.Contains(out, "HAS-AFTER") {
		t.Fatalf("if-true failed: %q err=%v", out, err)
	}
	ctx = ctx.child("v", Vuln{})
	out, err = r.renderNodes(withAfter, ctx)
	if err != nil || strings.Contains(out, "HAS-AFTER") {
		t.Fatalf("if-false failed: %q err=%v", out, err)
	}

	forLoop := []bodyNode{
		p(`{%p for p in v.desc %}`), p("D: {{ p }}"), p(`{%p endfor %}`),
	}
	ctx = ctx.child("v", Vuln{Desc: []string{"甲", "乙"}})
	out, err = r.renderNodes(forLoop, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "D: ") != 2 || !strings.Contains(out, ">甲<") || !strings.Contains(out, ">乙<") {
		t.Fatalf("for loop failed: %q", out)
	}
}

func TestReplaceTagsInTable(t *testing.T) {
	data := &DocData{Groups: []Group{{Name: "SQL注入", Count: 2, Severity: "高危"}}}
	ctx := newValueCtx(data)
	cell := `<w:tc><w:p><w:r><w:t>{{ groups[0].name if groups|length > 0 else "" }}</w:t></w:r></w:p></w:tc>`
	out := replaceTagsInXML(cell, ctx)
	if !strings.Contains(out, "SQL注入") || strings.Contains(out, "{{") {
		t.Fatalf("table tag replacement failed: %s", out)
	}
}

func unzip(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = data
	}
	return out
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
