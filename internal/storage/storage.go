package storage

import "sync"

type MemStorage struct {
	mu      sync.RWMutex
	gauge   map[string]float64
	counter map[string]int64
}

func NewMemStorage() *MemStorage {
	return &MemStorage{
		gauge:   make(map[string]float64),
		counter: make(map[string]int64),
	}
}

func (m *MemStorage) SetGauge(name string, value float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauge[name] = value
	return nil
}

func (m *MemStorage) UpdateCounter(name string, value int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counter[name] += value
	return nil
}

func (m *MemStorage) GetCounter(name string) (int64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.counter[name]
	return value, ok
}

func (m *MemStorage) GetGauge(name string) (float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.gauge[name]
	return value, ok
}

func (m *MemStorage) GetAllMetrics() (map[string]float64, map[string]int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	copyGauge := make(map[string]float64, len(m.gauge))
	copyCounter := make(map[string]int64, len(m.counter))

	for k, v := range m.gauge {
		copyGauge[k] = v
	}
	for k, v := range m.counter {
	copyGauge := make(map[string]float64, len(m.Gauge))
	copyCounter := make(map[string]int64, len(m.Counter))

	for k, v := range m.Gauge {
		copyGauge[k] = v
	}

	for k, v := range m.Counter {
		copyCounter[k] = v
	}

	return copyGauge, copyCounter
}

type Storage interface {
	SetGauge(name string, value float64) error
	UpdateCounter(name string, value int64) error
	GetCounter(name string) (int64, bool)
	GetGauge(name string) (float64, bool)
	GetAllMetrics() (map[string]float64, map[string]int64)
}
