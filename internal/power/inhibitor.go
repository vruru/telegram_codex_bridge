package power

import (
	"context"
	"log"
	"sync"
)

type logger interface {
	Printf(string, ...any)
}

type Manager struct {
	enabled bool
	logger  logger

	mu       sync.Mutex
	refCount int
	stopFn   func()
}

func New(enabled bool, log logger) *Manager {
	return &Manager{
		enabled: enabled,
		logger:  log,
	}
}

func (m *Manager) Acquire(ctx context.Context) func() {
	if m == nil || !m.enabled {
		return func() {}
	}

	m.mu.Lock()
	m.refCount++
	if m.refCount == 1 {
		stop, err := startInhibitorProcess(ctx)
		if err != nil {
			if m.logger != nil {
				m.logger.Printf("prevent sleep unavailable: %v", err)
			}
		} else {
			m.stopFn = stop
		}
	}
	m.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			m.release()
		})
	}
}

func (m *Manager) release() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.refCount > 0 {
		m.refCount--
	}
	if m.refCount != 0 {
		return
	}
	if m.stopFn != nil {
		m.stopFn()
		m.stopFn = nil
	}
}

func NewNoopLogger() logger {
	return log.New(nilDiscardWriter{}, "", 0)
}

type nilDiscardWriter struct{}

func (nilDiscardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}
