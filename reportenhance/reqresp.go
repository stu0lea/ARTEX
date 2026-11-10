// Package reportenhance 为报告交付提供增强能力：请求/响应报文截图渲染、
// 证据块收集与 Word 导出。它不改动现有 report 包的 Markdown 报告链路。
package reportenhance

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

// ReqRespInput 描述一张「请求/响应报文截图」的输入。
// 除 ReqBody/RespBody 外，其余字段都直接来自 db.FindingTrafficBinding.Snapshot。
type ReqRespInput struct {
	FindingID int64
	BindingID int64

	VulnClass string
	Severity  string

	Method string
	URL    string
	Status int

	ReqHead  string
	ReqBody  []byte
	RespHead string
	RespBody []byte
	RespCT   string // 响应 Content-Type，用于 charset 判定

	Highlight  []string // 需要标黄的 payload
	MaxLines   int      // 每侧正文最多行数；0 = 默认
	CapturedAt time.Time
	Hash       string
}

const (
	defaultMaxLines = 22
	stackMaxLines   = 40
	bodyHeightSplit = 318
	bodyHeightStack = 300
)

var (
	ctCharsetRe   = regexp.MustCompile(`(?i)charset[ \t]*=[ \t]*["']?[ \t]*([a-z0-9_\-]+)`)
	metaCharsetRe = regexp.MustCompile(`(?i)<meta[^>]+charset[ \t]*=[ \t]*["']?[ \t]*([a-z0-9_\-]+)`)
)

// DecodeBody 把报文正文转成 UTF-8。优先级：Content-Type → <meta charset> →
// UTF-8 合法则直接用 → GB18030 兜底。GBK/GB2312 站点不转换会在截图里显示乱码。
func DecodeBody(raw []byte, contentType string) string {
	if len(raw) == 0 {
		return ""
	}
	if cs := detectCharset(raw, contentType); cs != "" {
		if s, ok := transcode(raw, cs); ok {
			return s
		}
	}
	if utf8.Valid(raw) {
		return string(raw)
	}
	if s, ok := transcode(raw, "gb18030"); ok {
		return s
	}
	return string(bytes.ToValidUTF8(raw, []byte("")))
}

func detectCharset(raw []byte, contentType string) string {
	if m := ctCharsetRe.FindStringSubmatch(contentType); len(m) == 2 {
		return strings.ToLower(m[1])
	}
	head := raw
	if len(head) > 2048 {
		head = head[:2048]
	}
	if m := metaCharsetRe.FindSubmatch(head); len(m) == 2 {
		return strings.ToLower(string(m[1]))
	}
	return ""
}

