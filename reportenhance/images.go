package reportenhance

import (
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PoCImage 是一张按命名约定收集到的 PoC 截图。
type PoCImage struct {
	Path string
	Step int
}

const (
	pocMaxFiles  = 6
	pocMaxBytes  = 8 << 20
	pocMinWidth  = 200
	pocMinHeight = 150
)

// CollectPoCImages 在任务工作目录里找 f<findingID>[-s<step>]-poc-<n>.(png|jpg|jpeg)。
// 任何不合格的文件都静默跳过：截图是可选交付物，不能影响报告生成。
func CollectPoCImages(taskDir string, findingID int64) []PoCImage {
	if taskDir == "" || findingID <= 0 {
		return nil
	}
	re := regexp.MustCompile(fmt.Sprintf(`^f%d(?:-s(\d{1,2}))?[-_]poc[-_]?\d*\.(?:png|jpe?g)$`, findingID))
	var out []PoCImage
	_ = filepath.WalkDir(taskDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		m := re.FindStringSubmatch(name)
		if len(m) == 0 {
			return nil
		}
		step := 0
		if len(m) > 1 && m[1] != "" {
			step, _ = strconv.Atoi(m[1])
		}
		if !usableImage(path) {
			return nil
		}
		out = append(out, PoCImage{Path: path, Step: step})
		if len(out) >= pocMaxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Step != out[j].Step {
			return out[i].Step < out[j].Step
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func usableImage(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() <= 0 || fi.Size() > pocMaxBytes {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return false
	}
	return cfg.Width >= pocMinWidth && cfg.Height >= pocMinHeight
}
