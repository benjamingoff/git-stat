package main

import (
	"fmt"
	"image/color"
	"math"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/benjamingoff/git-stat/internal/parser"
)

var palette = []color.Color{
	lipgloss.Color("39"), lipgloss.Color("205"), lipgloss.Color("41"), lipgloss.Color("213"),
	lipgloss.Color("51"), lipgloss.Color("171"), lipgloss.Color("208"), lipgloss.Color("114"),
	lipgloss.Color("196"), lipgloss.Color("45"), lipgloss.Color("87"),
}

type filesLoadedMsg struct {
	days []parser.DayStats
	err  error
}

type ownerTick struct {
	idx   int // position in the chronological day list
	stats parser.UserDayStats
}

type ownersDoneMsg struct{ err error }

// ownerRun carries the in-flight background owner computation between Cmds;
// the channel close is the only synchronization (err is read only after it).
type ownerRun struct {
	ch  chan ownerTick
	err error
}

const (
	viewLangs      = iota // stacked bar chart: LOC per language over time
	viewOwners            // stacked bar chart: LOC per contributor over time
	viewLangTable         // table: LOC per language on the selected day
	viewOwnerTable        // table: LOC per contributor on the selected day
)

func viewName(v int) string {
	switch v {
	case viewLangs:
		return "by language"
	case viewOwners:
		return "by contributor"
	case viewLangTable:
		return "language table"
	default:
		return "contributor table"
	}
}

type chartModel struct {
	repo   string
	filter *parser.Filter

	fileDays   []parser.DayStats            // fast single-pass stats; ready first
	times      []time.Time                  // parsed fileDays dates, parallel to fileDays (precomputed at load)
	ownerByDay map[int]parser.UserDayStats  // day index -> snapshot; arrives out of order (newest first)

	fileSeries  []string
	fileColors  map[string]color.Color
	ownerSeries []string
	ownerColors map[string]color.Color

	owner *ownerRun

	view      int
	cursor    int
	rangeDays int // 0 = all time; otherwise days back from the last active day
	width     int
	height    int

	filesLoaded bool
	filesErr    error
	ownersDone  bool
	ownersErr   error
}

func newChartModel(repo string, filter *parser.Filter) chartModel {
	return chartModel{repo: repo, filter: filter, width: 80, height: 24}
}

func (m chartModel) Init() tea.Cmd {
	return m.loadFiles()
}

func (m chartModel) loadFiles() tea.Cmd {
	return func() tea.Msg {
		days, err := parser.LocOverTime(m.repo, m.filter)
		return filesLoadedMsg{days: days, err: err}
	}
}

func (m *chartModel) startOwners() tea.Cmd {
	run := &ownerRun{ch: make(chan ownerTick, 64)}
	m.owner = run
	go func() {
		_, err := parser.UserLocOverTimeProgress(m.repo, m.filter, func(done, total int, idx int, s parser.UserDayStats) {
			run.ch <- ownerTick{idx: idx, stats: s}
		})
		run.err = err
		close(run.ch)
	}()
	return m.nextOwnerTick()
}

// nextOwnerTick returns a Cmd that blocks until the next day snapshot, or the completion message once done.
func (m *chartModel) nextOwnerTick() tea.Cmd {
	if m.owner == nil {
		return nil
	}
	run := m.owner
	return func() tea.Msg {
		tick, ok := <-run.ch
		if !ok {
			return ownersDoneMsg{err: run.err}
		}
		return tick
	}
}

func (m chartModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case filesLoadedMsg:
		m.filesLoaded = true
		m.filesErr = msg.err
		m.fileDays = msg.days
		// Parse dates once at load time; the chart re-renders every frame and must not parse per cell.
		m.times = make([]time.Time, len(msg.days))
		for i, d := range msg.days {
			m.times[i] = parseDay(d.Date)
		}
		m.buildFileSeries()
		if msg.err != nil || len(msg.days) == 0 {
			return m, nil
		}
		return m, m.startOwners()

	case ownerTick:
		if m.ownerByDay == nil {
			m.ownerByDay = map[int]parser.UserDayStats{}
		}
		m.ownerByDay[msg.idx] = msg.stats
		m.buildOwnerSeries()
		return m, m.nextOwnerTick()

	case ownersDoneMsg:
		m.ownersDone = true
		m.ownersErr = msg.err
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "left", "h":
			if m.cursor > 0 {
				m.cursor--
			}
		case "right", "l":
			if m.cursor < len(m.fileDays)-1 {
				m.cursor++
			}
		case "1":
			m.setRange(7)
		case "2":
			m.setRange(30)
		case "3":
			m.setRange(90)
		case "4":
			m.setRange(365)
		case "5":
			m.setRange(0)
		case "tab", "v":
			m.view = (m.view + 1) % 4
		case "shift+tab":
			if m.view > 0 {
				m.view--
			} else {
				m.view = 3
			}
		}
	}
	return m, nil
}

