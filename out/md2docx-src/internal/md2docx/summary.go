package md2docx

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	summaryMarkerRe  = regexp.MustCompile(`^##\s*漏洞明细\s*$`)
	summaryFindingRe = regexp.MustCompile(`^###\s*\d+[.、]\s*\[([^\]]+)\]\s*(.+?)\s*$`)
	summaryDetailRe  = regexp.MustCompile(`^\*\*详细报告[：:]\*\*\s*$`)
	bareHostRe       = regexp.MustCompile(`(?i)\b((?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}(?::\d{1,5})?)\b`)
)

type summaryReport struct {
	preamble string
	findings []summaryFinding
}

type summaryFinding struct {
	title    string
	severity string
	body     string
}

type parsedSummaryFinding struct {
	vuln     Vuln
	category string
	meta     map[string]string
}

// parseSummaryReport 识别「漏洞发现汇总报告」并拆出每个 ### N. [SEV] 章节。
// 代码块里的 ### 不会误判为漏洞标题。
func parseSummaryReport(md string) (*summaryReport, bool) {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	marker := -1
	inFence := false
	fence := ""
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if f, ok := fenceMarker(t); ok {
			if !inFence {
				inFence, fence = true, f
			} else if strings.HasPrefix(t, fence) {
				inFence = false
			}
			continue
		}
		if inFence {
			continue
		}
		if summaryMarkerRe.MatchString(t) {
			marker = i
			break
		}
	}
	if marker < 0 {
		return nil, false
	}

	var findings []summaryFinding
	var cur *summaryFinding
	var body []string
	inFence = false
	fence = ""

	flush := func() {
		if cur == nil {
			return
		}
		cur.body = strings.Join(body, "\n")
		findings = append(findings, *cur)
		cur = nil
		body = nil
	}

	for i := marker + 1; i < len(lines); i++ {
		line := lines[i]
		t := strings.TrimSpace(line)
		if f, ok := fenceMarker(t); ok {
			if cur != nil {
				body = append(body, line)
			}
			if !inFence {
				inFence, fence = true, f
			} else if strings.HasPrefix(t, fence) {
				inFence = false
			}
			continue
		}
		if !inFence {
			if m := summaryFindingRe.FindStringSubmatch(t); m != nil {
				flush()
				cur = &summaryFinding{
					title:    strings.TrimSpace(m[2]),
					severity: strings.TrimSpace(m[1]),
				}
				continue
			}
		}
		if cur != nil {
			body = append(body, line)
		}
	}
	flush()

	if len(findings) == 0 {
		return nil, false
	}
	return &summaryReport{
		preamble: strings.Join(lines[:marker], "\n"),
		findings: findings,
	}, true
}

func splitFindingDetail(body string) (metaText, detail string) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if summaryDetailRe.MatchString(strings.TrimSpace(line)) {
			return strings.Join(lines[:i], "\n"), strings.Join(lines[i+1:], "\n")
		}
	}
	return body, ""
}

func fromSummaryReport(sr *summaryReport, baseDir string, opts ConvertOptions) *DocData {
	data := &DocData{}

	project := strings.TrimSpace(opts.Project)
	if project == "" {
		project = summaryProject(sr)
	}
	if project == "" {
		project = "目标"
	}
	data.ProjectName = project

	preambleMeta := parseMeta(sr.preamble)
	date := strings.TrimSpace(opts.Date)
	if date == "" {
		date = cnDate(preambleMeta["生成时间"])
	}

	bySystem := map[string][]Vuln{}
	var order []string
	groupCount := map[string]int{}
	groupSev := map[string]string{}
	firstURL := ""

	for _, sf := range sr.findings {
		pf := parseSummaryFinding(sf, baseDir)
		v := pf.vuln
		sev := normalizeSeverity(sf.severity)
		if sev == "info" {
			sev = normalizeSeverity(v.Severity)
		}

		data.Total++
		switch sev {
		case "critical", "high":
			data.High++
		case "medium":
			data.Medium++
		case "low":
			data.Low++
		default:
			data.Low++
		}

		sys := systemLabel(v.Location, project)
		if _, ok := bySystem[sys]; !ok {
			order = append(order, sys)
		}
		bySystem[sys] = append(bySystem[sys], v)

		cls := pf.category
		groupCount[cls]++
		if old := groupSev[cls]; old == "" || severityRank(sev) < severityRank(old) {
			groupSev[cls] = sev
		}

		if firstURL == "" && v.Location != "" && v.Location != "见详细报告" {
			firstURL = v.Location
		}
		if date == "" {
			date = cnDate(pf.meta["发现时间"])
		}
	}

	if date == "" {
		date = time.Now().Format("2006年1月2日")
	}
	data.DateCN = date

	for _, name := range order {
		data.Systems = append(data.Systems, System{Name: name, Vulns: bySystem[name]})
	}

	classes := make([]string, 0, len(groupCount))
	for c := range groupCount {
		classes = append(classes, c)
	}
	sort.SliceStable(classes, func(i, j int) bool {
		ri, rj := severityRank(groupSev[classes[i]]), severityRank(groupSev[classes[j]])
		if ri != rj {
			return ri < rj
		}
		return classes[i] < classes[j]
	})
	for _, c := range classes {
		data.Groups = append(data.Groups, Group{
			Name:     c,
			Count:    groupCount[c],
			Severity: severityCN(groupSev[c]),
		})
	}

	if firstURL == "" {
		firstURL = "见详细报告"
	}
	data.Scope = []ScopeRow{{Name: project, URL: firstURL}}
	data.Risk = riskState(data)
	return data
}

