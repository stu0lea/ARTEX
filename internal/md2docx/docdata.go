package md2docx

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 报告内的私有标记，与 reportenhance 保持一致：渲染时还原成对应样式。
const (
	codeSentinel    = "\uE000" // 代码块段落 -> 等宽字体
	captionSentinel = "\uE001" // 图注段落 -> 居中灰色
	headSentinel    = "\uE002" // 报告内小节标题 -> 加粗
	inlineCodeStart = "\uE003"
	inlineCodeEnd   = "\uE004"
	tableSentinel   = "\uE005" // Markdown 表格 -> 真正的 Word 表格
)

// DocData 与 Word 模板里的标签一一对应，字段名沿用 reportenhance 的 JSON 命名。
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
	Desc     []string `json:"desc"`
	After    []string `json:"after"`
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

// ConvertOptions 是命令行显式覆盖的字段，留空时从 Markdown 元数据推断。
type ConvertOptions struct {
	Project  string
	Title    string
	Location string
	Severity string
	Date     string
}

var (
	mdURLRe       = regexp.MustCompile(`https?://[^\s` + "`" + `"'）)]+`)
	metaLineRe    = regexp.MustCompile(`^\s*[-*]\s*\*\*(.+?)\*\*\s*[：:]\s*(.*)$`)
	titleSevRe    = regexp.MustCompile(`^\[\s*([A-Za-z\p{Han}]+)\s*\]\s*`)
	detailHeadRe  = regexp.MustCompile(`^#{2,6}\s*详细报告\s*$`)
	datePrefixRe  = regexp.MustCompile(`(\d{4})[-/年](\d{1,2})[-/月](\d{1,2})`)
	cnIndexRe     = regexp.MustCompile(`^[一二三四五六七八九十百]+、\s*`)
	tailParenRe   = regexp.MustCompile(`（[^）]{0,12}）$`)
	mdInlineImgRe = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdHTMLTagRe   = regexp.MustCompile(`<[^>]{1,80}>`)
)

// FromMarkdown 把一份导出的漏洞 Markdown 转成模板数据。
// 支持两种输入：
//   - 单个漏洞报告（含「详细报告」章节）
//   - 漏洞发现汇总报告（含「漏洞明细」以及多个 ### N. [SEV] 章节）
//
// baseDir 用于解析 Markdown 里相对路径的截图。
func FromMarkdown(md, baseDir string, opts ConvertOptions) *DocData {
	md = strings.TrimPrefix(md, "\ufeff")
	if summary, ok := parseSummaryReport(md); ok {
		return fromSummaryReport(summary, baseDir, opts)
	}
	return fromSingleReport(md, baseDir, opts)
}

func fromSingleReport(md, baseDir string, opts ConvertOptions) *DocData {
	metaText, report := splitDetailReport(md)
	meta := parseMeta(metaText)

	title, sev := parseTitle(metaText)
	reportTitle, reportSev := parseTitle(report)
	if title == "" {
		title = reportTitle
	}
	if sev == "" {
		sev = reportSev
	}
	if sev == "" {
		sev = meta["严重等级"]
	}
	if opts.Title != "" {
		title = opts.Title
	}
	if title == "" {
		title = "未命名漏洞"
	}
	if opts.Severity != "" {
		sev = opts.Severity
	}
	sev = normalizeSeverity(sev)

	project := strings.TrimSpace(opts.Project)
	if project == "" {
		project = projectLabel(metaText, meta["所属任务"], title, report)
	}
	if project == "" {
		project = "目标"
	}

	location := strings.TrimSpace(opts.Location)
	if location == "" {
		location = firstURL(report)
	}
	if location == "" {
		location = "见详细报告"
	}

	date := strings.TrimSpace(opts.Date)
	if date == "" {
		date = cnDate(meta["生成时间"])
	}
	if date == "" {
		date = cnDate(meta["发现时间"])
	}
	if date == "" {
		date = time.Now().Format("2006年1月2日")
	}

	blocks := trimIntro(parseMarkdown(report))
	desc, after, shots := splitBlocks(blocks, baseDir)
	if len(desc) == 0 && len(after) == 0 {
		var dummy []Shot
		desc = blocksToParas(blocks, baseDir, &dummy)
		shots = append(shots, dummy...)
	}

	cls := strings.TrimSpace(meta["类别"])
	if cls == "" {
		cls = title
	}
	sysName := systemLabel(location, shortName(meta["所属任务"], 40))

	data := &DocData{
		ProjectName: project,
		DateCN:      date,
		Total:       1,
	}
	switch sev {
	case "critical", "high":
		data.High = 1
	case "medium":
		data.Medium = 1
	case "low", "info":
		data.Low = 1
	}
	data.Systems = []System{{
		Name: sysName,
		Vulns: []Vuln{{
			Title:    title,
			Location: location,
			Desc:     desc,
			After:    after,
			Severity: severityCN(sev),
			Shots:    shots,
		}},
	}}
	scopeURL := location
	data.Scope = []ScopeRow{{Name: project, URL: scopeURL}}
	data.Groups = []Group{{Name: cls, Count: 1, Severity: severityCN(sev)}}
	data.Risk = riskState(data)
	return data
}

