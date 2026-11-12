package reportenhance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitJob(t *testing.T, m *Manager, taskID int64) *Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j := m.Status(taskID)
		if j != nil && j.State != "running" {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("任务 %d 在超时前未完成", taskID)
	return nil
}

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

	m := NewManager(dir)
	j := m.Status(5)
	if j == nil || j.State != "done" || filepath.Base(j.File) != "new.docx" {
		t.Fatalf("bad job: %+v", j)
	}
	if m.Status(6) != nil {
		t.Fatal("unexpected report for empty task")
	}
}

func TestLoadTemplateUsesRuntimeOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tpl.docx")
	if err := os.WriteFile(path, []byte("runtime-template"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTEX_REPORT_TEMPLATE", path)
	b, src, err := loadTemplate()
	if err != nil || string(b) != "runtime-template" || src != path {
		t.Fatalf("override not used: src=%q err=%v", src, err)
	}
}

func TestLoadTemplateRejectsMissingOverride(t *testing.T) {
	t.Setenv("ARTEX_REPORT_TEMPLATE", filepath.Join(t.TempDir(), "missing.docx"))
	if _, _, err := loadTemplate(); err == nil {
		t.Fatal("expected error for missing runtime template")
	}
}

func TestLoadTemplateFallsBackToPublic(t *testing.T) {
	t.Setenv("ARTEX_REPORT_TEMPLATE", "")
	b, src, err := loadTemplate()
	if err != nil || len(b) == 0 || src != "embedded" {
		t.Fatalf("public template fallback failed: src=%q len=%d err=%v", src, len(b), err)
	}
}

func TestManagerStartWritesReport(t *testing.T) {
	m := NewManager(t.TempDir())
	j := m.Start(7, func(ctx context.Context) ([]byte, error) {
		return []byte("docx-bytes"), nil
	})
	if j.State != "running" {
		t.Fatalf("initial state = %q, want running", j.State)
	}
	done := waitJob(t, m, 7)
	if done.State != "done" || done.File == "" {
		t.Fatalf("bad job: %+v", done)
	}
	got, err := os.ReadFile(done.File)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "docx-bytes" {
		t.Fatalf("file content = %q", got)
	}
}

func TestManagerReusesRunningJob(t *testing.T) {
	m := NewManager(t.TempDir())
	release := make(chan struct{})
	first := m.Start(8, func(ctx context.Context) ([]byte, error) {
		<-release
		return []byte("docx"), nil
	})
	second := m.Start(8, func(ctx context.Context) ([]byte, error) {
		t.Error("second render callback should not run")
		return nil, nil
	})
	if first.ID != second.ID {
		t.Fatalf("job not reused: %s != %s", first.ID, second.ID)
	}
	close(release)
	if j := waitJob(t, m, 8); j.State != "done" {
		t.Fatalf("job failed: %+v", j)
	}
}

func TestManagerStartFailure(t *testing.T) {
	m := NewManager(t.TempDir())
	m.Start(9, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("boom")
	})
	j := waitJob(t, m, 9)
	if j.State != "failed" || j.Error != "boom" || j.File != "" {
		t.Fatalf("bad failed job: %+v", j)
	}
}
