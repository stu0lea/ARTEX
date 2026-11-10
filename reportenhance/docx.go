package reportenhance

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed assets/render.py assets/report-template-fallback.docx
var assets embed.FS

// Job 是一次 Word 生成任务的状态快照。
type Job struct {
	ID       string    `json:"id"`
	TaskID   int64     `json:"task_id"`
	State    string    `json:"state"` // running | done | failed
	Error    string    `json:"error,omitempty"`
	File     string    `json:"-"`
	Started  time.Time `json:"started_at"`
	Finished time.Time `json:"finished_at,omitempty"`
}

// Manager 串行执行同任务的生成任务，重复点击直接复用正在跑的 job。
// 截图与 Python 渲染都很慢，HTTP handler 必须立即返回，否则会撞网关超时。
type Manager struct {
	mu       sync.Mutex
	jobs     map[int64]*Job
	dir      string
	renderer *Renderer
	timeout  time.Duration
}

func NewManager(dir string, r *Renderer) *Manager {
	return &Manager{jobs: map[int64]*Job{}, dir: dir, renderer: r, timeout: 15 * time.Minute}
}

// Start 启动异步生成；build 负责截图与数据组装，失败只影响这份报告。
func (m *Manager) Start(taskID int64, build func(ctx context.Context) (*DocData, error)) *Job {
	m.mu.Lock()
	if j := m.jobs[taskID]; j != nil && j.State == "running" {
		m.mu.Unlock()
		return j
	}
	j := &Job{ID: newID(), TaskID: taskID, State: "running", Started: time.Now()}
	m.jobs[taskID] = j
	m.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[reportenhance] 生成 Word 异常: %v", r)
				m.finish(j, "", fmt.Errorf("internal error: %v", r))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
		defer cancel()

		data, err := build(ctx)
		if err != nil {
			m.finish(j, "", err)
			return
		}
		out := filepath.Join(m.dir, fmt.Sprintf("%d", taskID), j.ID+".docx")
		if err = m.Render(ctx, data, out); err != nil {
			m.finish(j, "", err)
			return
		}
		m.finish(j, out, nil)
	}()
	return j
}

func (m *Manager) finish(j *Job, file string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.jobs[j.TaskID]
	if cur != nil {
		j = cur
	}
	j.Finished = time.Now()
	if err != nil {
		j.State = "failed"
		j.Error = err.Error()
	} else {
		j.State = "done"
		j.File = file
	}
}

// Status 返回该任务最近一次生成状态。进程重启后内存记录会丢失，
// 此时回退到磁盘上最新的 docx，保证页面仍能直接下载已生成的报告。
func (m *Manager) Status(taskID int64) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[taskID]; j != nil {
		cp := *j
		return &cp
	}
	if j, ok := m.latestReport(taskID); ok {
		return j
	}
	return nil
}

// latestReport 查找 data/reports/<task>/ 下修改时间最新的 docx。
func (m *Manager) latestReport(taskID int64) (*Job, bool) {
	matches, err := filepath.Glob(filepath.Join(m.dir, fmt.Sprintf("%d", taskID), "*.docx"))
	if err != nil || len(matches) == 0 {
		return nil, false
	}
	var best string
	var bestMod time.Time
	for _, path := range matches {
		fi, err := os.Stat(path)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if best == "" || fi.ModTime().After(bestMod) {
			best, bestMod = path, fi.ModTime()
		}
	}
	if best == "" {
		return nil, false
	}
	id := strings.TrimSuffix(filepath.Base(best), filepath.Ext(best))
	return &Job{ID: id, TaskID: taskID, State: "done", File: best, Started: bestMod, Finished: bestMod}, true
}

// templateBytes 返回本次渲染使用的模板：优先 ARTEX_REPORT_TEMPLATE 指向的
// 运行时模板（容器里挂载仓库中的 docx，改完下次生成即生效），否则回退到
// 编译进二进制的内置模板。
func (m *Manager) templateBytes() ([]byte, string, error) {
	if p := strings.TrimSpace(os.Getenv("ARTEX_REPORT_TEMPLATE")); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			return b, p, nil
		} else {
			log.Printf("[reportenhance] 读取运行时模板 %s 失败，回退内置模板: %v", p, err)
		}
	}
	b, err := assets.ReadFile("assets/report-template-fallback.docx")
	return b, "embedded", err
}

// Render 调用镜像内的 Python + docxtpl 把数据套进客户模板。
func (m *Manager) Render(ctx context.Context, data *DocData, out string) error {
	python := os.Getenv("PYTHON_BIN")
	if python == "" {
		python = "python3"
	}
	if _, err := exec.LookPath(python); err != nil {
		return errors.New("镜像内未安装 python3，无法生成 Word")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(m.dir, "stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)

	py, err := assets.ReadFile("assets/render.py")
	if err != nil {
		return err
	}
	tpl, tplPath, err := m.templateBytes()
	if err != nil {
		return err
	}
	if tplPath != "embedded" {
		log.Printf("[reportenhance] 使用运行时 Word 模板: %s", tplPath)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	files := map[string][]byte{
		"render.py":     py,
		"template.docx": tpl,
		"data.json":     raw,
	}
	for name, b := range files {
		if err = os.WriteFile(filepath.Join(stage, name), b, 0o600); err != nil {
			return err
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, python,
		filepath.Join(stage, "render.py"),
		"--template", filepath.Join(stage, "template.docx"),
		"--data", filepath.Join(stage, "data.json"),
		"--out", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Word 渲染失败: %v: %s", err, truncateRunes(string(msg), 600))
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		return errors.New("Word 渲染失败: 未生成输出文件")
	}
	return nil
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
