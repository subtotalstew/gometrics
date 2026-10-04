// Package models содержит транспортные структуры и константы типов метрик,
// общие для сервера и агента.
package models

// Допустимые значения поля Metrics.MType.
const (
	// Counter — тип метрики-счётчика: значение накапливается (дельта).
	Counter = "counter"
	// Gauge — тип метрики-датчика: значение перезаписывается целиком.
	Gauge = "gauge"
)

// Metrics — метрика в JSON-представлении, используется эндпоинтами
// /update, /value и /updates/ и файловым хранилищем.
//
// NOTE: Не усложняем пример, вводя иерархическую вложенность структур.
// Органичиваясь плоской моделью.
// Delta и Value объявлены через указатели,
// что бы отличать значение "0", от не заданного значения
// и соответственно не кодировать в структуру.
type Metrics struct {
	// ID — имя метрики.
	ID string `json:"id"`
	// MType — тип метрики: Gauge или Counter.
	MType string `json:"type"`
	// Delta — приращение counter-метрики (nil для gauge).
	Delta *int64 `json:"delta,omitempty"`
	// Value — значение gauge-метрики (nil для counter).
	Value *float64 `json:"value,omitempty"`
	// Hash — HMAC-SHA256 подписи метрики, если задан ключ подписи.
	Hash string `json:"hash,omitempty"`
}
