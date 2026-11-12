package reportenhance

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
)

// 公开模板不含公司名称与 logo，作为编译进二进制的默认模板。
//
//go:embed assets/report-template-public.docx
var publicTemplate []byte

// PublicTemplate 返回编译进二进制的公开模板，供 CLI 等调用方复用。
func PublicTemplate() []byte {
	return publicTemplate
}

// loadTemplate 优先使用 ARTEX_REPORT_TEMPLATE 指定的内部模板，
// 未设置时使用内置公开模板。
func loadTemplate() ([]byte, string, error) {
	path := strings.TrimSpace(os.Getenv("ARTEX_REPORT_TEMPLATE"))
	if path == "" {
		return publicTemplate, "embedded", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, path, fmt.Errorf("读取 Word 模板 %s 失败: %w", path, err)
	}
	return b, path, nil
}
