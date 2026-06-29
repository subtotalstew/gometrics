package storage

import (
	"testing"
)

func TestNewMemStorage(t *testing.T) {
	storage := NewMemStorage()

	if storage.Gauge == nil {
		t.Error("Gauge map must be init")
	}

	if storage.Counter == nil {
		t.Error("Counter map must be init")
	}
}

func TestSetGauge(t *testing.T) {
	storage := NewMemStorage()

	tests := []struct {
		name  string
		value float64
	}{
		{"temperature", 25.5},
		{"pressure", 1013.25},
		{"humidity", 65.0},
		{"zero", 0.0},
		{"negative", -10.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := storage.SetGauge(tt.name, tt.value)
			if err != nil {
				t.Errorf("SetGauge() error = %v, want nil", err)
			}
			got, exists := storage.GetGauge(tt.name)
			if !exists {
				t.Errorf("GetGauge() exists = false, want true")
			}
			if got != tt.value {
				t.Errorf("GetGauge() = %v, want %v", got, tt.value)
			}
		})
	}
}

func TestUpdateCounter(t *testing.T) {
	storage := NewMemStorage()

	tests := []struct {
		name     string
		values   []int64
		expected int64
	}{
		{"requests", []int64{10, 5, 3}, 18},
		{"visits", []int64{1, 1, 1, 1}, 4},
		{"errors", []int64{0, 0, 0}, 0},
		{"negative", []int64{-5, 10, -3}, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Обновляем счетчик несколько раз
			for _, val := range tt.values {
				err := storage.UpdateCounter(tt.name, val)
				if err != nil {
					t.Errorf("UpdateCounter() error = %v, want nil", err)
				}
			}

			got, exists := storage.GetCounter(tt.name)
			if !exists {
				t.Errorf("GetCounter() exists = false, want true")
			}
			if got != tt.expected {
				t.Errorf("GetCounter() = %v, want %v", got, tt.expected)
			}
		})
	}
}
