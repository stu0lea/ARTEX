package reportenhance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// ShotTimeout 是单张截图的渲染上限；超时按失败处理。
	ShotTimeout = 20 * time.Second
	// ShotViewport 与 HTML 模板的 1600px 画布保持一致。
	ShotViewport = "1600,600" // 视口高度只作为下限；--full-page 会按内容撑高，避免底部留白
	pngMagic     = "\x89PNG\r\n\x1a\n"
)

// Renderer 调用镜像内的 playwright CLI 把 HTML 渲染成 PNG。
// Chromium 吃内存，所以并发固定为 1（compose 里已配 shm_size: 1gb）。
type Renderer struct {
	Bin string // 默认取 PLAYWRIGHT_BIN，否则 "playwright"
	Dir string // 临时 HTML 落盘目录；空则用系统临时目录

	once sync.Once
	sem  chan struct{}
}

// NewRenderer 创建渲染器。dir 用于放临时 HTML 文件。
func NewRenderer(dir string) *Renderer {
	return &Renderer{Bin: os.Getenv("PLAYWRIGHT_BIN"), Dir: dir}
}

func (r *Renderer) init() {
	r.once.Do(func() {
		r.sem = make(chan struct{}, 1)
		if r.Bin == "" {
			r.Bin = "playwright"
		}
	})
}

// Available 报告 playwright 二进制是否可用；不可用时应直接跳过截图。
func (r *Renderer) Available() bool {
	r.init()
	if _, err := exec.LookPath(r.Bin); err != nil {
		return false
	}
	return true
}

// Shot 把一次请求/响应渲染成 PNG 写到 outPNG。
//
// 约定：截图属于可选交付物，任何失败（playwright 缺失、超时、输出非法）
// 都只记录日志并返回 false，不返回 error —— 报告生成不能因为一张图而中断。
func (r *Renderer) Shot(ctx context.Context, in ReqRespInput, outPNG string) (ok bool) {
	r.init()

	if fi, err := os.Stat(outPNG); err == nil && fi.Size() > 0 {
		return true // 命中缓存
	}
	if !r.Available() {
		log.Printf("[reportenhance] 跳过报文截图：未找到 %q", r.Bin)
		return false
	}
	if err := os.MkdirAll(filepath.Dir(outPNG), 0o755); err != nil {
		log.Printf("[reportenhance] 跳过报文截图：创建目录失败: %v", err)
		return false
	}

	htmlBytes, err := RenderReqRespHTML(in)
	if err != nil {
		log.Printf("[reportenhance] 跳过报文截图：渲染 HTML 失败: %v", err)
		return false
	}

	tmpDir := r.Dir
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}
	tmp := filepath.Join(tmpDir, fmt.Sprintf("rr-%d-%d-%d.html", in.FindingID, in.BindingID, time.Now().UnixNano()))
	if err = os.WriteFile(tmp, htmlBytes, 0o600); err != nil {
		log.Printf("[reportenhance] 跳过报文截图：写入临时文件失败: %v", err)
		return false
	}
	defer os.Remove(tmp)

	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return false
	}

	runCtx, cancel := context.WithTimeout(ctx, ShotTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, r.Bin,
		"screenshot", "--full-page",
		"--viewport-size="+ShotViewport,
		"file://"+tmp, outPNG)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		log.Printf("[reportenhance] 跳过报文截图：playwright 失败: %v %s", err, strings.TrimSpace(stderr.String()))
		return false
	}
	if !isPNG(outPNG) {
		log.Printf("[reportenhance] 跳过报文截图：输出不是合法 PNG (%s)", outPNG)
		_ = os.Remove(outPNG)
		return false
	}
	return true
}

func isPNG(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	hdr := make([]byte, len(pngMagic))
	if _, err := f.Read(hdr); err != nil {
		return false
	}
	return string(hdr) == pngMagic
}

// ShotHash 取报文内容的短哈希，用于截图文件名（证据变更即自动换新图）。
func ShotHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])[:8]
}

// ShotRelPath 返回截图相对任务工作区的路径。
func ShotRelPath(findingID, bindingID int64, hash string) string {
	return fmt.Sprintf("shots/f%d-e%d-%s.png", findingID, bindingID, hash)
}