func parseSummaryFinding(sf summaryFinding, baseDir string) parsedSummaryFinding {
	metaText, detail := splitFindingDetail(sf.body)
	if strings.TrimSpace(detail) == "" {
		detail = sf.body
	}
	meta := parseMeta(metaText)

	title := strings.TrimSpace(sf.title)
	if title == "" {
		title, _ = parseTitle(detail)
	}
	if title == "" {
		title = strings.TrimSpace(meta["类别"])
	}
	if title == "" {
		title = "未命名漏洞"
	}

	location := summaryLocation(meta, metaText, detail)
	blocks := trimIntro(parseMarkdown(detail))
	desc, after, shots := splitBlocks(blocks, baseDir)
	if len(desc) == 0 && len(after) == 0 {
		var dummy []Shot
		desc = blocksToParas(blocks, baseDir, &dummy)
		shots = append(shots, dummy...)
	}

	category := strings.TrimSpace(meta["类别"])
	if category == "" {
		category = title
	}
	return parsedSummaryFinding{
		vuln: Vuln{
			Title:    title,
			Location: location,
			Desc:     desc,
			After:    after,
			Severity: severityCN(sf.severity),
			Shots:    shots,
		},
		category: category,
		meta:     meta,
	}
}

// summaryLocation 优先取「受影响范围」里的资产/主机，再退回正文第一个 URL。
// 主机行经常只写裸域名（如 `filemover.example.com:24006`），也要能识别。
func summaryLocation(meta map[string]string, metaText, detail string) string {
	for _, line := range strings.Split(detail, "\n") {
		t := strings.TrimSpace(strings.TrimLeft(line, "-* \t"))
		for _, prefix := range []string{
			"受影响主机：", "受影响主机:", "资产：", "资产:", "主机：", "主机:",
		} {
			if strings.HasPrefix(t, prefix) {
				// 只看标签后紧跟的第一个值，避免把「主机：外部域名（后端 http://内网）」
				// 里的内网地址当成漏洞位置。
				rest := strings.TrimSpace(strings.TrimPrefix(t, prefix))
				rest = strings.TrimLeft(rest, "`* ")
				if i := strings.IndexAny(rest, "`（(，,；; \t"); i >= 0 {
					rest = rest[:i]
				}
				if u := firstURL(rest); u != "" {
					return u
				}
				if hp := bareHostRe.FindString(rest); hp != "" {
					return "https://" + hp
				}
			}
		}
	}

	if u := firstURLInSection(detail, "受影响范围"); u != "" {
		return u
	}
	if u := firstURLInSection(detail, "概述"); u != "" {
		return u
	}
	for _, key := range []string{"受影响主机", "主机", "资产", "URL", "地址"} {
		if v := strings.TrimSpace(meta[key]); v != "" {
			if u := firstURL(v); u != "" {
				return u
			}
		}
	}
	if u := firstURL(detail); u != "" {
		return u
	}
	if u := firstURL(metaText); u != "" {
		return u
	}
	return "见详细报告"
}

func firstURLInSection(text, name string) string {
	in := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if m := headingRe.FindStringSubmatch(t); m != nil {
			if len(m[1]) <= 2 {
				in = len(m[1]) == 2 && strings.Contains(m[2], name)
				continue
			}
		}
		if in {
			if u := firstURL(t); u != "" {
				return u
			}
		}
	}
	return ""
}

func summaryProject(sr *summaryReport) string {
	meta := parseMeta(sr.preamble)
	for _, key := range []string{"项目名称", "所属任务"} {
		if p := projectLabel(sr.preamble, meta[key], "漏洞发现汇总报告", sr.preamble); p != "" {
			return p
		}
	}
	for _, sf := range sr.findings {
		metaText, _ := splitFindingDetail(sf.body)
		m := parseMeta(metaText)
		if p := projectLabel(metaText, m["所属任务"], sf.title, sf.body); p != "" {
			return p
		}
	}
	return ""
}

func severityRank(s string) int {
	switch normalizeSeverity(s) {
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
