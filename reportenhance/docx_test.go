package reportenhance

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatusFallsBackToLatestReport(t *testing.T) {
	dir := t.TempDir()
	taskDir := filepath.Join(dir, "5")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(taskDir, "old.docx")
	newer := filepath.Join(taskDir, "new.docx")
	for _, p := range []string{old, newer} {
		if err := os.WriteFile(p, []byte("docx"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, base, base); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, base.Add(time.Minute), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir, nil)
	j := m.Status(5)
	if j == nil || j.State != "done" || filepath.Base(j.File) != "new.docx" {
		t.Fatalf("bad job: %+v", j)
	}
	if m.Status(6) != nil {
		t.Fatal("unexpected report for empty task")
	}
}

func TestTemplateBytesUsesRuntimeOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tpl.docx")
	if err := os.WriteFile(path, []byte("runtime-template"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTEX_REPORT_TEMPLATE", path)
	m := NewManager(t.TempDir(), nil)
	b, src, err := m.templateBytes()
	if err != nil || string(b) != "runtime-template" || src != path {
		t.Fatalf("override not used: src=%q err=%v", src, err)
	}
}

func TestTemplateBytesFallsBackToEmbedded(t *testing.T) {
	t.Setenv("ARTEX_REPORT_TEMPLATE", filepath.Join(t.TempDir(), "missing.docx"))
	m := NewManager(t.TempDir(), nil)
	b, src, err := m.templateBytes()
	if err != nil || len(b) == 0 || src != "embedded" {
		t.Fatalf("fallback failed: src=%q len=%d err=%v", src, len(b), err)
	}
}
