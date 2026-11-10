package reportenhance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/evidence"
)

// 报告内的私有标记：Word 生成后在 render.py 里还原成对应样式。
const (
	codeSentinel    = "\uE000" // 代码块段落 -> 等宽字体
	captionSentinel = "\uE001" // 图注段落 -> 居中灰色
	headSentinel    = "\uE002" // 报告内小节标题 -> 加粗
	tableSentinel   = "\uE005" // Markdown 表格 -> 真正的 Word 表格
)

// DocData 是交给 docxtpl 的渲染数据，字段名与模板标签一一对应。
type DocData struct {
	ProjectName string     `json:"project_name"`
	DateCN      string     `json:"date_cn"`
	Total       int        `json:"total"`
	High        int        `json:"high"`
	Medium      int        `json:"medium"`
	Low         int        `json:"low"`
	Risk        string     `json:"risk"`
	Systems     []System   `json:"systems"`
	Scope       []ScopeRow `json:"scope"`
	Groups      []Group    `json:"groups"`
}

type System struct {
	Name  string `json:"name"`
	Vulns []Vuln `json:"vulns"`
}

type Vuln struct {
	Title    string   `json:"title"`
	Location string   `json:"location"`
	Desc     []string `json:"desc"`  // 概述/复现步骤/证据（截图插在证据之后）
	After    []string `json:"after"` // 证据之后的章节（修复建议等）
	Severity string   `json:"severity"`
	Shots    []Shot   `json:"shots"`
}

type Shot struct {
	Path    string `json:"path"`
	Caption string `json:"caption"`
}

type ScopeRow struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Group struct {
	Name     string `json:"name"`
	Count    int    `json:"count"`
	Severity string `json:"severity"`
}

// BuildOptions 汇总一次 Word 生成所需的运行时依赖。
type BuildOptions struct {
	TaskID   int64
	TaskDesc string
	TaskDir  string // 任务工作目录，用于找 PoC 截图
	ShotDir  string // 报文截图落盘目录
	Store    *evidence.Store
	Renderer *Renderer
	Assets   map[int64]*db.Asset
}

const (
	maxBindingsPerFinding = 3
	maxBodyBytes          = 256 << 10
)

// Build 把一批 findings 组装成模板数据，同时按需渲染请求/响应截图。
// 任何一张截图失败都只是少一张图，不影响整体数据。
func Build(ctx context.Context, opts BuildOptions, findings []*db.DBFinding) *DocData {
	items := append([]*db.DBFinding(nil), findings...)
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := severityRank(items[i].Severity), severityRank(items[j].Severity)
		if ri != rj {
			return ri < rj
		}
		return items[i].ID < items[j].ID
	})

	data := &DocData{
		ProjectName: shortName(opts.TaskDesc, 40),
		DateCN:      time.Now().Format("2006年1月2日"),
	}
	if data.ProjectName == "" {
		data.ProjectName = "目标"
	}

	bySystem := map[string][]Vuln{}
	var order []string
	var firstURL string
	groupCount := map[string]int{}
	groupSev := map[string]string{}

	for _, f := range items {
		if f == nil {
			continue
		}
		switch strings.ToLower(f.Severity) {
		case "critical":
			data.Total++
			data.High++
		case "high":
			data.Total++
			data.High++
		case "medium":
			data.Total++
			data.Medium++
		case "low", "info":
			data.Total++
			data.Low++
		default:
			data.Total++
		}

		sys := systemLabel(f, opts.Assets)
		if _, ok := bySystem[sys]; !ok {
			order = append(order, sys)
		}

		v := Vuln{Title: findingTitle(f), Location: location(f), Severity: severityCN(f.Severity)}
		v.Desc, v.After = splitAtEvidence(trimIntro(ParseReport(f.Report)))
		v.Shots = buildShots(ctx, opts, f)
		bySystem[sys] = append(bySystem[sys], v)

		cls := strings.TrimSpace(f.VulnClass)
		if cls == "" {
			cls = findingTitle(f)
		}
		groupCount[cls]++
		if groupSev[cls] == "" || severityRank(f.Severity) < severityRank(groupSev[cls]) {
			groupSev[cls] = f.Severity
		}
		if firstURL == "" {
			firstURL = v.Location
		}
	}

	for _, name := range order {
		data.Systems = append(data.Systems, System{Name: name, Vulns: bySystem[name]})
	}
	classes := make([]string, 0, len(groupCount))
	for c := range groupCount {
		classes = append(classes, c)
	}
	sort.SliceStable(classes, func(i, j int) bool {
		if severityRank(groupSev[classes[i]]) != severityRank(groupSev[classes[j]]) {
			return severityRank(groupSev[classes[i]]) < severityRank(groupSev[classes[j]])
		}
		return classes[i] < classes[j]
	})
	for _, c := range classes {
		data.Groups = append(data.Groups, Group{Name: c, Count: groupCount[c], Severity: severityCN(groupSev[c])})
	}
	data.Scope = []ScopeRow{{Name: data.ProjectName, URL: firstURL}}
	data.Risk = riskState(data)
	return data
}

