package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	models "github.com/subtotalstew/gometrics.git/internal/model"
)

// TestMain отключает вывод логов, чтобы бенчмарки измеряли код,
// а не запись в stderr.
func TestMain(m *testing.M) {
	log.Logger = zerolog.Nop()
	os.Exit(m.Run())
}

// benchMetricNames формирует реалистичный набор имён метрик
// (runtime-метрики агента + нумерованные серии), чтобы бенчмарки
// работали не на пустом словаре.
func benchMetricNames(n int) []string {
	base := []string{
		"Alloc", "BuckHashSys", "Frees", "GCCPUFraction", "GCSys",
		"HeapAlloc", "HeapIdle", "HeapInuse", "HeapObjects", "HeapReleased",
		"HeapSys", "LastGC", "Lookups", "MCacheInuse", "MCacheSys",
		"MSpanInuse", "MSpanSys", "Mallocs", "NextGC", "NumForcedGC",
		"NumGC", "OtherSys", "PauseTotalNs", "StackInuse", "StackSys",
		"Sys", "TotalAlloc", "RandomValue", "TotalMemory", "FreeMemory",
	}

	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if i < len(base) {
			names = append(names, base[i])
			continue
		}
		names = append(names, fmt.Sprintf("metric_%d", i))
	}
	return names
}

func fillMemStorage(s *MemStorage, n int) {
	for i, name := range benchMetricNames(n) {
		_ = s.SetGauge(name, float64(i)*1.5)
		_ = s.UpdateCounter(name, int64(i%97))
	}
}

func benchBatch(n int) []models.Metrics {
	batch := make([]models.Metrics, 0, n)
	for i, name := range benchMetricNames(n) {
		if i%2 == 0 {
			v := float64(i) * 3.14
			batch = append(batch, models.Metrics{ID: name, MType: models.Gauge, Value: &v})
		} else {
			d := int64(i)
			batch = append(batch, models.Metrics{ID: name, MType: models.Counter, Delta: &d})
		}
	}
	return batch
}

func BenchmarkMemStorageSetGauge(b *testing.B) {
	s := NewMemStorage()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.SetGauge("HeapAlloc", float64(i))
	}
}

func BenchmarkMemStorageUpdateCounter(b *testing.B) {
	s := NewMemStorage()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.UpdateCounter("PollCount", 1)
	}
}

func BenchmarkMemStorageGetGauge(b *testing.B) {
	s := NewMemStorage()
	fillMemStorage(s, 100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.GetGauge("HeapAlloc")
	}
}

func BenchmarkMemStorageGetAllMetrics100(b *testing.B) {
	s := NewMemStorage()
	fillMemStorage(s, 100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.GetAllMetrics()
	}
}

func BenchmarkMemStorageGetAllMetrics5000(b *testing.B) {
	s := NewMemStorage()
	fillMemStorage(s, 5000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.GetAllMetrics()
	}
}

func BenchmarkMemStorageUpdateBatch100(b *testing.B) {
	s := NewMemStorage()
	batch := benchBatch(100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.UpdateBatch(batch)
	}
}

func BenchmarkSaveToFile(b *testing.B) {
	s := NewMemStorage()
	fillMemStorage(s, 5000)
	path := filepath.Join(b.TempDir(), "metrics-store.json")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := SaveToFile(s, path); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadFromFile(b *testing.B) {
	src := NewMemStorage()
	fillMemStorage(src, 5000)

	path := filepath.Join(b.TempDir(), "metrics-store.json")
	if err := SaveToFile(src, path); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst := NewMemStorage()
		if err := LoadFromFile(dst, path); err != nil {
			b.Fatal(err)
		}
	}
}