func (m *chartModel) setRange(days int) {
	m.rangeDays = days
	if s := m.visibleStart(); m.cursor < s {
		m.cursor = s
	}
}

// visibleStart is the index of the first day in the current range; it anchors at the last active day, not today.
func (m chartModel) visibleStart() int {
	if m.rangeDays == 0 || len(m.fileDays) == 0 {
		return 0
	}
	cutoff := m.times[len(m.fileDays)-1].AddDate(0, 0, -m.rangeDays)
	for i, t := range m.times {
		if !t.Before(cutoff) {
			return i
		}
	}
	return len(m.fileDays)
}

func parseDay(date string) time.Time {
	t, err := time.Parse("2006-01-02", date) // "YYYY-MM-DD"
	if err != nil {
		return time.Time{}
	}
	return t
}

func rangeLabel(days int) string {
	switch days {
	case 7:
		return "1w"
	case 30:
		return "1m"
	case 90:
		return "3m"
	case 365:
		return "1y"
	default:
		return "all time"
	}
}

func (m *chartModel) buildFileSeries() {
	totals := map[string]int64{}
	for _, d := range m.fileDays {
		for ext, n := range d.Types {
			totals[ext] += n
		}
	}
	m.fileSeries = sortedByTotal(totals)
	m.fileColors = colorsFor(m.fileSeries)
}

// buildOwnerSeries orders authors by lines owned; colors are assigned on first sight so they don't shuffle as data streams in.
func (m *chartModel) buildOwnerSeries() {
	totals := map[string]int64{}
	for _, d := range m.ownerByDay {
		for a, n := range d.Users {
			totals[a] += n
		}
	}
	if m.ownerColors == nil {
		m.ownerColors = map[string]color.Color{}
	}
	names := sortedByTotal(totals)
	for _, a := range names {
		if _, ok := m.ownerColors[a]; !ok {
			m.ownerColors[a] = palette[len(m.ownerColors)%len(palette)]
		}
	}
	m.ownerSeries = names
}

func sortedByTotal(totals map[string]int64) []string {
	names := make([]string, 0, len(totals))
	for n := range totals {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if totals[names[i]] != totals[names[j]] {
			return totals[names[i]] > totals[names[j]]
		}
		return names[i] < names[j]
	})
	return names
}

func colorsFor(names []string) map[string]color.Color {
	colors := make(map[string]color.Color, len(names))
	for i, n := range names {
		colors[n] = palette[i%len(palette)]
	}
	return colors
}

func (m chartModel) View() tea.View {
	if !m.filesLoaded {
		return tea.NewView("Computing file stats…\n")
	}
	if m.filesErr != nil {
		return tea.NewView("Error: " + m.filesErr.Error() + "\n")
	}
	if len(m.fileDays) == 0 {
		return tea.NewView("No commits found.\n")
	}

	var b strings.Builder
	title := m.repo + " — LOC over time"
	if m.rangeDays != 0 {
		title += " (" + rangeLabel(m.rangeDays) + ")"
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(title+"  ["+viewName(m.view)+"]") + "\n\n")

	visStart := m.visibleStart()

	switch m.view {
	case viewLangs:
		m.renderLegend(&b, "By language", m.fileSeries, m.fileColors)
		m.renderStackedChart(&b, m.fileSeries, m.fileColors, m.chartBarHeight(), func(idx int) map[string]int64 { return m.fileDays[idx].Types }, visStart)
		m.renderDayBreakdown(&b, m.fileColors, m.fileDays[m.cursor].Types)

	case viewOwners:
		status := "By contributor"
		if !m.ownersDone {
			status = fmt.Sprintf("By contributor %s %d/%d", progressBar(len(m.ownerByDay), len(m.fileDays)), len(m.ownerByDay), len(m.fileDays))
		} else if m.ownersErr != nil {
			status = "Contributors failed (" + m.ownersErr.Error() + ")"
		}
		m.renderLegend(&b, status, m.ownerSeries, m.ownerColors)
		m.renderStackedChart(&b, m.ownerSeries, m.ownerColors, m.chartBarHeight(), func(idx int) map[string]int64 {
			if s, ok := m.ownerByDay[idx]; ok {
				return s.Users
			}
			return nil
		}, visStart)
		if s, ok := m.ownerByDay[m.cursor]; ok {
			m.renderDayBreakdown(&b, m.ownerColors, s.Users)
		} else {
			b.WriteString("\n" + fmt.Sprintf("%s  (contributors: computing… %d/%d)", m.fileDays[m.cursor].Date, len(m.ownerByDay), len(m.fileDays)) + "\n")
		}

	case viewLangTable:
		// Cumulative counts, so the last day is the current all-time state.
		m.renderTotalTable(&b, "By language", m.fileColors, m.fileDays[len(m.fileDays)-1].Types)

	case viewOwnerTable:
		if last, ok := m.ownerByDay[len(m.fileDays)-1]; ok {
			if m.ownersDone && m.ownersErr != nil {
				b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Render("Some days failed to compute: "+m.ownersErr.Error()) + "\n\n")
			}
			m.renderTotalTable(&b, "By contributor", m.ownerColors, last.Users)
		} else if !m.ownersDone {
			b.WriteString(fmt.Sprintf("Contributors are still computing… (%d/%d)\n", len(m.ownerByDay), len(m.fileDays)))
		} else {
			b.WriteString("Owner computation failed: " + m.ownersErr.Error() + "\n")
		}
	}

	b.WriteString("\n←/→ day · 1:1w 2:1m 3:3m 4:1y 5:all · tab view · q quit\n")
	return tea.NewView(b.String())
}

