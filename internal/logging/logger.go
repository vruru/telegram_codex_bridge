package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	FilePath   string
	MaxSizeMB  int
	MaxBackups int
}

type rotatingWriter struct {
	path       string
	maxSize    int64
	maxBackups int

	mu   sync.Mutex
	file *os.File
	size int64
}

func NewLogger(cfg Config) (*log.Logger, io.Closer, error) {
	if cfg.MaxSizeMB <= 0 {
		cfg.MaxSizeMB = 20
	}
	if cfg.MaxBackups <= 0 {
		cfg.MaxBackups = 5
	}

	writer, err := newRotatingWriter(cfg.FilePath, int64(cfg.MaxSizeMB)*1024*1024, cfg.MaxBackups)
	if err != nil {
		return nil, nil, err
	}

	logger := log.New(writer, "[bridge] ", log.LstdFlags|log.Lmsgprefix)
	return logger, writer, nil
}

func newRotatingWriter(path string, maxSize int64, maxBackups int) (*rotatingWriter, error) {
	if path == "" {
		return nil, fmt.Errorf("log path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("stat log file: %w", err)
	}

	return &rotatingWriter{
		path:       path,
		maxSize:    maxSize,
		maxBackups: maxBackups,
		file:       file,
		size:       info.Size(),
	}, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, fmt.Errorf("log writer is closed")
	}
	if w.maxSize > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotatingWriter) rotateLocked() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return fmt.Errorf("close log file for rotation: %w", err)
		}
		w.file = nil
	}

	if w.maxBackups > 0 {
		oldest := fmt.Sprintf("%s.%d", w.path, w.maxBackups)
		_ = os.Remove(oldest)
		for i := w.maxBackups - 1; i >= 1; i-- {
			src := fmt.Sprintf("%s.%d", w.path, i)
			dst := fmt.Sprintf("%s.%d", w.path, i+1)
			if _, err := os.Stat(src); err == nil {
				_ = os.Rename(src, dst)
			}
		}
		if _, err := os.Stat(w.path); err == nil {
			if err := os.Rename(w.path, w.path+".1"); err != nil {
				return fmt.Errorf("rotate log file: %w", err)
			}
		}
	} else {
		_ = os.Remove(w.path)
	}

	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open fresh log file: %w", err)
	}
	w.file = file
	w.size = 0
	return nil
}
