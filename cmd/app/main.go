package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/benjamingoff/git-stat/internal/parser"
)

type model struct {
	repos    []string
	cursor   int
	selected string
	width    int
	height   int
}

func initialModel(repos []string) model {
	return model{
		repos:  repos,
		cursor: 0,
		width:  80,
		height: 24,
	}
}

var (
	titleBarStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("57"))
	selectedRowStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("57"))
	emptyTitleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("226"))
)

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyPressMsg:
		switch msg.String() {

		case "ctrl+c", "q":
			return m, tea.Quit

		case "up":
			if m.cursor > 0 {
				m.cursor--
			}

		case "down":
			if m.cursor < len(m.repos)-1 {
				m.cursor++
			}

		case "enter":
			if m.cursor < len(m.repos) {
				m.selected = m.repos[m.cursor]
			}
			return m, tea.Quit
		}
	}

	return m, nil
}

// windowStart returns the index of the first visible row so that cursor is always in view.
func windowStart(cursor, total, rows int) int {
	if total <= rows || rows < 1 {
		return 0
	}
	start := cursor - rows/2 // keep the selection roughly centered
	if start < 0 {
		start = 0
	}
	if start > total-rows {
		start = total - rows
	}
	return start
}

// splitLine renders a full-width line with left and right content, padding the gap.
func splitLine(width int, left, right string) string {
	if width <= 0 {
		return ""
	}
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	if lw+rw > width { // shrink the left text to make room for the right side
		left = truncate(left, max(1, width-rw-2))
		lw = lipgloss.Width(left)
	}
	pad := width - lw - rw
	if pad < 0 {
		pad = 0
	}
	return left + strings.Repeat(" ", pad) + right
}

// centerLine horizontally centers s within width cells (s is expected to fit).
func centerLine(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width || width <= 0 {
		return s
	}
	pad := (width - w) / 2
	return strings.Repeat(" ", pad) + s
}

func pluralRepos(n int) string {
	if n == 1 {
		return "1 repo"
	}
	return fmt.Sprintf("%d repos", n)
}

// footer renders the key hints (bold keys + dim labels), dropping whole segments
// from the end until they fit within budget cells.
func (m model) footer(budget int) string {
	type hint struct{ k, label string }
	hints := []hint{{"↑/↓", "navigate"}, {"enter", "select"}, {"q", "quit"}}
	for len(hints) > 0 {
		var b strings.Builder
		for i, h := range hints {
			if i > 0 {
				b.WriteString(dimStyle.Render(" · "))
			}
			b.WriteString(keyStyle.Render(h.k) + dimStyle.Render(" "+h.label))
		}
		s := b.String()
		if lipgloss.Width(s) <= budget {
			return s
		}
		hints = hints[:len(hints)-1] // drop the last segment and retry
	}
	return ""
}

func (m model) View() tea.View {
	width := m.width
	if width <= 0 {
		width = 80
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	var b strings.Builder

	// Title bar: full-width accent strip with the app name and repo count.
	title := splitLine(width, " git-stat — select a repository", pluralRepos(len(m.repos)))
	b.WriteString(titleBarStyle.Width(width).Render(truncate(title, width)) + "\n")

	if len(m.repos) == 0 {
		// Empty state: styled message instead of a bare line.
		b.WriteString("\n")
		b.WriteString(centerLine(emptyTitleStyle.Render("No git repositories found"), width) + "\n")
		hint := dimStyle.Render(truncate("Pass a directory containing git repos as the first argument, e.g. `git-stat ~/Documents`.", width))
		b.WriteString(centerLine(hint, width) + "\n")
		b.WriteString("\n" + dimStyle.Render("q quit") + "\n")
		return tea.NewView(b.String())
	}

	// Visible rows: title bar (1) + top gap (1) + bottom gap (1) + footer (1) = 4 lines of overhead.
	rows := height - 4
	if rows < 1 {
		rows = 1
	}
	start := windowStart(m.cursor, len(m.repos), rows)
	end := min(start+rows, len(m.repos))

	b.WriteString("\n") // gap after the title bar
	for i := start; i < end; i++ {
		repo := m.repos[i]
		if i == m.cursor {
			b.WriteString(selectedRowStyle.Width(width).Render(truncate("> "+repo, width)) + "\n")
		} else {
			line := "  " + repo // two cells to align with the selected row's marker
			if lipgloss.Width(line) > width {
				line = truncate(line, width)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n") // gap before the footer
	pos := fmt.Sprintf("%d of %d", m.cursor+1, len(m.repos))
	budget := width - lipgloss.Width(pos) - 2 // leave a small gap between hints and position
	if budget < 0 {
		budget = 0
	}
	b.WriteString(splitLine(width, m.footer(budget), pos) + "\n")
	return tea.NewView(b.String())
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "git-stat: %s does not exist\n", root)
		} else {
			fmt.Fprintf(os.Stderr, "git-stat: cannot access %s: %v\n", root, err)
		}
		fmt.Fprintln(os.Stderr, "usage: git-stat [directory]")
		os.Exit(1)
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "git-stat: %s is not a directory\n", root)
		fmt.Fprintln(os.Stderr, "usage: git-stat [directory]")
		os.Exit(1)
	}

	p := tea.NewProgram(initialModel(parser.GetGitDirPaths(root)))
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "git-stat: %v\n", err)
		os.Exit(1)
	}

	m, ok := finalModel.(model)
	if !ok || m.selected == "" {
		return
	}

	cfg := parser.LoadConfig(m.selected)
	filter := parser.NewFilter(m.selected, cfg)

	p = tea.NewProgram(newChartModel(m.selected, filter))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to run chart for %s: %v\n", m.selected, err)
		os.Exit(1)
	}
}
