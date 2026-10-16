// SPDX-License-Identifier: BSD-2-Clause

package pkgmanager

import (
	"io"
	"os"
	"strings"
	"sync"
	"unicode"
)

const MaxLogBytes = 8 << 20

type LogWriter struct {
	mu      sync.Mutex
	output  io.Writer
	pending string
	written int
}

func NewLogWriter(output io.Writer) *LogWriter {
	w := &LogWriter{output: output}
	if file, ok := output.(*os.File); ok {
		if info, err := file.Stat(); err == nil {
			w.written = int(min(info.Size(), MaxLogBytes))
		}
	}
	return w
}
func (w *LogWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending += string(data)
	for {
		index := strings.IndexAny(w.pending, "\r\n")
		if index < 0 {
			break
		}
		w.line(w.pending[:index] + "\n")
		w.pending = w.pending[index+1:]
	}
	if len(w.pending) > 64<<10 {
		w.line("[oversized log line omitted]\n")
		w.pending = ""
	}
	return len(data), nil
}
func (w *LogWriter) line(line string) {
	if w.written >= MaxLogBytes {
		return
	}
	line = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, Redact(line))
	if len(line) > MaxLogBytes-w.written {
		line = "[log limit reached]\n"
		if len(line) > MaxLogBytes-w.written {
			line = line[:MaxLogBytes-w.written]
		}
		w.written = MaxLogBytes
	} else {
		w.written += len(line)
	}
	_, _ = io.WriteString(w.output, line)
}
func (w *LogWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending != "" {
		w.line(w.pending)
		w.pending = ""
	}
}