func buildShots(ctx context.Context, opts BuildOptions, f *db.DBFinding) []Shot {
	var shots []Shot
	if opts.Store != nil {
		n := 0
		for i := range f.TrafficBindings {
			if n >= maxBindingsPerFinding {
				break
			}
			b := f.TrafficBindings[i]
			reqBody, _ := readBody(opts.Store, b.Snapshot, "request")
			respBody, _ := readBody(opts.Store, b.Snapshot, "response")
			if len(reqBody) == 0 && len(respBody) == 0 {
				continue
			}
			hash := ShotHash(respBody)
			out := filepath.Join(opts.ShotDir, filepath.Base(ShotRelPath(f.ID, b.ID, hash)))
			ok := false
			if opts.Renderer != nil {
				ok = opts.Renderer.Shot(ctx, ReqRespInput{
					FindingID: f.ID, BindingID: b.ID,
					VulnClass: f.VulnClass, Severity: f.Severity,
					Method: b.Snapshot.Method, URL: b.Snapshot.URL, Status: b.Snapshot.Status,
					ReqHead: b.Snapshot.ReqHead, ReqBody: reqBody,
					RespHead: b.Snapshot.RespHead, RespBody: respBody,
					RespCT:   b.Snapshot.ContentType,
					Hash:     hash,
					MaxLines: defaultMaxLines,
				}, out)
			}
			if !ok {
				continue
			}
			caption := "请求/响应报文"
			if note := strings.TrimSpace(b.Note); note != "" {
				caption += " · " + truncateRunes(note, 30)
			}
			shots = append(shots, Shot{Path: out, Caption: captionSentinel + caption})
			n++
		}
	}
	for _, img := range CollectPoCImages(opts.TaskDir, f.ID) {
		shots = append(shots, Shot{Path: img.Path, Caption: captionSentinel + PoCCaption(f.Report, img.Step, f)})
	}
	return shots
}

func readBody(st *evidence.Store, snap db.TrafficEvidenceSnapshot, side string) ([]byte, error) {
	f, _, err := st.OpenBody(snap, side)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxBodyBytes))
}

func blocksToParas(bs []Block) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		if b.Kind == "table" {
			if raw, err := json.Marshal(b.Rows); err == nil {
				out = append(out, tableSentinel+base64.StdEncoding.EncodeToString(raw))
			}
			continue
		}
		if strings.TrimSpace(b.Text) == "" {
			continue
		}
		if b.Kind == "heading" {
			out = append(out, headSentinel+b.Text)
			continue
		}
		if b.Kind == "code" {
			for _, line := range strings.Split(b.Text, "\n") {
				out = append(out, codeSentinel+line)
			}
			continue
		}
		out = append(out, b.Text)
	}
	return out
}

// trimIntro 丢掉第一个二级标题之前的内容（报告标题、类型/等级/资产/证据文件
// 这类速览清单），Word 里的「漏洞描述」直接从「概述」开始。
func trimIntro(bs []Block) []Block {
	for i, b := range bs {
		if b.Kind == "heading" && b.Level >= 2 {
			return bs[i:]
		}
	}
	return bs
}

// splitAtEvidence 把正文拆成两段：
//   - desc：只保留「概述 / 复现步骤 / 证据」三个二级章节（截图随后）；
//   - after：「修复建议」的内容，进入模板单独的「漏洞建议」小节。
//
// 其余章节（影响与危害、受影响范围、根因分析、PoC 等）不进入 Word。
func splitAtEvidence(bs []Block) (desc, after []string) {
	var keep, fix []Block
	mode := ""
	hasKeep := false
	for _, b := range bs {
		if b.Kind == "heading" && b.Level >= 2 {
			switch {
			case isFixHeading(b.Text):
				mode = "fix"
			case b.Level == 2 && isKeepHeading(b.Text):
				mode = "keep"
				hasKeep = true
				keep = append(keep, b)
			case b.Level == 2:
				mode = "drop"
			default:
				// 保留章节里的三级小标题跟随当前章节
				if mode == "keep" {
					keep = append(keep, b)
				} else if mode == "fix" {
					fix = append(fix, b)
				}
			}
			continue
		}
		switch mode {
		case "keep":
			keep = append(keep, b)
		case "fix":
			fix = append(fix, b)
		}
	}
	if !hasKeep {
		// 兼容没有标准二级标题的旧报告：仍按「修复建议」切开。
		return splitByFix(bs)
	}
	return blocksToParas(keep), blocksToParas(fix)
}