func (m chartModel) chartBarHeight() int {
	h := m.height - 10
	if h < 6 {
		return 6
	}
	if h > 24 {
		return 24
	}
	return h
}

func (m chartModel) renderLegend(b *strings.Builder, title string, series []string, colors map[string]color.Color) {
	line := lipgloss.NewStyle().Bold(true).Render(title)
	if len(series) > 0 {
		var items []string
		for _, s := range series {
			items = append(items, lipgloss.NewStyle().Foreground(colors[s]).Render("██ "+s))
		}
		line += "  " + strings.Join(items, "  ")
	}
	b.WriteString(line + "\n")
}

// chartColumn is one rendered column of the stacked bar chart: either a single day
// (when there are fewer visible days than plot cells) or an aggregate bucket of days.
type chartColumn struct {
	dayIdx int             // absolute index into fileDays; -1 when no data in this column's range
	values map[string]int64 // series -> lines at the end of this column's range (nil = gap: no data)
}

// renderStackedChart draws a stacked bar chart of LOC over time. Columns are positioned by
// absolute day index within the visible range, so sparse owner data shows honest gaps instead
// of compressing missing days into a continuous line. getValues returns per-series line counts
// for a day index (nil when that day has no data yet); series order is bottom-up stacking order.
func (m chartModel) renderStackedChart(b *strings.Builder, series []string, colors map[string]color.Color, barH int, getValues func(idx int) map[string]int64, visStart int) {
	n := len(m.fileDays) - visStart
	if n <= 0 {
		b.WriteString("  (no data)\n")
		return
	}

	// Pass 1: the true peak across all visible days, so the scale is honest and stable as data streams in.
	maxTotal := int64(0)
	for i := visStart; i < len(m.fileDays); i++ {
		var t int64
		for _, v := range getValues(i) {
			t += v
		}
		if t > maxTotal {
			maxTotal = t
		}
	}
	if maxTotal == 0 {
		b.WriteString("  (waiting for data…)\n")
		return
	}

	margin := len(fmt.Sprintf("%d", maxTotal)) + 2 // y-axis label column width
	plotW := m.width - margin
	if plotW < 10 {
		plotW = 10
	}

	// Columns: one per day when there is room, otherwise bucket consecutive days so each
	// column covers at most ceil(n/plotW) days and the chart fills the width.
	bucketSize := 1
	if n > plotW {
		bucketSize = (n + plotW - 1) / plotW
	}
	numCols := (n + bucketSize - 1) / bucketSize
	gap := 0
	colWidth := plotW / numCols // ≥ 1 since numCols ≤ plotW; fat columns for small ranges
	if numCols > 1 && colWidth >= 3 {
		// Leave a one-cell gap between fat columns so they read as distinct blocks.
		if w := (plotW - (numCols - 1)) / numCols; w >= 2 && w < colWidth {
			colWidth = w
			gap = 1
		}
	}
	if colWidth < 1 {
		colWidth = 1
		gap = 0
	}

	cols := make([]chartColumn, numCols)
	for bIdx := 0; bIdx < numCols; bIdx++ {
		start := visStart + bIdx*bucketSize
		end := start + bucketSize
		if end > len(m.fileDays) {
			end = len(m.fileDays)
		}
		// The column represents the state at the end of its range: use the last day with data.
		var values map[string]int64
		dayIdx := -1
		for i := start; i < end; i++ {
			if v := getValues(i); len(v) > 0 {
				values = v
				dayIdx = i
			}
		}
		cols[bIdx] = chartColumn{dayIdx: dayIdx, values: values}
	}

	// Grid of series indices (-1 = empty cell); row 0 is the top. Bars stack bottom-up in series order.
	grid := make([][]int, barH)
	for r := range grid {
		grid[r] = make([]int, plotW)
		for c := range grid[r] {
			grid[r][c] = -1
		}
	}

	scale := float64(barH) / float64(maxTotal)
	for bIdx, col := range cols {
		if len(col.values) == 0 {
			continue // honest gap: no data for this column's range
		}
		var total int64
		for _, v := range col.values {
			total += v
		}
		height := int(math.Round(float64(total) * scale))
		if height < 1 && total > 0 {
			height = 1 // keep tiny columns visible
		}
		segs := distributeHeight(col.values, series, height)
		x0 := bIdx * (colWidth + gap)
		row := barH - 1
		for _, s := range segs {
			for r := row; r > row-s.h; r-- {
				for j := 0; j < colWidth && x0+j < plotW; j++ {
					grid[r][x0+j] = s.idx
				}
			}
			row -= s.h
		}
	}

	styles := make([]lipgloss.Style, len(series))
	for i, name := range series {
		styles[i] = lipgloss.NewStyle().Foreground(colors[name])
	}
	dimLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	accent := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("226"))

	// Rows top to bottom; one Render per color run, not per cell.
	for r := 0; r < barH; r++ {
		var row strings.Builder
		if r == 0 {
			row.WriteString(dimLabel.Render(fmt.Sprintf("%*s ", margin-1, fmt.Sprintf("%d", maxTotal))))
		} else {
			row.WriteString(strings.Repeat(" ", margin))
		}
		x := 0
		for x < plotW {
			k := grid[r][x]
			if k == -1 {
				start := x
				for x < plotW && grid[r][x] == -1 {
					x++
				}
				row.WriteString(strings.Repeat(" ", x-start))
				continue
			}
			start := x
			for x < plotW && grid[r][x] == k {
				x++
			}
			row.WriteString(styles[k].Render(strings.Repeat("█", x-start)))
		}
		b.WriteString(row.String() + "\n")
	}

	// Cursor guide: marker row under the chart at the selected day's column.
	markerX := -1
	if m.cursor >= visStart && m.cursor < len(m.fileDays) {
		cIdx := (m.cursor - visStart) / bucketSize
		if cIdx > numCols-1 {
			cIdx = numCols - 1
		}
		markerX = cIdx*(colWidth+gap) + colWidth/2
	}
	var mrow strings.Builder
	mrow.WriteString(strings.Repeat(" ", margin))
	if markerX >= 0 && markerX < plotW {
		mrow.WriteString(strings.Repeat(" ", markerX))
		mrow.WriteString(accent.Render("▼"))
	}
	b.WriteString(mrow.String() + "\n")

	// Date labels below the chart at evenly spaced columns; the selected day's label is bold and wins collisions.
	type dateLabel struct {
		start    int
		text     string
		selected bool
	}
	labels := []dateLabel{}
	if m.cursor >= visStart && m.cursor < len(m.fileDays) {
		cIdx := (m.cursor - visStart) / bucketSize
		if cIdx > numCols-1 {
			cIdx = numCols - 1
		}
		text := m.fileDays[m.cursor].Date // "YYYY-MM-DD"
		if len(text) <= plotW {
			start := cIdx*(colWidth+gap) + colWidth/2 - len(text)/2
			if start < 0 {
				start = 0
			}
			if start+len(text) > plotW {
				start = plotW - len(text)
			}
			labels = append(labels, dateLabel{start: start, text: text, selected: true})
		}
	}
	maxLabels := plotW / 7 // MM-DD is 5 chars + gap
	if maxLabels < 1 {
		maxLabels = 1
	}
	labelEvery := (numCols + maxLabels - 1) / maxLabels
	for j := 0; j < numCols; j += labelEvery {
		endDay := visStart + (j+1)*bucketSize
		if endDay > len(m.fileDays) {
			endDay = len(m.fileDays)
		}
		text := m.fileDays[endDay-1].Date[5:10] // "MM-DD" of the column's last day
		if len(text) > plotW {
			continue
		}
		start := j*(colWidth+gap) + colWidth/2 - len(text)/2
		if start < 0 {
			start = 0
		}
		if start+len(text) > plotW {
			start = plotW - len(text)
		}
		overlap := false
		for _, l := range labels {
			if start < l.start+len(l.text) && start+len(text) > l.start {
				overlap = true
				break
			}
		}
		if !overlap {
			labels = append(labels, dateLabel{start: start, text: text})
		}
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i].start < labels[j].start })

	var drow strings.Builder
	drow.WriteString(strings.Repeat(" ", margin))
	pos := 0
	for _, l := range labels {
		if l.start > pos {
			drow.WriteString(strings.Repeat(" ", l.start-pos))
		}
		st := dimLabel
		if l.selected {
			st = accent
		}
		drow.WriteString(st.Render(l.text))
		pos = l.start + len(l.text)
	}
	b.WriteString(drow.String() + "\n")
}