// splitDetailReport 拆出元数据区与「详细报告」正文。
func splitDetailReport(md string) (metaText, report string) {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if detailHeadRe.MatchString(strings.TrimSpace(line)) {
			return strings.Join(lines[:i], "\n"), strings.Join(lines[i+1:], "\n")
		}
	}
	return "", md
}

func parseMeta(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if m := metaLineRe.FindStringSubmatch(line); m != nil {
			out[strings.TrimSpace(m[1])] = strings.TrimSpace(m[2])
		}
	}
	return out
}

// parseTitle 取第一个一级标题，并顺带解析 `[HIGH]` 形式的等级前缀。
func parseTitle(text string) (title, severity string) {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if m := titleSevRe.FindStringSubmatch(heading); m != nil {
			severity = m[1]
			heading = strings.TrimSpace(heading[len(m[0]):])
		}
		return strings.TrimSpace(plainText(heading)), severity
	}
	return "", ""
}

func normalizeSeverity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "critical", "严重":
		return "critical"
	case "high", "高危", "高":
		return "high"
	case "medium", "中危", "中":
		return "medium"
	case "low", "低危", "低":
		return "low"
	default:
		return "info"
	}
}

func severityCN(s string) string {
	switch normalizeSeverity(s) {
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
	case hasCritical(d):
		return "严重状态"
	case d.High > 0 || d.Medium > 0:
		return "预警状态"
	default:
		return "良好状态"
	}
}

func hasCritical(d *DocData) bool {
	for _, g := range d.Groups {
		if g.Severity == "严重" {
			return true
		}
	}
	return false
}

func cnDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	m := datePrefixRe.FindStringSubmatch(raw)
	if m == nil {
		return ""
	}
	var y, mo, d int
	fmt.Sscanf(m[1], "%d", &y)
	fmt.Sscanf(m[2], "%d", &mo)
	fmt.Sscanf(m[3], "%d", &d)
	return fmt.Sprintf("%d年%d月%d日", y, mo, d)
}

func firstURL(s string) string {
	loc := mdURLRe.FindString(s)
	return strings.TrimRight(loc, ".,;:，。；：、")
}

func shortName(s string, n int) string {
	s = strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
	s = cnIndexRe.ReplaceAllString(s, "")
	s = tailParenRe.ReplaceAllString(s, "")
	return truncateRunes(strings.TrimSpace(s), n)
}

// projectLabel 从「所属任务」里选一个最相关的地址作为报告项目名。
// 单漏洞报告的所属任务经常是一长串资产 URL，不能直接拿第一行；
// 优先选在标题/正文里出现次数最多的主机，并去掉协议和端口。
func projectLabel(metaText, raw, title, report string) string {
	raw = strings.TrimSpace(raw)
	urls := collectTaskURLs(metaText)
	if len(urls) == 0 {
		urls = mdURLRe.FindAllString(raw, -1)
	}
	if len(urls) == 0 {
		return shortName(raw, 40)
	}
	if best := chooseBestURL(urls, title, report); best != "" {
		if h := hostOf(best); h != "" {
			return h
		}
	}
	return shortName(raw, 40)
}

// collectTaskURLs 收集「所属任务」条目下紧跟的多行 URL。
func collectTaskURLs(text string) []string {
	var urls []string
	collecting := false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if strings.Contains(t, "**所属任务**") {
			collecting = true
			urls = append(urls, mdURLRe.FindAllString(t, -1)...)
			continue
		}
		if !collecting {
			continue
		}
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "- **") ||
			strings.HasPrefix(t, "* **") || strings.HasPrefix(t, "**") {
			break
		}
		if strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") {
			urls = append(urls, mdURLRe.FindAllString(t, -1)...)
			continue
		}
		break
	}
	return urls
}

func chooseBestURL(urls []string, title, report string) string {
	context := title + "\n" + report
	best := ""
	bestScore := -1
	bestLen := 0
	for _, raw := range urls {
		raw = strings.TrimRight(strings.TrimSpace(raw), ".,;:，。；：、")
		h := hostOf(raw)
		if h == "" {
			continue
		}
		// 必须按完整主机名匹配：icsc.mall.icbc.com.cn 不能算作 icbc.com.cn 的一次出现。
		score := hostCount(title, h)*100 + hostCount(context, h)
		if strings.HasPrefix(strings.ToLower(raw), "https://") {
			score++
		}
		if score > bestScore || (score == bestScore && len(h) > bestLen) {
			best, bestScore, bestLen = raw, score, len(h)
		}
	}
	return best
}

func hostCount(text, host string) int {
	text = strings.ToLower(text)
	host = strings.ToLower(host)
	if host == "" {
		return 0
	}
	count := 0
	for start := 0; start < len(text); {
		i := strings.Index(text[start:], host)
		if i < 0 {
			break
		}
		i += start
		after := i + len(host)
		beforeOK := i == 0 || !isHostChar(text[i-1])
		afterOK := after >= len(text) || !isHostChar(text[after])
		if beforeOK && afterOK {
			count++
		}
		start = i + 1
	}
	return count
}