func transcode(raw []byte, charset string) (string, bool) {
	enc, err := htmlindex.Get(charset)
	if err != nil || enc == nil {
		return "", false
	}
	out, _, err := transform.Bytes(enc.NewDecoder(), raw)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// countLines 统计正文行数，用于决定版面。
func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// clipLines 截断超长正文，并在末尾标注原始行数。
func clipLines(s string, limit int) (string, bool) {
	if limit <= 0 {
		limit = defaultMaxLines
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= limit {
		return s, false
	}
	return strings.Join(lines[:limit], "\n") + fmt.Sprintf("\n… 截断，共 %d 行", len(lines)), true
}

// highlightHTML 先转义再标黄，避免 payload 里的标签被当成 HTML 执行。
func highlightHTML(s string, needles []string) string {
	out := html.EscapeString(s)
	for _, n := range needles {
		n = strings.TrimSpace(n)
		if len(n) < 3 || len(n) > 200 {
			continue // 太短会满屏标黄，太长基本不可能命中
		}
		esc := html.EscapeString(n)
		if !strings.Contains(out, esc) {
			continue
		}
		out = strings.ReplaceAll(out, esc, "<mark>"+esc+"</mark>")
	}
	return out
}

func statusText(code int) string {
	if code <= 0 {
		return "-"
	}
	switch {
	case code < 300:
		return fmt.Sprintf("%d OK", code)
	case code < 400:
		return fmt.Sprintf("%d REDIRECT", code)
	case code < 500:
		return fmt.Sprintf("%d CLIENT", code)
	default:
		return fmt.Sprintf("%d SERVER", code)
	}
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

type shotView struct {
	Layout      string
	Method      string
	URL         string
	StatusText  string
	ReqSize     string
	RespSize    string
	ReqHead     template.HTML
	ReqBody     template.HTML
	RespHead    template.HTML
	RespBody    template.HTML
	FooterLeft  string
	FooterRight string
	BodyHeight  int
}

// RenderReqRespHTML 渲染 Burp 风格的请求/响应对照截图 HTML。
func RenderReqRespHTML(in ReqRespInput) ([]byte, error) {
	reqBody := DecodeBody(in.ReqBody, "")
	respBody := DecodeBody(in.RespBody, in.RespCT)

	reqText := in.ReqHead + "\n\n" + reqBody
	respText := in.RespHead + "\n\n" + respBody

	// 默认双栏 + 22 行截断（进 Word 后约 70mm 高，一张 A4 能放多条漏洞）。
	// 只有调用方显式要求更多行（MaxLines >= stackMaxLines）时才切上下布局。
	layout := "split"
	maxLines := in.MaxLines
	if maxLines >= stackMaxLines {
		layout = "stack"
	} else if maxLines <= 0 {
		maxLines = defaultMaxLines
	}

	reqHead, reqBodyText := splitHeadBody(reqText)
	respHead, respBodyText := splitHeadBody(respText)
	reqClipped, _ := clipLines(reqBodyText, maxLines)
	respClipped, _ := clipLines(respBodyText, maxLines)

	bodyHeight := bodyHeightSplit
	if layout == "stack" {
		bodyHeight = bodyHeightStack
	}

	when := in.CapturedAt
	if when.IsZero() {
		when = time.Now()
	}
	left := fmt.Sprintf("finding #%d · evidence #%d", in.FindingID, in.BindingID)
	if in.VulnClass != "" {
		left += " · " + truncateRunes(in.VulnClass, 28)
	}
	right := when.Format("2006-01-02 15:04")
	if in.Hash != "" {
		right += " · sha256 " + in.Hash
	}

	var buf bytes.Buffer
	err := shotTemplate.Execute(&buf, shotView{
		Layout:      layout,
		Method:      strings.ToUpper(in.Method),
		URL:         in.URL,
		StatusText:  statusText(in.Status),
		ReqSize:     humanBytes(len(in.ReqBody)),
		RespSize:    humanBytes(len(in.RespBody)),
		ReqHead:     template.HTML(highlightHTML(reqHead, in.Highlight)),     //nolint:gosec // 已转义
		ReqBody:     template.HTML(highlightHTML(reqClipped, in.Highlight)),  //nolint:gosec
		RespHead:    template.HTML(highlightHTML(respHead, in.Highlight)),    //nolint:gosec
		RespBody:    template.HTML(highlightHTML(respClipped, in.Highlight)), //nolint:gosec
		FooterLeft:  left,
		FooterRight: right,
		BodyHeight:  bodyHeight,
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func splitHeadBody(s string) (head, body string) {
	if i := strings.Index(s, "\n\n"); i >= 0 {
		return s[:i], s[i+2:]
	}
	return s, ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

var shotTemplate = template.Must(template.New("shot").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><style>
*{box-sizing:border-box}
body{margin:0;background:#eef1f4;font-family:"PingFang SC","Microsoft YaHei",Arial,sans-serif}
.wrap{width:1600px;padding:20px}
.card{background:#fff;border:1px solid #d7dde3;border-radius:10px;overflow:hidden;box-shadow:0 2px 10px rgba(16,24,40,.06)}
.hd{display:flex;align-items:center;gap:12px;padding:12px 18px;background:#1f2937}
.method{font:700 14px/1 ui-monospace,Menlo,Consolas,monospace;background:#374151;color:#f3f4f6;padding:6px 11px;border-radius:6px;letter-spacing:.5px}
.url{font:13px/1.4 ui-monospace,Menlo,Consolas,monospace;color:#93c5fd;flex:1;word-break:break-all}
.status{font:700 12px/1 ui-monospace,Menlo,Consolas,monospace;padding:6px 11px;border-radius:6px;background:#065f46;color:#a7f3d0}
.cols{display:flex;align-items:stretch}
.col{flex:1;min-width:0;border-right:1px solid #e5e7eb}
.col:last-child{border-right:0}
.pt{display:flex;justify-content:space-between;padding:8px 16px;background:#f8fafc;border-bottom:1px solid #eef2f6;font:600 11.5px/1 "PingFang SC",sans-serif;color:#64748b;letter-spacing:1px}
pre{margin:0;padding:12px 16px;font:11.5px/1.55 ui-monospace,Menlo,Consolas,monospace;white-space:pre-wrap;word-break:break-all;color:#111827;background:#fff}
.hd2{color:#0f172a;font-weight:600;background:#f8fafc;border-bottom:1px dashed #e5e7eb}
.body{overflow:hidden}
mark{background:#fde68a;color:#7c2d12;font-weight:700;padding:0 2px;border-radius:2px}
.ft{display:flex;justify-content:space-between;padding:9px 18px;background:#f8fafc;border-top:1px solid #eef2f6;font:11px/1 ui-monospace,Menlo,monospace;color:#94a3b8}
.stack .cols{flex-direction:column}
.stack .col{border-right:0;border-bottom:1px solid #e5e7eb}
.stack .col:last-child{border-bottom:0}
</style></head>
<body><div class="wrap"><div class="card {{.Layout}}">
<div class="hd"><span class="method">{{.Method}}</span><span class="url">{{.URL}}</span><span class="status">{{.StatusText}}</span></div>
<div class="cols">
<div class="col"><div class="pt"><span>请求 REQUEST</span><span>{{.ReqSize}}</span></div>
<pre class="hd2">{{.ReqHead}}</pre><pre class="body" style="height:{{.BodyHeight}}px">{{.ReqBody}}</pre></div>
<div class="col"><div class="pt"><span>响应 RESPONSE</span><span>{{.RespSize}}</span></div>
<pre class="hd2">{{.RespHead}}</pre><pre class="body" style="height:{{.BodyHeight}}px">{{.RespBody}}</pre></div>
</div>
<div class="ft"><span>{{.FooterLeft}}</span><span>{{.FooterRight}}</span></div>
</div></div></body></html>`))
