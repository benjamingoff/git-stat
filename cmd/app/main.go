package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

func lineCounter() {}

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

func countLines(filePath string) int64 {
	file, err := os.ReadFile(filePath)
	if err != nil {
		return 0
	}

	reader := bytes.NewReader(file)
	lineCount := 0
	buffer := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buffer)
		if err != nil {
			break
		}
		lineCount += bytes.Count(buffer[:n], []byte("\n"))
	}
	return int64(lineCount)
}

func getFilePaths(root string, excludeList []string) []string {
	var filePaths []string
	err := filepath.WalkDir(root, func(path string, dir os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		for _, exclude := range excludeList {
			if strings.HasPrefix(path, exclude) {
				return nil
			}
		}

		if !dir.IsDir() {
			filePaths = append(filePaths, path)
		}
		return nil
	})

	if err != nil {
		fmt.Println(err)
	}
	return filePaths
}

func main() {
	fmt.Println(hasGitRepo())

	excludeList := []string{".git", ".idea"}
	dirs := getFilePaths(".", excludeList)

	for _, dir := range dirs {
		lines := countLines(dir)
		fmt.Println(dir, lines)
	}

	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		panic(err)
	}
}
