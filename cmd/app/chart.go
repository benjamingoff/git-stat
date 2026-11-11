package main

import (
	"fmt"
	"image/color"
	"math"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/benjamingoff/git-stat/internal/parser"
)

var palette = []color.Color{
	lipgloss.Color("39"), lipgloss.Color("205"), lipgloss.Color("41"), lipgloss.Color("213"),
	lipgloss.Color("51"), lipgloss.Color("171"), lipgloss.Color("208"), lipgloss.Color("114"),
}

// dayValues is one chart column: a date plus per-series line counts.
type dayValues struct {
	date   string
	values map[string]int64
}

type filesLoadedMsg struct {
	days []parser.DayStats
	err  error
}

type ownerTick struct {
	done  int
	total int
	stats parser.UserDayStats
}

type ownersDoneMsg struct{ err error }

// ownerRun carries the in-flight background owner computation between Cmds.
// The producer writes err and then closes ch; consumers read err only after
// observing the close, so the channel is the only synchronization needed.
type ownerRun struct {
	ch  chan ownerTick
	err error
}

type chartModel struct {
	repo         string
	includeTypes map[string]bool

	fileDays  []parser.DayStats     // fast single-pass stats; ready first
	ownerDays []parser.UserDayStats // slow per-day blame; arrives live

	fileSeries  []string
	fileColors  map[string]color.Color
	ownerSeries []string
	ownerColors map[string]color.Color

	owner *ownerRun

	cursor int
	width  int
	height int

	filesLoaded bool
	filesErr    error
	ownersDone  bool
	ownersErr   error
}

func newChartModel(repo string, includeTypes map[string]bool) chartModel {
	return chartModel{repo: repo, includeTypes: includeTypes, width: 80, height: 24}
}

func (m chartModel) Init() tea.Cmd {
	return m.loadFiles()
}

// loadFiles computes the fast file-type timeline in a background Cmd.
func (m chartModel) loadFiles() tea.Cmd {
	return func() tea.Msg {
		days, err := parser.LocOverTime(m.repo, m.includeTypes)
		return filesLoadedMsg{days: days, err: err}
	}
}

// startOwners kicks off the slow per-owner blame walk in the background and
// returns a Cmd that delivers the first day snapshot.
func (m chartModel) startOwners() tea.Cmd {
	run := &ownerRun{ch: make(chan ownerTick, 64)}
	m.owner = run
	go func() {
		_, err := parser.UserLocOverTimeProgress(m.repo, m.includeTypes, func(done, total int, s parser.UserDayStats) {
			run.ch <- ownerTick{done: done, total: total, stats: s}
		})
		run.err = err
		close(run.ch)
	}()
	return m.nextOwnerTick()
}

// nextOwnerTick returns a Cmd that blocks until the next day snapshot (or the
// completion message once the channel is closed).
func (m chartModel) nextOwnerTick() tea.Cmd {
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
		m.ownerDays = append(m.ownerDays, msg.stats)
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
		}
	}
	return m, nil
}

// buildFileSeries orders file types by total lines and assigns colors.
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

// buildOwnerSeries orders authors by lines owned so far and assigns each a
// stable color on first sight, so colors don't shuffle as data streams in.
func (m *chartModel) buildOwnerSeries() {
	totals := map[string]int64{}
	for _, d := range m.ownerDays {
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
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(m.repo+" — LOC over time") + "\n\n")

	m.renderLegend(&b, "Files:", m.fileSeries, m.fileColors)
	m.renderChart(&b, fileSnaps(m.fileDays), m.fileSeries, m.fileColors, m.barHeight())
	b.WriteString("\n")

	status := "Owners"
	if !m.ownersDone {
		status = fmt.Sprintf("Owners %s %d/%d", progressBar(len(m.ownerDays), len(m.fileDays)), len(m.ownerDays), len(m.fileDays))
	} else if m.ownersErr != nil {
		status = "Owners failed (" + m.ownersErr.Error() + ")"
	}
	m.renderLegend(&b, status, m.ownerSeries, m.ownerColors)
	m.renderChart(&b, ownerSnaps(m.ownerDays), m.ownerSeries, m.ownerColors, m.barHeight())

	m.renderDetails(&b)
	b.WriteString("\n←/→ select day · q quit\n")
	return tea.NewView(b.String())
}

// barHeight splits the available vertical space between the two charts.
func (m chartModel) barHeight() int {
	h := (m.height - 12) / 2
	if h < 4 {
		return 4
	}
	if h > 18 {
		return 18
	}
	return h
}

// renderLegend writes a "Title  ██ series  ██ series" line.
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

// cell is one character of the plot grid.
type cell struct {
	r   rune
	c   int // series index; -1 empty, dimIdx for gridlines
	dot bool
}

const dimIdx = -2

