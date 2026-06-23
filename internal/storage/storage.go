package storage

type MemStorage struct {
	Gauge   map[string]float64
	Counter map[string]int64
}

func (m *MemStorage) SetGauge(name string, value float64) error {
	m.Gauge[name] = value
	return nil
}

func (m *MemStorage) UpdateCounter(name string, value int64) error {
	m.Counter[name] += value
	return nil
}

type Storage interface {
	SetGauge(name string, value float64) error
	UpdateCounter(name string, value int64) error
}
