package report

import (
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestFindingsMarkdownForDocxOmitsTrafficSection(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.Local)
	f := &db.DBFinding{
		ID:        1,
		Name:      "SQL 注入",
		VulnClass: "SQL注入",
		Severity:  "high",
		Status:    "confirmed",
		CreatedAt: now,
		Report:    "## 证据\n\n```http\nPOST /login HTTP/1.1\nHost: example.com\n```\n",
		TrafficBindings: []db.FindingTrafficBinding{{
			ID: 9,
			Snapshot: db.TrafficEvidenceSnapshot{
				URL:    "http://example.com/login",
				Method: "POST",
				Status: 200,
			},
		}},
	}
	full := FindingsMarkdown([]*db.DBFinding{f}, now)
	if !strings.Contains(full, "关联流量证据") {
		t.Fatal("常规导出应包含流量证据章节")
	}
	word := FindingsMarkdownForDocx([]*db.DBFinding{f}, now)
	if strings.Contains(word, "关联流量证据") {
		t.Fatal("Word 导出不应包含流量证据章节")
	}
	if !strings.Contains(word, "POST /login HTTP/1.1") {
		t.Fatal("Word 导出应保留正文中的请求报文代码块")
	}
}