// isKeepHeading 是 Word「漏洞描述」下允许保留的二级章节。
func isKeepHeading(t string) bool {
	switch strings.TrimSpace(t) {
	case "概述", "复现步骤", "证据":
		return true
	}
	return false
}

// splitByFix 是旧报告的兜底拆分：修复建议之前全部保留。
func splitByFix(bs []Block) (desc, after []string) {
	cut := -1
	for i, b := range bs {
		if b.Kind == "heading" && isFixHeading(b.Text) {
			cut = i
			break
		}
	}
	if cut < 0 {
		return blocksToParas(bs), nil
	}
	// 丢掉「修复建议」标题本身，模板已有「漏洞建议」标题
	return blocksToParas(bs[:cut]), blocksToParas(bs[cut+1:])
}

func isFixHeading(t string) bool {
	return strings.Contains(t, "修复") || strings.Contains(t, "整改") || strings.Contains(t, "加固建议")
}

func findingTitle(f *db.DBFinding) string {
	if s := strings.TrimSpace(f.Name); s != "" {
		return s
	}
	if s := strings.TrimSpace(f.VulnClass); s != "" {
		return s
	}
	return "未命名漏洞"
}

// 兼容 report 包里的导出标题逻辑（未导出，故此处重复实现一份）。
var (
	mdURLRe     = regexp.MustCompile("https?://[^\\s`\"'）)]+")
	cnIndexRe   = regexp.MustCompile(`^[一二三四五六七八九十百]+、\s*`)
	tailParenRe = regexp.MustCompile(`（[^）]{0,12}）$`)
)

func location(f *db.DBFinding) string {
	for i := range f.TrafficBindings {
		if u := strings.TrimSpace(f.TrafficBindings[i].Snapshot.URL); u != "" {
			return u
		}
	}
	if m := mdURLRe.FindString(f.Report); m != "" {
		return m
	}
	return "见详细报告"
}

// systemLabel 决定 4.1 的系统名：优先按流量里的 host 归类，
// 没有绑定流量时退回报告里第一个 URL，再退回资产表。
func systemLabel(f *db.DBFinding, assets map[int64]*db.Asset) string {
	for i := range f.TrafficBindings {
		if h := hostOf(f.TrafficBindings[i].Snapshot.URL); h != "" {
			return h
		}
	}
	if m := mdURLRe.FindString(f.Report); m != "" {
		if h := hostOf(m); h != "" {
			return h
		}
	}
	for _, id := range f.AssetIDs {
		a := assets[id]
		if a == nil {
			continue
		}
		switch {
		case strings.TrimSpace(a.Domain) != "":
			return strings.TrimSpace(a.Domain)
		case strings.TrimSpace(a.IP) != "":
			return strings.TrimSpace(a.IP)
		case strings.TrimSpace(a.AppName) != "":
			return strings.TrimSpace(a.AppName)
		}
	}
	if s := shortName(f.TaskDescription, 40); s != "" {
		return s
	}
	return "目标系统"
}

// hostOf 从 URL 里取主机名（兼容没有 scheme 的写法）。
func hostOf(raw string) string {
	raw = strings.Trim(strings.TrimSpace(raw), "`'\"),;，。")
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// shortName 取任务描述首行并截断，避免把整份范围清单塞进标题。
// 顺手去掉「一、」「（17家）」这类清单式前后缀，封面更干净。
func shortName(s string, n int) string {
	s = strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
	s = cnIndexRe.ReplaceAllString(s, "")
	s = tailParenRe.ReplaceAllString(s, "")
	return truncateRunes(strings.TrimSpace(s), n)
}

func severityRank(s string) int {
	switch strings.ToLower(s) {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}

func severityCN(s string) string {
	switch strings.ToLower(s) {
	case "critical":
		return "严重"
	case "high":
		return "高危"
	case "medium":
		return "中危"
	case "low":
		return "低危"
	default:
		return "信息"
	}
}

func riskState(d *DocData) string {
	switch {
	case d.Total == 0:
		return "良好状态"
	case anyCritical(d):
		return "严重状态"
	case d.High > 0 || d.Medium > 0:
		return "预警状态"
	default:
		return "良好状态"
	}
}

func anyCritical(d *DocData) bool {
	// High 计数把 critical 与 high 合并了；单独判断 groups 里的「严重」。
	for _, g := range d.Groups {
		if g.Severity == "严重" {
			return true
		}
	}
	return false
}
