package storage

import "maps"

type MemStorage struct {
	Gauge   map[string]float64
	Counter map[string]int64
}

func NewMemStorage() *MemStorage {
	return &MemStorage{
		Gauge:   make(map[string]float64),
		Counter: make(map[string]int64),
	}
}

func (m *MemStorage) SetGauge(name string, value float64) error {
	m.Gauge[name] = value
	return nil
}

func (m *MemStorage) UpdateCounter(name string, value int64) error {
	m.Counter[name] += value
	return nil
}

func (m *MemStorage) GetCounter(name string) (int64, bool) {
	value, ok := m.Counter[name]
	return value, ok
}

func (m *MemStorage) GetGauge(name string) (float64, bool) {
	value, ok := m.Gauge[name]
	return value, ok

}

func (m *MemStorage) GetAllMetrics() (map[string]float64, map[string]int64) {
	copyGauge := make(map[string]float64, len(m.Gauge))
	copyCounter := make(map[string]int64, len(m.Counter))

	maps.Copy(copyGauge, m.Gauge)

	maps.Copy(copyCounter, m.Counter)

	return copyGauge, copyCounter
}

type Storage interface {
	SetGauge(name string, value float64) error
	UpdateCounter(name string, value int64) error
	GetCounter(name string) (int64, bool)
	GetGauge(name string) (float64, bool)
	GetAllMetrics() (map[string]float64, map[string]int64)
}
