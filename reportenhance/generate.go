package reportenhance

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/report"
	"github.com/Autumn-27/artex/reportenhance/md2docx"
)

// GenerateOptions 是生成一份 Word 报告所需的任务信息。
type GenerateOptions struct {
	TaskDesc string
	Date     string
}

// Generate 把一批 findings 转成 Markdown，再按模板渲染成 docx 字节。
// 请求/响应报文只作为 Markdown 代码块排进 Word，不再生成截图。
func Generate(ctx context.Context, opts GenerateOptions, findings []*db.DBFinding) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	date := strings.TrimSpace(opts.Date)
	if date == "" {
		date = time.Now().Format("2006年1月2日")
	}
	md := report.FindingsMarkdownForDocx(findings, time.Now())
	data := md2docx.FromMarkdown(md, "", md2docx.ConvertOptions{
		Project: projectName(opts.TaskDesc),
		Date:    date,
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	tpl, source, err := loadTemplate()
	if err != nil {
		return nil, err
	}
	if source != "embedded" {
		log.Printf("[reportenhance] 使用运行时 Word 模板: %s", source)
	}
	out, err := md2docx.Render(tpl, data)
	if err != nil {
		return nil, fmt.Errorf("渲染 Word 失败: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("渲染 Word 失败: 输出为空")
	}
	return out, nil
}

// projectName 取任务描述首行并截断，避免把整份范围清单塞进封面。
func projectName(s string) string {
	s = strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
	if s == "" {
		return "目标"
	}
	r := []rune(s)
	if len(r) > 40 {
		return string(r[:40])
	}
	return s
}
