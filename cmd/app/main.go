package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
)

type model struct {
	choices  []string
	selected map[int]bool
	cursor   int
}

func initialModel() model {
	return model{
		choices:  []string{"carrots", "beans", "soda"},
		selected: map[int]bool{},
		cursor:   0,
	}
}

func hasGitRepo() bool {
	exists, err := os.ReadDir("./.git")
	if err != nil {
		return false
	}

	return len(exists) > 0
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
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}

		case "enter":
			if m.selected[m.cursor] == true {
				m.selected[m.cursor] = false
			} else {
				m.selected[m.cursor] = true
			}
		}
	}

	return m, nil
}

func (m model) View() tea.View {
	s := "Header for the question\n"

	for i, choice := range m.choices {
		cursor := " "
		if m.cursor == i {
			cursor = ">"
		}

		checked := " "
		if m.selected[i] == true {
			checked = "x"
		}
		s += fmt.Sprintf("%s [%s] %s\n", cursor, checked, choice)
	}

	s += "\nPress q to quit.\n"
	return tea.NewView(s)
}

func (m model) Init() tea.Cmd {
	return nil
}

func main() {
	fmt.Println(hasGitRepo())

	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		panic(err)
	}
}
