package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/subtotalstew/gometrics.git/internal/models"
)

func TestNewMemStorage(t *testing.T) {
	storage := NewMemStorage()

	if storage.gauge == nil {
		t.Error("Gauge map must be init")
	}

	if storage.counter == nil {
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

func TestMemStorage_UpdateBatch(t *testing.T) {
	t.Parallel()

	s := NewMemStorage()

	g1 := 1.5
	g2 := 2.5
	c1 := int64(10)
	c2 := int64(20)

	batch := []models.Metrics{
		{ID: "g1", MType: models.Gauge, Value: &g1},
		{ID: "g2", MType: models.Gauge, Value: &g2},
		{ID: "c1", MType: models.Counter, Delta: &c1},
		{ID: "c2", MType: models.Counter, Delta: &c2},
	}
	s.UpdateBatch(batch)

	// Gauge-метрики записались.
	if got, ok := s.GetGauge("g1"); !ok || got != g1 {
		t.Errorf("gauge g1 = (%v, %v), want (%v, true)", got, ok, g1)
	}
	if got, ok := s.GetGauge("g2"); !ok || got != g2 {
		t.Errorf("gauge g2 = (%v, %v), want (%v, true)", got, ok, g2)
	}

	// Counter-метрики прибавились.
	if got, ok := s.GetCounter("c1"); !ok || got != c1 {
		t.Errorf("counter c1 = (%v, %v), want (%v, true)", got, ok, c1)
	}
	if got, ok := s.GetCounter("c2"); !ok || got != c2 {
		t.Errorf("counter c2 = (%v, %v), want (%v, true)", got, ok, c2)
	}

	// Повторный вызов: counter-метрики должны удвоиться, gauge — перезаписаться.
	s.UpdateBatch(batch)
	if got, _ := s.GetCounter("c1"); got != c1*2 {
		t.Errorf("after second batch: counter c1 = %v, want %v", got, c1*2)
	}
	if got, _ := s.GetCounter("c2"); got != c2*2 {
		t.Errorf("after second batch: counter c2 = %v, want %v", got, c2*2)
	}
}

func TestRunPeriodicSave(t *testing.T) {
	// Не t.Parallel() — тест спит ~1 секунду, чтобы не тормозить остальные.
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.json")

	s := NewMemStorage()
	if err := s.SetGauge("g1", 1.5); err != nil {
		t.Fatalf("SetGauge: %v", err)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunPeriodicSave(s, path, 1, stop)
	}()

	// interval = 1 секунда, значит первый тик — через 1 секунду.
	// Ждём с запасом 1.5 секунды.
	time.Sleep(1500 * time.Millisecond)
	close(stop)
	<-done

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Error("file is empty")
	}
}