func isHostChar(b byte) bool {
	return b == '.' || b == '-' || b == '_' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func systemLabel(location, project string) string {
	if h := hostOf(location); h != "" {
		return h
	}
	if project != "" {
		return project
	}
	return "目标系统"
}

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

// trimIntro 丢掉第一个二级标题之前的内容（标题、类型/等级/资产等速览清单）。
func trimIntro(bs []block) []block {
	for i, b := range bs {
		if b.kind == blockHeading && b.level >= 2 {
			return bs[i:]
		}
	}
	return bs
}

// splitBlocks 把正文拆成「漏洞描述」与「修复建议」，并收集正文里的截图。
func splitBlocks(bs []block, baseDir string) (desc, after []string, shots []Shot) {
	var keep, fix []block
	mode := ""
	hasKeep := false
	for _, b := range bs {
		if b.kind == blockHeading && b.level >= 2 {
			switch {
			case isFixHeading(b.text):
				mode = "fix"
			case b.level == 2 && isKeepHeading(b.text):
				mode = "keep"
				hasKeep = true
				keep = append(keep, b)
			case b.level == 2:
				mode = "drop"
			default:
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
		return splitByFix(bs, baseDir)
	}
	desc = blocksToParas(keep, baseDir, &shots)
	after = blocksToParas(fix, baseDir, nil)
	return desc, after, shots
}

func isKeepHeading(t string) bool {
	switch strings.TrimSpace(t) {
	case "概述", "复现步骤", "证据":
		return true
	}
	return false
}

func isFixHeading(t string) bool {
	return strings.Contains(t, "修复") || strings.Contains(t, "整改") || strings.Contains(t, "加固建议")
}

func splitByFix(bs []block, baseDir string) (desc, after []string, shots []Shot) {
	cut := -1
	for i, b := range bs {
		if b.kind == blockHeading && isFixHeading(b.text) {
			cut = i
			break
		}
	}
	if cut < 0 {
		desc = blocksToParas(bs, baseDir, &shots)
		return desc, nil, shots
	}
	desc = blocksToParas(bs[:cut], baseDir, &shots)
	after = blocksToParas(bs[cut+1:], baseDir, nil)
	return desc, after, shots
}

// blocksToParas 把排版块转成模板里的段落列表，使用与 reportenhance 相同的私有标记。
func blocksToParas(bs []block, baseDir string, shots *[]Shot) []string {
	const maxParas = 200
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		switch b.kind {
		case blockImage:
			if shots != nil {
				if shot, ok := imageShot(b, baseDir); ok {
					*shots = append(*shots, shot)
				}
			}
			continue
		case blockTable:
			rows := make([][]string, 0, len(b.rows))
			for _, row := range b.rows {
				cells := make([]string, 0, len(row))
				for _, cell := range row {
					cells = append(cells, runsText(parseInline(cell)))
				}
				rows = append(rows, cells)
			}
			if raw, err := json.Marshal(rows); err == nil {
				out = append(out, tableSentinel+base64.StdEncoding.EncodeToString(raw))
			}
			continue
		case blockRule:
			continue
		}
		text := strings.TrimSpace(blockText(b))
		if text == "" {
			continue
		}
		switch b.kind {
		case blockHeading:
			out = append(out, headSentinel+text)
		case blockCode:
			for _, line := range strings.Split(b.text, "\n") {
				out = append(out, codeSentinel+line)
			}
		default:
			out = append(out, text)
		}
		if len(out) >= maxParas {
			break
		}
	}
	return out
}

func blockText(b block) string {
	text := runsText(parseInline(b.text))
	if b.kind == blockList {
		// 与 reportenhance 一致：无序列表统一成圆点，有序列表保留编号。
		if b.ordered {
			return strings.TrimSpace(b.marker) + " " + text
		}
		return "• " + text
	}
	return text
}

// runsText 把行内样式压平：保留行内代码标记，其余标记只留文字。
func runsText(rs []run) string {
	var b strings.Builder
	for _, r := range rs {
		text := r.text
		if text == "" {
			continue
		}
		if r.style.code {
			b.WriteString(inlineCodeStart)
			b.WriteString(text)
			b.WriteString(inlineCodeEnd)
			continue
		}
		b.WriteString(text)
	}
	return strings.TrimSpace(mdHTMLTagRe.ReplaceAllString(b.String(), ""))
}

// imageShot 把 Markdown 图片块转成截图；只接受本地存在的文件。
func imageShot(b block, baseDir string) (Shot, bool) {
	raw := strings.TrimSpace(b.image)
	if raw == "" {
		return Shot{}, false
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return Shot{}, false
	}
	if unescaped, err := url.PathUnescape(raw); err == nil {
		raw = unescaped
	}
	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, filepath.FromSlash(path))
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return Shot{}, false
	}
	caption := strings.TrimSpace(b.alt)
	if caption == "" {
		caption = "PoC 验证"
	}
	caption = mdInlineImgRe.ReplaceAllString(caption, "")
	return Shot{Path: path, Caption: captionSentinel + caption}, true
}
