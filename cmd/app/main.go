package main

import (
	"encoding/json"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/benjamingoff/git-stat/internal/parser"
)

type model struct {
	repos    []string
	cursor   int
	selected string
}

func initialModel(repos []string) model {
	return model{
		repos:  repos,
		cursor: 0,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

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

func (m model) View() tea.View {
	s := "Select a git repository:\n\n"

	if len(m.repos) == 0 {
		s += "No git repositories found.\n"
	}

	for i, repo := range m.repos {
		cursor := " "
		if m.cursor == i {
			cursor = ">"
		}
		s += fmt.Sprintf("%s %s\n", cursor, repo)
	}

	s += "\nPress enter to select, q to quit.\n"
	return tea.NewView(s)
}

func main() {
	repos := parser.GetGitDirPaths("~/Documents/GitHub")

	p := tea.NewProgram(initialModel(repos))
	finalModel, err := p.Run()
	if err != nil {
		panic(err)
	}

	m, ok := finalModel.(model)
	if !ok || m.selected == "" {
		return
	}

	explicitFileTypes := map[string]bool{"go": true, "ts": true, "tsx": true, "py": true}
	days, err := parser.LocOverTime(m.selected, explicitFileTypes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to compute LOC timeline for %s: %v\n", m.selected, err)
		os.Exit(1)
	}

	printDayStats(m.selected, days)
}

// printDayStats dumps the per-day, per-file-type line counts as a JSON-ish
// map, newest day first.
func printDayStats(repo string, days []parser.DayStats) {
	fmt.Println(repo)
	fmt.Println("{")
	for i := len(days) - 1; i >= 0; i-- {
		b, _ := json.Marshal(days[i].Types)
		comma := ","
		if i == 0 {
			comma = ""
		}
		fmt.Printf("  %q: %s%s\n", days[i].Date, b, comma)
	}
	fmt.Println("}")
}
