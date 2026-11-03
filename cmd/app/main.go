package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

type model struct {
	choices  []string
	selected map[int]struct{}
	cursor   int
}

func initialModel() model {
	return model{
		choices:  []string{"carrots", "beans", "soda"},
		selected: map[int]struct{}{},
		cursor:   0,
	}
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

		}
	}

}

func (m model) Init() tea.Cmd {
	return nil
}

func main() {
	fmt.Println("hello world")
}