// seg is one stacked segment of a column: series index and height in rows.
type seg struct {
	idx int
	h   int
}

// distributeHeight splits a column's total height across its nonzero segments proportionally,
// using the largest-remainder method so the heights sum exactly to `height`. Segments are
// returned in series order (bottom-up stacking).
func distributeHeight(values map[string]int64, series []string, height int) []seg {
	if height <= 0 {
		return nil
	}
	type rawSeg struct {
		idx  int
		val  int64
		frac float64
	}
	var rs []rawSeg
	var colTotal int64
	for si, name := range series {
		v := values[name]
		if v <= 0 {
			continue
		}
		rs = append(rs, rawSeg{idx: si, val: v})
		colTotal += v
	}
	if len(rs) == 0 || colTotal == 0 {
		return nil
	}
	floors := make([]int, len(rs))
	sumFloors := 0
	for i, r := range rs {
		raw := float64(r.val) / float64(colTotal) * float64(height)
		f := int(math.Floor(raw))
		floors[i] = f
		sumFloors += f
		rs[i].frac = raw - float64(f)
	}
	remainder := height - sumFloors
	if remainder < 0 {
		remainder = 0
	}
	if remainder > len(rs) {
		remainder = len(rs)
	}
	order := make([]int, len(rs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return rs[order[a]].frac > rs[order[b]].frac })
	for k := 0; k < remainder; k++ {
		floors[order[k]]++
	}
	segs := make([]seg, 0, len(rs))
	for i, r := range rs {
		if floors[i] > 0 {
			segs = append(segs, seg{idx: r.idx, h: floors[i]})
		}
	}
	return segs
}

