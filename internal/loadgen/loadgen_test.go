package loadgen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/subtotalstew/gometrics.git/internal/handler"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	h := handler.NewHandler(storage.NewMemStorage())
	r := chi.NewRouter()
	r.Use(h.GzipMiddleware)
	r.Post("/update", h.UpdateJSONHandler)
	r.Post("/value", h.ValueJSONHandler)
	r.Post("/updates/", h.UpdatesJSONHandler)
	r.Get("/value/{type}/{name}", h.ValueHandler)
	r.Get("/", h.RootHandler)

	return httptest.NewServer(r)
}

func TestRun_SendsLoadAndCountsRequests(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	stats, err := Run(context.Background(), Config{
		Addr:        srv.URL,
		Workers:     4,
		Duration:    300 * time.Millisecond,
		SeedMetrics: 200,
		BatchSize:   20,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.SeedRequests != 10 {
		t.Errorf("SeedRequests = %d, want 10", stats.SeedRequests)
	}
	if stats.Requests == 0 {
		t.Fatal("нагрузка не сгенерирована: requests = 0")
	}
	if stats.Errors != 0 {
		t.Errorf("errors = %d, want 0", stats.Errors)
	}
	if stats.RootPage+stats.ValueJSON+stats.ValuePath+stats.UpdateBatch == 0 {
		t.Error("ни одного запроса по маршрутам")
	}
}

func TestRun_InvalidAddress(t *testing.T) {
	if _, err := Run(context.Background(), Config{Duration: time.Millisecond}); err == nil {
		t.Fatal("ожидалась ошибка при пустом адресе")
	}
}

func TestRun_UnreachableServer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	_, err := Run(context.Background(), Config{
		Addr:        url,
		Workers:     1,
		Duration:    50 * time.Millisecond,
		SeedMetrics: 10,
		BatchSize:   5,
	})
	if err == nil {
		t.Fatal("ожидалась ошибка при недоступном сервере")
	}
}

func TestRun_StopsAfterRequestLimit(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	stats, err := Run(context.Background(), Config{
		Addr:        srv.URL,
		Workers:     4,
		Requests:    200,
		SeedMetrics: 100,
		BatchSize:   10,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Requests < 200 {
		t.Fatalf("requests = %d, want >= 200", stats.Requests)
	}
	// Остановка асинхронная, поэтому допускаем небольшой «перелёт».
	if stats.Requests > 260 {
		t.Fatalf("requests = %d: нагрузка не остановилась по лимиту", stats.Requests)
	}
}

func TestMetricNameAndType(t *testing.T) {
	if got := metricName(7); got != "metric_7" {
		t.Errorf("metricName(7) = %q", got)
	}
	if got := metricType(0); got != "gauge" {
		t.Errorf("metricType(0) = %q", got)
	}
	if got := metricType(1); got != "counter" {
		t.Errorf("metricType(1) = %q", got)
	}

	batch := buildBatch(0, 4)
	if len(batch) != 4 {
		t.Fatalf("len(batch) = %d, want 4", len(batch))
	}
	if batch[0].Value == nil || batch[1].Delta == nil {
		t.Error("батч собран с неверными типами метрик")
	}
}

func TestConfigWithDefaults(t *testing.T) {
	cfg := Config{}.withDefaults()
	if cfg.Workers != 4 || cfg.Duration != 10*time.Second || cfg.SeedMetrics != 1000 || cfg.BatchSize != 100 {
		t.Fatalf("неожиданные значения по умолчанию: %+v", cfg)
	}
}

func TestRun_RespectsContextCancel(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stats, err := Run(ctx, Config{
		Addr:        srv.URL,
		Workers:     1,
		Duration:    10 * time.Second,
		SeedMetrics: 10,
		BatchSize:   5,
	})
	if err == nil && stats.Requests > 0 {
		t.Fatal("нагрузка продолжилась после отмены контекста")
	}
}
