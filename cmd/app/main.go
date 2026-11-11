package main

import (
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
	repos := parser.GetGitDirPaths("~/Documents")

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

	p = tea.NewProgram(newChartModel(m.selected, explicitFileTypes))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to run chart for %s: %v\n", m.selected, err)
		os.Exit(1)
	}
}
