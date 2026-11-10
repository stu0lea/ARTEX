// md2docx 把 ARTEX 导出的漏洞 Markdown 报告转成与模板一致的 Word 文档。
// 支持单个漏洞报告和「漏洞发现汇总报告」；汇总报告会一次生成一个包含全部漏洞的 docx。
//
//	go build -o md2docx ./cmd/md2docx
//	./md2docx -in finding.md -out finding.docx
//	./md2docx -in findings-20261110.md -out summary.docx
//	./md2docx -in ./export -out ./word
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Autumn-27/artex/internal/md2docx"
)

func main() {
	var (
		in       = flag.String("in", "", "Markdown 文件或目录（必填）")
		out      = flag.String("out", "", "输出 docx 路径；批量时为输出目录")
		template = flag.String("template", "", "Word 模板；默认读取 ARTEX_REPORT_TEMPLATE，否则使用内置模板")
		project  = flag.String("project", "", "覆盖报告项目名")
		title    = flag.String("title", "", "覆盖漏洞标题")
		location = flag.String("location", "", "覆盖漏洞位置")
		severity = flag.String("severity", "", "覆盖严重等级：critical/high/medium/low/info")
		date     = flag.String("date", "", "覆盖报告日期，如 2026年11月10日")
	)
	flag.Parse()

	if strings.TrimSpace(*in) == "" {
		fmt.Fprintln(os.Stderr, "用法: md2docx -in <报告.md|目录> [-out <输出.docx|目录>]")
		flag.PrintDefaults()
		os.Exit(2)
	}

	tpl, err := loadTemplate(*template)
	if err != nil {
		fail(err)
	}
	files, err := collectMarkdown(*in)
	if err != nil {
		fail(err)
	}
	if len(files) == 0 {
		fail(fmt.Errorf("没有找到 Markdown 文件: %s", *in))
	}

	opts := md2docx.ConvertOptions{
		Project:  *project,
		Title:    *title,
		Location: *location,
		Severity: *severity,
		Date:     *date,
	}

	info, _ := os.Stat(*in)
	root := ""
	if info != nil && info.IsDir() {
		root = *in
	}
	for _, file := range files {
		dest, err := outputPath(root, file, *out, len(files) > 1)
		if err != nil {
			fail(err)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			fail(err)
		}
		data := md2docx.FromMarkdown(string(raw), filepath.Dir(file), opts)
		if err := md2docx.RenderToFile(tpl, data, dest); err != nil {
			fail(fmt.Errorf("生成 %s 失败: %w", file, err))
		}
		fmt.Printf("%s -> %s\n", file, dest)
	}
}

func loadTemplate(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		path = strings.TrimSpace(os.Getenv("ARTEX_REPORT_TEMPLATE"))
	}
	if path == "" {
		// 在仓库里直接运行时，自动使用现有的客户模板。
		candidates := []string{"reportenhance/assets/report-template-auto.docx"}
		if exe, err := os.Executable(); err == nil {
			dir := filepath.Dir(exe)
			candidates = append(candidates,
				filepath.Join(dir, "report-template-auto.docx"),
				filepath.Join(dir, "assets", "report-template-auto.docx"))
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
		}
	}
	if path == "" {
		return md2docx.FallbackTemplate(), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取模板 %s 失败: %w", path, err)
	}
	return b, nil
}

func collectMarkdown(in string) ([]string, error) {
	info, err := os.Stat(in)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{in}, nil
	}
	var files []string
	err = filepath.WalkDir(in, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func outputPath(root, file, out string, batch bool) (string, error) {
	if !batch {
		if out == "" {
			return strings.TrimSuffix(file, filepath.Ext(file)) + ".docx", nil
		}
		if info, err := os.Stat(out); err == nil && info.IsDir() {
			name := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)) + ".docx"
			return filepath.Join(out, name), nil
		}
		return out, nil
	}
	if out == "" {
		out = root
	}
	if info, err := os.Stat(out); err == nil && !info.IsDir() {
		return "", fmt.Errorf("批量转换时 -out 必须是目录: %s", out)
	}
	rel, err := filepath.Rel(root, file)
	if err != nil {
		rel = filepath.Base(file)
	}
	rel = strings.TrimSuffix(rel, filepath.Ext(rel)) + ".docx"
	return filepath.Join(out, rel), nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "md2docx:", err)
	os.Exit(1)
}
