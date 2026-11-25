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
}

type dayValues struct {
	date   string
	values map[string]int64
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
	viewLangs      = iota // line graph: LOC per language over time
	viewOwners            // line graph: LOC per contributor over time
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

	fileDays   []parser.DayStats           // fast single-pass stats; ready first
	ownerByDay map[int]parser.UserDayStats // day index -> snapshot; arrives out of order (newest first)

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
	cutoff := parseDay(m.fileDays[len(m.fileDays)-1].Date).AddDate(0, 0, -m.rangeDays)
	for i, d := range m.fileDays {
		if !parseDay(d.Date).Before(cutoff) {
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
	cursorRel := m.cursor - visStart

	switch m.view {
	case viewLangs:
		m.renderLegend(&b, "By language", m.fileSeries, m.fileColors)
		m.renderChart(&b, fileSnapsOf(m.fileDays[visStart:]), m.fileSeries, m.fileColors, m.chartBarHeight(), cursorRel)
		m.renderDayBreakdown(&b, m.fileColors, m.fileDays[m.cursor].Types)

	case viewOwners:
		status := "By contributor"
		if !m.ownersDone {
			status = fmt.Sprintf("By contributor %s %d/%d", progressBar(len(m.ownerByDay), len(m.fileDays)), len(m.ownerByDay), len(m.fileDays))
		} else if m.ownersErr != nil {
			status = "Contributors failed (" + m.ownersErr.Error() + ")"
		}
		m.renderLegend(&b, status, m.ownerSeries, m.ownerColors)
		m.renderChart(&b, m.ownerArrivedSnaps(visStart), m.ownerSeries, m.ownerColors, m.chartBarHeight(), m.ownerCursorRel(visStart))
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

type cell struct {
	r   rune
	c   int // series index; -1 empty, dimIdx for gridlines
	dot bool
}

const dimIdx = -2

// renderChart draws one polyline per series stretched across the full plot width;
// cursor is an index into snaps (-1 selects nothing).
func (m chartModel) renderChart(b *strings.Builder, snaps []dayValues, series []string, colors map[string]color.Color, barH, cursor int) {
	plotW := m.width - 10 // leave room for the y-axis labels
	if plotW < 10 {
		plotW = 10
	}
	n := len(snaps)
	if n == 0 {
		b.WriteString("  (waiting for data…)\n")
		return
	}

	// Sample so there is at most one point per column, then stretch the
	// points across the full plot width.
	step := 1
	if n > plotW {
		step = (n + plotW - 1) / plotW
	}
	points := 0
	for i := 0; i < n; i += step {
		points++
	}

	maxTotal := int64(1)
	for _, s := range snaps {
		var t int64
		for _, v := range s.values {
			t += v
		}
		if t > maxTotal {
			maxTotal = t
		}
	}

	yPos := func(v int64) int {
		return int(float64(v) / float64(maxTotal) * float64(barH-1))
	}
	xPos := func(k int) int {
		if points == 1 {
			return plotW / 2
		}
		return int(math.Round(float64(k) * float64(plotW-1) / float64(points-1)))
	}

	grid := make([][]cell, barH)
	for r := range grid {
		grid[r] = make([]cell, plotW)
		for c := range grid[r] {
			grid[r][c] = cell{r: ' ', c: -1}
		}
	}

	// Faint horizontal gridline at each tick; the bottom one is the axis.
	tickRow := map[int]string{}
	for _, t := range niceTicks(maxTotal, 5) {
		r := yPos(t)
		if _, ok := tickRow[r]; !ok {
			tickRow[r] = formatTick(t)
		}
		ch := '·'
		if r == 0 {
			ch = '-'
		}
		for c := 0; c < plotW; c++ {
			grid[r][c] = cell{r: ch, c: dimIdx}
		}
	}

	// Pass 1: line segments between consecutive points (later series
	// overwrite earlier ones on overlap).
	for si := range series {
		name := series[si]
		prevX, prevY, k := -1, 0, 0
		for i := 0; i < n; i += step {
			x := xPos(k)
			y := yPos(snaps[i].values[name])
			if prevX >= 0 {
				drawLine(grid, prevX, prevY, x, y, si)
			}
			prevX, prevY, k = x, y, k+1
		}
	}

	for si := range series {
		name := series[si]
		k := 0
		for i := 0; i < n; i += step {
			grid[yPos(snaps[i].values[name])][xPos(k)] = cell{r: '•', c: si, dot: true}
			k++
		}
	}

	margin := 1
	for _, l := range tickRow {
		if len(l)+1 > margin {
			margin = len(l) + 1
		}
	}

	// The selected day snaps to the nearest drawn point.
	selX := -1
	if cursor >= 0 && cursor < n {
		selX = xPos(cursor / step)
	}

	styles := make([]lipgloss.Style, len(series))
	for i, s := range series {
		styles[i] = lipgloss.NewStyle().Foreground(colors[s])
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	for r := barH - 1; r >= 0; r-- {
		var row strings.Builder
		if l, ok := tickRow[r]; ok {
			row.WriteString(fmt.Sprintf("%*s ", margin-1, l))
		} else {
			row.WriteString(strings.Repeat(" ", margin))
		}
		for c := 0; c < plotW; c++ {
			cl := grid[r][c]
			if cl.c == -1 {
				row.WriteString(" ")
				continue
			}
			var st lipgloss.Style
			switch {
			case cl.c == dimIdx:
				st = dim
			case cl.dot && c == selX:
				st = styles[cl.c].Underline(true)
			default:
				st = styles[cl.c]
			}
			row.WriteString(st.Render(string(cl.r)))
		}
		b.WriteString(row.String() + "\n")
	}

	label := make([]string, plotW)
	for i := range label {
		label[i] = " "
	}
	lastEnd := -1
	k := 0
	for i := 0; i < n; i += step {
		s := snaps[i].date
		if len(s) >= 10 {
			s = s[5:10] // "YYYY-MM-DD" -> "MM-DD"
		}
		start := xPos(k) - len(s)/2
		end := start + len(s)
		if start >= 0 && end <= plotW && start > lastEnd {
			for j, ch := range s {
				label[start+j] = string(ch)
			}
			lastEnd = end
		}
		k++
	}
	b.WriteString(strings.Repeat(" ", margin) + strings.Join(label, "") + "\n")
}

// drawLine connects (x0,y0) to (x1,y1), x1 > x0, stepping one column at a
// time with slope characters: - flat, / rising, \ falling, | steep.
func drawLine(grid [][]cell, x0, y0, x1, y1, si int) {
	prevY := y0
	for x := x0 + 1; x <= x1; x++ {
		t := float64(x-x0) / float64(x1-x0)
		y := int(math.Round(float64(y0) + t*float64(y1-y0)))
		dy := y - prevY
		var ch rune
		switch {
		case dy == 0:
			ch = '-'
		case dy > 0:
			if dy >= 2 {
				ch = '|'
			} else {
				ch = '/'
			}
		default:
			if dy <= -2 {
				ch = '|'
			} else {
				ch = '\\'
			}
		}
		r := y
		if prevY < r {
			r = prevY // sit on the lower cell of the step
		}
		if r >= 0 && r < len(grid) {
			grid[r][x] = cell{r: ch, c: si}
		}
		prevY = y
	}
}

func niceTicks(max int64, count int) []int64 {
	if max <= 0 || count < 2 {
		return []int64{0}
	}
	raw := float64(max) / float64(count-1)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	norm := raw / mag
	var nice float64
	switch {
	case norm <= 1:
		nice = 1
	case norm <= 2:
		nice = 2
	case norm <= 5:
		nice = 5
	default:
		nice = 10
	}
	step := int64(nice * mag)
	var ticks []int64
	for v := int64(0); v <= max; v += step {
		ticks = append(ticks, v)
	}
	return ticks
}

func formatTick(v int64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.0fM", float64(v)/1e6)
	case v >= 1000:
		k := float64(v) / 1000
		if k == math.Trunc(k) {
			return fmt.Sprintf("%dk", int64(k))
		}
		return fmt.Sprintf("%.1fk", k)
	default:
		return fmt.Sprintf("%d", v)
	}
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

func fileSnapsOf(days []parser.DayStats) []dayValues {
	out := make([]dayValues, len(days))
	for i, d := range days {
		out[i] = dayValues{date: d.Date, values: d.Types}
	}
	return out
}

func (m chartModel) ownerArrivedSnaps(visStart int) []dayValues {
	out := make([]dayValues, 0, len(m.ownerByDay))
	for i := visStart; i < len(m.fileDays); i++ {
		if s, ok := m.ownerByDay[i]; ok {
			out = append(out, dayValues{date: s.Date, values: s.Users})
		}
	}
	return out
}

// ownerCursorRel is the x index of the selected day among arrived snapshots, or -1 if it has not arrived yet.
func (m chartModel) ownerCursorRel(visStart int) int {
	if _, ok := m.ownerByDay[m.cursor]; !ok {
		return -1
	}
	pos := 0
	for i := visStart; i < m.cursor; i++ {
		if _, ok := m.ownerByDay[i]; ok {
			pos++
		}
	}
	return pos
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
