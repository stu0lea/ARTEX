// md2docx 把 ARTEX 导出的漏洞 Markdown 报告转成 Word 文档。
// 支持单个漏洞报告和「漏洞发现汇总报告」；汇总报告会一次生成一个包含全部漏洞的 docx。
//
//	go build -o md2docx ./cmd/md2docx
//	./md2docx finding.md -out finding.docx
//	./md2docx -in finding.md -out finding.docx
//	./md2docx -in findings-20261110.md -out summary.docx
//	./md2docx -in ./export -out ./word
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Autumn-27/artex/reportenhance"
	"github.com/Autumn-27/artex/reportenhance/md2docx"
)

func main() {
	var (
		in       = flag.String("in", "", "Markdown 文件、目录或 -（标准输入）；也可直接作为位置参数传入")
		out      = flag.String("out", "", "输出 docx 路径；批量时为输出目录；- 表示标准输出")
		template = flag.String("template", "", "Word 模板；默认使用内置公开模板，可用本参数或 ARTEX_REPORT_TEMPLATE 覆盖")
		baseDir  = flag.String("base-dir", "", "Markdown 中图片相对路径的基准目录；-in - 时默认为当前目录")
		project  = flag.String("project", "", "可选：覆盖报告项目名，默认从 Markdown 提取")
		title    = flag.String("title", "", "可选：覆盖漏洞标题，默认从 Markdown 提取")
		location = flag.String("location", "", "可选：覆盖漏洞位置，默认从 Markdown 提取")
		severity = flag.String("severity", "", "可选：覆盖严重等级 critical/high/medium/low/info，默认从 Markdown 提取")
		date     = flag.String("date", "", "可选：覆盖报告日期，如 2026年11月10日，默认从 Markdown 提取")
	)
	argv, err := normalizeArgs(os.Args)
	if err != nil {
		fail(err)
	}
	if err := flag.CommandLine.Parse(argv[1:]); err != nil {
		fail(err)
	}

	if strings.TrimSpace(*in) == "" {
		fmt.Fprintln(os.Stderr, "用法: md2docx <报告.md|目录|-> [-out <输出.docx|目录|->]")
		flag.PrintDefaults()
		os.Exit(2)
	}

	tpl, err := loadTemplate(*template)
	if err != nil {
		fail(err)
	}
	opts := md2docx.ConvertOptions{
		Project:  *project,
		Title:    *title,
		Location: *location,
		Severity: *severity,
		Date:     *date,
	}

	// 标准输入模式：直接把管道里的 Markdown 转成 docx，供 skill/工具集成。
	if *in == "-" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			fail(fmt.Errorf("读取标准输入失败: %w", err))
		}
		if strings.TrimSpace(*out) == "" {
			fail(fmt.Errorf("从标准输入读取时必须用 -out 指定输出路径，或使用 -out - 输出到标准输出"))
		}
		dir := *baseDir
		if dir == "" {
			dir, _ = os.Getwd()
		}
		data := md2docx.FromMarkdown(string(raw), dir, opts)
		writeResult(tpl, data, *out)
		return
	}

	files, err := collectMarkdown(*in)
	if err != nil {
		fail(err)
	}
	if len(files) == 0 {
		fail(fmt.Errorf("没有找到 Markdown 文件: %s", *in))
	}
	if *out == "-" && len(files) > 1 {
		fail(fmt.Errorf("-out - 只能用于单个 Markdown 文件"))
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
		dir := filepath.Dir(file)
		if *baseDir != "" {
			dir = *baseDir
		}
		data := md2docx.FromMarkdown(string(raw), dir, opts)
		if *out == "-" {
			writeResult(tpl, data, "-")
			return
		}
		if err := md2docx.RenderToFile(tpl, data, dest); err != nil {
			fail(fmt.Errorf("生成 %s 失败: %w", file, err))
		}
		fmt.Printf("%s -> %s\n", file, dest)
	}
}

// normalizeArgs 允许把输入路径直接写在命令行里，例如 md2docx report.md -out out.docx。
// 标准库 flag 遇到第一个位置参数就停止解析，这里先把它转换成 -in，保证后面的参数仍生效。
func normalizeArgs(args []string) ([]string, error) {
	if len(args) <= 1 {
		return args, nil
	}
	takesValue := map[string]bool{
		"in": true, "out": true, "template": true, "base-dir": true,
		"project": true, "title": true, "location": true, "severity": true, "date": true,
	}
	var positional []string
	out := []string{args[0]}
	for i := 1; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		out = append(out, a)
		name := strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			continue
		}
		if takesValue[name] && i+1 < len(args) {
			i++
			out = append(out, args[i])
		}
	}
	if len(positional) > 1 {
		return nil, fmt.Errorf("只能指定一个输入路径，收到 %d 个: %s", len(positional), strings.Join(positional, ", "))
	}
	if len(positional) == 1 && hasFlag(out, "in") {
		return nil, fmt.Errorf("不能同时使用 -in 和位置参数指定输入路径")
	}
	if len(positional) == 1 {
		out = append(out, "-in", positional[0])
	}
	return out, nil
}

func hasFlag(args []string, name string) bool {
	for _, a := range args[1:] {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		key := strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(key, '='); eq >= 0 {
			key = key[:eq]
		}
		if key == name {
			return true
		}
	}
	return false
}

// writeResult 把生成结果写到文件或标准输出；标准输出只写 docx 二进制。
func writeResult(tpl []byte, data *md2docx.DocData, out string) {
	if out == "-" {
		b, err := md2docx.Render(tpl, data)
		if err != nil {
			fail(fmt.Errorf("生成 docx 失败: %w", err))
		}
		if _, err := os.Stdout.Write(b); err != nil {
			fail(fmt.Errorf("写入标准输出失败: %w", err))
		}
		return
	}
	if info, err := os.Stat(out); err == nil && info.IsDir() {
		out = filepath.Join(out, "report.docx")
	}
	if err := md2docx.RenderToFile(tpl, data, out); err != nil {
		fail(fmt.Errorf("生成 %s 失败: %w", out, err))
	}
	fmt.Fprintf(os.Stderr, "stdin -> %s\n", out)
}

func loadTemplate(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		path = strings.TrimSpace(os.Getenv("ARTEX_REPORT_TEMPLATE"))
	}
	if path == "" {
		return reportenhance.PublicTemplate(), nil
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
		if !strings.EqualFold(filepath.Ext(in), ".md") {
			return nil, fmt.Errorf("只支持 Markdown 文件（.md）: %s", in)
		}
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
