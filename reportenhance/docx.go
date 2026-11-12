package reportenhance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

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

// Manager 执行异步生成任务，同一任务重复提交时复用正在运行的 job。
type Manager struct {
	mu      sync.Mutex
	jobs    map[int64]*Job
	dir     string
	timeout time.Duration
}

func NewManager(dir string) *Manager {
	return &Manager{jobs: map[int64]*Job{}, dir: dir, timeout: 15 * time.Minute}
}

// Start 启动异步生成；render 产出的 docx 字节由 Manager 负责落盘。
func (m *Manager) Start(taskID int64, render func(ctx context.Context) ([]byte, error)) *Job {
	m.mu.Lock()
	if j := m.jobs[taskID]; j != nil && j.State == "running" {
		cp := *j
		m.mu.Unlock()
		return &cp
	}
	j := &Job{ID: newID(), TaskID: taskID, State: "running", Started: time.Now()}
	m.jobs[taskID] = j
	snapshot := *j
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

		b, err := render(ctx)
		if err != nil {
			m.finish(j, "", err)
			return
		}
		if len(b) == 0 {
			m.finish(j, "", fmt.Errorf("生成 Word 失败: 输出为空"))
			return
		}
		out, err := m.writeReport(taskID, j.ID, b)
		if err != nil {
			m.finish(j, "", err)
			return
		}
		m.finish(j, out, nil)
	}()
	return &snapshot
}

// writeReport 先写临时文件再改名，避免进程中断留下半份 docx。
func (m *Manager) writeReport(taskID int64, jobID string, b []byte) (string, error) {
	dir := filepath.Join(m.dir, strconv.FormatInt(taskID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".report-*.docx")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	out := filepath.Join(dir, jobID+".docx")
	if err := os.Rename(tmpName, out); err != nil {
		return "", err
	}
	return out, nil
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
	matches, err := filepath.Glob(filepath.Join(m.dir, strconv.FormatInt(taskID, 10), "*.docx"))
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

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b[:])
}