// renderChart draws a line graph: one colored polyline per series with a dot
// at each (sampled) day, y-axis tick labels on the left, faint horizontal
// gridlines, and date labels centered under their dots.
func (m chartModel) renderChart(b *strings.Builder, snaps []dayValues, series []string, colors map[string]color.Color, barH int) {
	maxCols := m.width - 10 // leave room for the y-axis labels
	if maxCols < 10 {
		maxCols = 10
	}
	n := len(snaps)
	if n == 0 {
		b.WriteString("  (waiting for data…)\n")
		return
	}
	step := 1
	if n > maxCols {
		step = (n + maxCols - 1) / maxCols
	}
	cols := 0
	for i := 0; i < n; i += step {
		cols++
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

	grid := make([][]cell, barH)
	for r := range grid {
		grid[r] = make([]cell, cols)
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
		for c := 0; c < cols; c++ {
			grid[r][c] = cell{r: ch, c: dimIdx}
		}
	}

	// Pass 1: line segments (later series overwrite earlier ones on overlap).
	for si := range series {
		name := series[si]
		prevX, prevY := -1, 0
		for c := 0; c < cols; c++ {
			y := yPos(snaps[c*step].values[name])
			if prevX >= 0 {
				drawSegment(grid, prevX, prevY, c, y, si)
			}
			prevX, prevY = c, y
		}
	}

	// Pass 2: dots on top of the lines.
	for si := range series {
		name := series[si]
		for c := 0; c < cols; c++ {
			y := yPos(snaps[c*step].values[name])
			grid[y][c] = cell{r: '•', c: si, dot: true}
		}
	}

	margin := 1
	for _, l := range tickRow {
		if len(l)+1 > margin {
			margin = len(l) + 1
		}
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
		for c := 0; c < cols; c++ {
			cl := grid[r][c]
			if cl.c == -1 {
				row.WriteString(" ")
				continue
			}
			var st lipgloss.Style
			switch {
			case cl.c == dimIdx:
				st = dim
			case cl.dot && c*step == m.cursor:
				st = styles[cl.c].Underline(true)
			default:
				st = styles[cl.c]
			}
			row.WriteString(st.Render(string(cl.r)))
		}
		b.WriteString(row.String() + "\n")
	}

	// X axis: "MM-DD" labels centered under their dots, skipping any that
	// would overlap a previous label.
	label := make([]string, cols)
	for i := range label {
		label[i] = " "
	}
	lastEnd := -1
	for c := 0; c < cols; c++ {
		s := snaps[c*step].date
		if len(s) >= 5 {
			s = s[:5] // "DD-MM-YYYY" -> "DD-MM"
		}
		start := c - len(s)/2
		end := start + len(s)
		if start < 0 || end > cols || start <= lastEnd {
			continue
		}
		for i, ch := range s {
			label[start+i] = string(ch)
		}
		lastEnd = end
	}
	b.WriteString(strings.Repeat(" ", margin) + strings.Join(label, "") + "\n")
}

// drawSegment connects (x0,y0) to the adjacent column x1 with a single slope
// character: - flat, / rising, \ falling, | steep.
func drawSegment(grid [][]cell, x0, y0, x1, y1, si int) {
	dy := y1 - y0
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
	r := y1
	switch {
	case dy > 0 && dy < 2:
		r = y0 // "/" sits on the lower cell of a rising step
	case dy < 0 && dy > -2:
		r = y1 // "\" sits on the lower cell of a falling step
	}
	if r < 0 || r >= len(grid) {
		return
	}
	grid[r][x1] = cell{r: ch, c: si}
}

// niceTicks returns ~count round tick values covering 0..max.
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

// renderDetails shows the breakdown for the selected day: file types always,
// owners once that day's snapshot has arrived.
func (m chartModel) renderDetails(b *strings.Builder) {
	d := m.fileDays[m.cursor]
	var total int64
	for _, v := range d.Types {
		total += v
	}

	b.WriteString("\n" + lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%s  (%d lines)", d.Date, total)) + "\n")

	var files []string
	for _, ext := range m.fileSeries {
		if v, ok := d.Types[ext]; ok {
			files = append(files, fmt.Sprintf("%s %d", lipgloss.NewStyle().Foreground(m.fileColors[ext]).Render("██"), v))
		}
	}
	b.WriteString("  files:  " + strings.Join(files, "  ") + "\n")

	if m.cursor < len(m.ownerDays) {
		var owners []string
		od := m.ownerDays[m.cursor]
		for _, a := range m.ownerSeries {
			if v, ok := od.Users[a]; ok {
				owners = append(owners, fmt.Sprintf("%s %-20s %d", lipgloss.NewStyle().Foreground(m.ownerColors[a]).Render("██"), a, v))
			}
		}
		b.WriteString("  owners: " + strings.Join(owners, "  ") + "\n")
	} else {
		b.WriteString("  owners: computing…\n")
	}
}

func fileSnaps(days []parser.DayStats) []dayValues {
	out := make([]dayValues, len(days))
	for i, d := range days {
		out[i] = dayValues{date: d.Date, values: d.Types}
	}
	return out
}

func ownerSnaps(days []parser.UserDayStats) []dayValues {
	out := make([]dayValues, len(days))
	for i, d := range days {
		out[i] = dayValues{date: d.Date, values: d.Users}
	}
	return out
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
