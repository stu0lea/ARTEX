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
