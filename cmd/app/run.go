package main

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/benjamingoff/git-stat/internal/parser"
)

func run(start time.Time) error {
	excludeList := []string{"/.git", "/.idea", "/frontend/node_modules", "/.venv", "/frontend/src/generated", "/.opencode"}
	explicitFileTypes := map[string]bool{"go": true, "tsx": true, "ts": true}
	filePaths := parser.GetFilePaths("../SoloCharts", excludeList, explicitFileTypes)

	flc := parser.NewFileLineCounter()

	if err := parser.ProcessFiles(filePaths, flc); err != nil {
		return err
	}

	fmt.Printf("Time taken: %v\n", time.Since(start))
	for ft, lines := range flc.Files() {
		fmt.Printf("%s: %d\n", ft, lines)
	}

	fmt.Println(parser.GetGitDirPaths("~/Documents"))
	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}
