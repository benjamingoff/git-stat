package parser

import (
	"bytes"
	"os"
	"sync"
)

func CountLines(filePath string) int64 {
	b, err := os.ReadFile(filePath)
	if err != nil {
		return 0
	}
	r := bytes.NewReader(b)
	var n int64
	buf := make([]byte, 32*1024)
	for {
		c, err := r.Read(buf)
		if err != nil {
			break
		}
		n += int64(bytes.Count(buf[:c], []byte("\n")))
	}
	return n
}

type FileLineCounter struct {
	mu    sync.Mutex
	files map[string]int64
}

func NewFileLineCounter() *FileLineCounter {
	return &FileLineCounter{files: make(map[string]int64)}
}

func (f *FileLineCounter) UpdateFileLineCount(fileType string, lines int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[fileType] += lines
}

func (f *FileLineCounter) Files() map[string]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.files
}
