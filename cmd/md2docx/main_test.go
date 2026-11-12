package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalizeArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "positional input",
			in:   []string{"md2docx", "report.md", "-out", "out.docx"},
			want: []string{"md2docx", "-out", "out.docx", "-in", "report.md"},
		},
		{
			name: "positional input after flags",
			in:   []string{"md2docx", "-out", "out.docx", "report.md"},
			want: []string{"md2docx", "-out", "out.docx", "-in", "report.md"},
		},
		{
			name: "flag values are not positional",
			in:   []string{"md2docx", "-title", "SQL 注入", "-out", "out.docx", "report.md"},
			want: []string{"md2docx", "-title", "SQL 注入", "-out", "out.docx", "-in", "report.md"},
		},
		{
			name: "stdin positional",
			in:   []string{"md2docx", "-", "-out", "-"},
			want: []string{"md2docx", "-out", "-", "-in", "-"},
		},
		{
			name: "equals form",
			in:   []string{"md2docx", "-in=report.md", "-out=out.docx"},
			want: []string{"md2docx", "-in=report.md", "-out=out.docx"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeArgs(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizeArgsRejectsMultipleInputs(t *testing.T) {
	if _, err := normalizeArgs([]string{"md2docx", "a.md", "b.md"}); err == nil {
		t.Fatal("expected error for multiple positional inputs")
	}
	if _, err := normalizeArgs([]string{"md2docx", "-in", "a.md", "b.md"}); err == nil {
		t.Fatal("expected error when both -in and positional input are given")
	}
}

func TestCollectMarkdownRejectsNonMarkdown(t *testing.T) {
	dir := t.TempDir()
	docx := filepath.Join(dir, "report.docx")
	if err := os.WriteFile(docx, []byte("not markdown"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := collectMarkdown(docx); err == nil {
		t.Fatal("expected error for non-markdown input")
	}
	md := filepath.Join(dir, "report.md")
	if err := os.WriteFile(md, []byte("# 标题\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := collectMarkdown(md)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != md {
		t.Fatalf("files = %v", files)
	}
}
