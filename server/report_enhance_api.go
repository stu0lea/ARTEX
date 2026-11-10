package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/reportenhance"
)

// 报告增强（Word 导出）。真正的实现都在 reportenhance 包里，这里只做
// HTTP 适配：异步任务 + 状态轮询 + 文件下载。

var reportManagers sync.Map // dataDir -> *reportenhance.Manager

func (s *Server) reportManager() *reportenhance.Manager {
	dir := filepath.Join(s.m.dir, "reports")
	if v, ok := reportManagers.Load(dir); ok {
		return v.(*reportenhance.Manager)
	}
	m := reportenhance.NewManager(dir, reportenhance.NewRenderer(os.TempDir()))
	actual, _ := reportManagers.LoadOrStore(dir, m)
	return actual.(*reportenhance.Manager)
}

// POST /api/report/docx?task=N —— 启动一次 Word 生成（异步，立即返回 job）。
func (s *Server) generateReportDocx(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, 404, "no active task")
		return
	}
	tid, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil {
		writeErr(w, 400, "bad task id")
		return
	}
	findings, err := s.m.pg.ListFindingsForExport(db.FindingFilter{TaskID: t.ID}, nil)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	store := s.evidenceStore()
	if err := store.StageFindingsExport(r.Context(), findings, "", false); err != nil {
		evidenceError(w, err)
		return
	}
	assets := s.resolveFindingAssets(findings)
	opts := reportenhance.BuildOptions{
		TaskID:   tid,
		TaskDesc: t.Description,
		TaskDir:  filepath.Join(s.m.dir, "tasks", t.ID),
		ShotDir:  filepath.Join(s.m.dir, "reports", t.ID, "shots"),
		Store:    store,
		Renderer: reportenhance.NewRenderer(os.TempDir()),
		Assets:   assets,
	}
	job := s.reportManager().Start(tid, func(ctx context.Context) (*reportenhance.DocData, error) {
		return reportenhance.Build(ctx, opts, findings), nil
	})
	writeJSON(w, 200, job)
}

// GET /api/report/docx/status?task=N
func (s *Server) reportDocxStatus(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, 404, "no active task")
		return
	}
	tid, _ := strconv.ParseInt(t.ID, 10, 64)
	if job := s.reportManager().Status(tid); job != nil {
		writeJSON(w, 200, job)
		return
	}
	writeJSON(w, 200, map[string]any{"state": "idle"})
}

// GET /api/report/docx/download?task=N
func (s *Server) reportDocxDownload(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.URL.Query().Get("task"))
	if t == nil {
		writeErr(w, 404, "no active task")
		return
	}
	tid, _ := strconv.ParseInt(t.ID, 10, 64)
	job := s.reportManager().Status(tid)
	if job == nil || job.State != "done" || job.File == "" {
		writeErr(w, 404, "报告尚未生成")
		return
	}
	f, err := os.Open(job.File)
	if err != nil {
		writeErr(w, 404, "报告文件不存在")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="report-%s.docx"`, t.ID))
	http.ServeContent(w, r, filepath.Base(job.File), job.Finished, f)
}
