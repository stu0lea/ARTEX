package reportenhance

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestGenerateProducesDocxWithoutMedia(t *testing.T) {
	findings := []*db.DBFinding{
		{
			ID:              1,
			Name:            "登录接口 SQL 注入",
			VulnClass:       "SQL注入",
			Severity:        "high",
			Status:          "confirmed",
			Summary:         "登录接口存在注入。",
			CreatedAt:       time.Date(2026, 11, 1, 10, 0, 0, 0, time.Local),
			TaskDescription: "某单位门户网站渗透测试",
			Report: "# 登录接口 SQL 注入\n\n## 概述\n\n`username` 参数存在注入。\n\n" +
				"## 复现步骤\n\n1. 访问登录页\n2. 提交 payload\n\n" +
				"## 证据\n\n```http\nPOST /login HTTP/1.1\nHost: example.com\n```\n\n" +
				"## 修复建议\n\n使用参数化查询。\n",
			TrafficBindings: []db.FindingTrafficBinding{{
				ID:        11,
				FindingID: 1,
				Role:      "request",
				Snapshot: db.TrafficEvidenceSnapshot{
					URL:    "http://example.com/login",
					Method: "POST",
					Status: 200,
				},
			}},
			EvidenceVersion: 3,
		},
		{
			ID:              2,
			Name:            "后台未授权访问",
			VulnClass:       "未授权访问",
			Severity:        "critical",
			Status:          "confirmed",
			Summary:         "后台可直接访问。",
			CreatedAt:       time.Date(2026, 11, 2, 10, 0, 0, 0, time.Local),
			TaskDescription: "某单位门户网站渗透测试",
			Report:          "# 后台未授权访问\n\n## 概述\n\n后台页面无需登录。\n\n## 修复建议\n\n增加鉴权。\n",
		},
	}
	out, err := Generate(context.Background(), GenerateOptions{
		TaskDesc: "某单位门户网站渗透测试",
		Date:     "2026年11月12日",
	}, findings)
	if err != nil {
		t.Fatal(err)
	}
	files := readZip(t, out)
	doc := string(files["word/document.xml"])
	for _, bad := range []string{"{{", "{%", "}}", "%}"} {
		if strings.Contains(doc, bad) {
			t.Fatalf("模板标签残留: %s", bad)
		}
	}
	for _, want := range []string{
		"某单位门户网站渗透测试",
		"2026年11月12日",
		"登录接口 SQL 注入",
		"后台未授权访问",
		"POST /login HTTP/1.1",
		"使用参数化查询",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("document missing %q", want)
		}
	}
	if strings.Contains(doc, "关联流量证据") {
		t.Fatal("Word 报告不应包含流量证据章节")
	}
	for name := range files {
		if strings.HasPrefix(name, "word/media/") {
			t.Fatalf("公开模板与无截图流程不应产生图片: %s", name)
		}
	}
}

func readZip(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]byte, len(zr.File))
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

func TestGenerateEmptyFindings(t *testing.T) {
	out, err := Generate(context.Background(), GenerateOptions{
		TaskDesc: "空任务",
		Date:     "2026年11月12日",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := readZip(t, out)
	if files["word/document.xml"] == nil {
		t.Fatal("missing document.xml")
	}
	for name := range files {
		if strings.HasPrefix(name, "word/media/") {
			t.Fatalf("不应产生图片: %s", name)
		}
	}
}

func TestGenerateSummaryOnlyFinding(t *testing.T) {
	f := &db.DBFinding{
		ID: 1, Name: "仅摘要漏洞", Severity: "medium", Status: "pending",
		Summary: "这个漏洞只有概述，没有详细报告。", CreatedAt: time.Now(),
		TaskDescription: "测试任务",
	}
	out, err := Generate(context.Background(), GenerateOptions{TaskDesc: "测试任务", Date: "2026年11月12日"}, []*db.DBFinding{f})
	if err != nil {
		t.Fatal(err)
	}
	doc := string(readZip(t, out)["word/document.xml"])
	if !strings.Contains(doc, "这个漏洞只有概述，没有详细报告。") {
		t.Fatal("仅摘要漏洞的概述未写入 Word")
	}
}
