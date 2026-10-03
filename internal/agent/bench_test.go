package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

func benchAgent(serverURL string) *Agent {
	return NewAgent(serverURL, 2, 10, 4, "")
}

func BenchmarkCollectorUpdateMetrics(b *testing.B) {
	c := NewCollector()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.UpdateMetrics()
	}
}

func BenchmarkCollectorGetGauge(b *testing.B) {
	c := NewCollector()
	c.UpdateMetrics()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.GetGauge()
	}
}

func BenchmarkCollectorGetCounter(b *testing.B) {
	c := NewCollector()
	c.UpdateMetrics()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.GetCounter()
	}
}

func BenchmarkAgentEnqueueMetrics(b *testing.B) {
	a := benchAgent("http://127.0.0.1:0")
	a.collector.UpdateMetrics()
	a.collector.UpdatePSUtilMetrics()

	jobs := make(chan []models.Metrics, 1)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.enqueueMetrics(ctx, jobs)
		<-jobs
	}
}

func BenchmarkAgentSendMetricsBatch(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := benchAgent(srv.URL)
	a.collector.UpdateMetrics()
	a.collector.UpdatePSUtilMetrics()

	jobs := make(chan []models.Metrics, 1)
	a.enqueueMetrics(context.Background(), jobs)
	batch := <-jobs

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.sendMetricsBatch(batch)
	}
}

func BenchmarkAgentSendMetricsBatch_1000(b *testing.B) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := benchAgent(srv.URL)
	batch := benchBatchMetrics(1000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.sendMetricsBatch(batch)
	}
}

func benchBatchMetrics(n int) []models.Metrics {
	metrics := make([]models.Metrics, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("metric_%d", i)
		if i%2 == 0 {
			v := float64(i) * 1.5
			metrics = append(metrics, models.Metrics{ID: name, MType: models.Gauge, Value: &v})
		} else {
			d := int64(i)
			metrics = append(metrics, models.Metrics{ID: name, MType: models.Counter, Delta: &d})
		}
	}
	return metrics
}
