// Package storage содержит реализации хранилища метрик (память, файл,
// PostgreSQL) и общий интерфейс Storage, которым пользуются хендлеры.
package storage

import (
	"maps"
	"sync"

	models "github.com/subtotalstew/gometrics.git/internal/model"
)

// MemStorage — потокобезопасное хранилище метрик в оперативной памяти.
// Gauge-метрики хранятся как есть (последнее записанное значение), а
// counter-метрики — как накопленная сумма всех переданных дельт.
type MemStorage struct {
	mu      sync.RWMutex
	gauge   map[string]float64
	counter map[string]int64
}

// NewMemStorage создаёт пустое хранилище в памяти.
func NewMemStorage() *MemStorage {
	return &MemStorage{
		gauge:   make(map[string]float64),
		counter: make(map[string]int64),
	}
}

// SetGauge записывает значение gauge-метрики с именем name,
// перезатирая предыдущее значение.
func (m *MemStorage) SetGauge(name string, value float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauge[name] = value
	return nil
}

// UpdateCounter увеличивает counter-метрику с именем name на value.
// Значение value может быть отрицательным.
func (m *MemStorage) UpdateCounter(name string, value int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counter[name] += value
	return nil
}

// GetCounter возвращает текущее значение counter-метрики и признак того,
// что метрика есть в хранилище.
func (m *MemStorage) GetCounter(name string) (int64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.counter[name]
	return value, ok
}

// GetGauge возвращает текущее значение gauge-метрики и признак того,
// что метрика есть в хранилище.
func (m *MemStorage) GetGauge(name string) (float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.gauge[name]
	return value, ok
}

// GetAllMetrics возвращает копии всех gauge- и counter-метрик. Копии
// отдаются для того, чтобы вызывающий код мог читать карты без блокировки.
func (m *MemStorage) GetAllMetrics() (map[string]float64, map[string]int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return maps.Clone(m.gauge), maps.Clone(m.counter)
}

// UpdateBatch применяет набор метрик одной критической секцией:
// gauge-метрики перезаписываются, counter-метрики суммируются.
// Метрики с nil-значением (Value/Delta) пропускаются.
func (m *MemStorage) UpdateBatch(metrics []models.Metrics) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, mt := range metrics {
		switch mt.MType {
		case models.Gauge:
			if mt.Value != nil {
				m.gauge[mt.ID] = *mt.Value
			}
		case models.Counter:
			if mt.Delta != nil {
				m.counter[mt.ID] += *mt.Delta
			}
		}
	}
	return nil
}

// Storage — абстракция хранилища метрик, которой пользуются HTTP-хендлеры.
// Реализации: память (MemStorage), PostgreSQL (DBStorage).
type Storage interface {
	// SetGauge записывает значение gauge-метрики.
	SetGauge(name string, value float64) error
	// UpdateCounter увеличивает counter-метрику на value.
	UpdateCounter(name string, value int64) error
	// GetCounter возвращает значение counter-метрики и признак её наличия.
	GetCounter(name string) (int64, bool)
	// GetGauge возвращает значение gauge-метрики и признак её наличия.
	GetGauge(name string) (float64, bool)
	// GetAllMetrics возвращает все gauge- и counter-метрики.
	GetAllMetrics() (map[string]float64, map[string]int64)
	// UpdateBatch применяет набор метрик (используется эндпоинтом /updates/).
	UpdateBatch(metrics []models.Metrics) error
}
