package parser

import (
	"runtime"
	"sync"
)

func ProcessFiles(paths []string, counter *FileLineCounter) error {
	fileCh := make(chan string, len(paths))
	for _, p := range paths {
		fileCh <- p
	}
	close(fileCh)

	workers := runtime.NumCPU()
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for path := range fileCh {
				lines := CountLines(path)
				if ft, err := GetFileType(path); err == nil {
					counter.UpdateFileLineCount(ft, lines)
				}
			}
		}()
	}
	wg.Wait()
	return nil
}
