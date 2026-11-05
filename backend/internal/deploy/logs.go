package deploy

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"zhan/backend/internal/command"
)

type logWriter struct {
	mu         sync.Mutex
	file       *os.File
	pending    []byte
	written    int64
	max        int64
	privateKey bool
	skip       bool
	truncated  bool
	err        error
}

func (e *Engine) LogPath(id int64) string {
	return filepath.Join(e.Config.DataDir, "logs", fmt.Sprintf("task-%d.log", id))
}
func newLog(path string, max int) (*logWriter, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	return &logWriter{file: file, max: int64(max) * 1024 * 1024, written: info.Size()}, nil
}
func (w *logWriter) line(p []byte) {
	if w.err != nil {
		return
	}
	if w.written >= w.max {
		if !w.truncated {
			_, w.err = w.file.WriteString("\n[日志达到容量上限，后续输出省略]\n")
			w.truncated = true
		}
		return
	}
	text := string(p)
	if w.privateKey {
		if strings.Contains(text, "-----END ") && strings.Contains(text, "PRIVATE KEY-----") {
			w.privateKey = false
		}
		return
	}
	if strings.Contains(text, "-----BEGIN ") && strings.Contains(text, "PRIVATE KEY-----") {
		w.privateKey = !strings.Contains(text, "-----END ")
		text = "[PRIVATE KEY REDACTED]\n"
	}
	line := command.Redact(text)
	remaining := w.max - w.written
	if int64(len(line)) > remaining {
		line = "[当前行超出日志容量，已省略]\n"
	}
	n, err := w.file.WriteString(line)
	w.written += int64(n)
	w.err = err
}
func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		idx := bytes.IndexByte(p, '\n')
		if idx < 0 {
			if !w.skip {
				if len(w.pending)+len(p) > 65536 {
					w.pending = nil
					w.skip = true
				} else {
					w.pending = append(w.pending, p...)
				}
			}
			break
		}
		part := p[:idx+1]
		p = p[idx+1:]
		if w.skip || len(w.pending)+len(part) > 65536 {
			w.line([]byte("[超长日志行已省略]\n"))
			w.pending = nil
			w.skip = false
			continue
		}
		w.pending = append(w.pending, part...)
		w.line(w.pending)
		w.pending = nil
	}
	if w.err != nil {
		return n, w.err
	}
	return n, nil
}
func (w *logWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 {
		w.line(w.pending)
	}
	err := w.file.Close()
	if w.err != nil {
		return w.err
	}
	return err
}
func ReadLog(path string, offset int64, limit int) (string, int64, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", offset, nil
	}
	if err != nil {
		return "", offset, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", offset, err
	}
	if offset < 0 {
		offset = max(int64(0), info.Size()-int64(limit))
	}
	if offset > info.Size() {
		offset = info.Size()
	}
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return "", offset, err
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)))
	return string(data), offset + int64(len(data)), err
}