func seriesByValue(values map[string]int64) []string {
	names := make([]string, 0, len(values))
	for n, v := range values {
		if v > 0 {
			names = append(names, n)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		if values[names[i]] != values[names[j]] {
			return values[names[i]] > values[names[j]]
		}
		return names[i] < names[j]
	})
	return names
}

func (m chartModel) renderDayBreakdown(b *strings.Builder, colors map[string]color.Color, values map[string]int64) {
	var total int64
	for _, v := range values {
		total += v
	}

	b.WriteString("\n" + lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%s  (%d lines)", m.fileDays[m.cursor].Date, total)) + "\n")
	for _, name := range seriesByValue(values) {
		square := lipgloss.NewStyle().Foreground(colors[name]).Render("██")
		b.WriteString(fmt.Sprintf("  %s %-24s %d\n", square, name, values[name]))
	}
}

func (m chartModel) renderTotalTable(b *strings.Builder, title string, colors map[string]color.Color, values map[string]int64) {
	var total int64
	for _, v := range values {
		total += v
	}

	b.WriteString(lipgloss.NewStyle().Bold(true).Render(title+" — all time") + "\n\n")

	names := seriesByValue(values)
	if len(names) == 0 {
		b.WriteString("  (no lines)\n")
		return
	}

	nameW := 0
	for _, n := range names {
		if len(n) > nameW {
			nameW = len(n)
		}
	}
	if nameW > 28 {
		nameW = 28
	}

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	b.WriteString(dim.Render(fmt.Sprintf("   %-*s  %10s  %7s", nameW, "name", "lines", "share")) + "\n")
	for _, n := range names {
		v := values[n]
		share := 0.0
		if total > 0 {
			share = float64(v) / float64(total) * 100
		}
		square := lipgloss.NewStyle().Foreground(colors[n]).Render("██ ")
		b.WriteString(fmt.Sprintf("  %s%-*s  %10d  %6.1f%%\n", square, nameW, n, v, share))
	}
	b.WriteString(dim.Render(fmt.Sprintf("   %-*s  %10d", nameW, "total", total)) + "\n")
}

func progressBar(done, total int) string {
	if total <= 0 {
		return ""
	}
	const width = 12
	filled := done * width / total
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}
