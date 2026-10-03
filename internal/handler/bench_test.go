package handler

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	models "github.com/subtotalstew/gometrics.git/internal/model"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

const benchSeedMetrics = 5000

// benchNames возвращает набор имён метрик, похожий на то, что присылает агент:
// runtime-метрики + CPUutilizationN + TotalMemory/FreeMemory.
func benchNames(n int) []string {
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
		switch {
		case i < len(base):
			names = append(names, base[i])
		case i%2 == 0:
			names = append(names, fmt.Sprintf("CPUutilization%d", i))
		default:
			names = append(names, fmt.Sprintf("metric_%d", i))
		}
	}
	return names
}

func fillStorage(s *storage.MemStorage, n int) {
	for i, name := range benchNames(n) {
		_ = s.SetGauge(name, float64(i)*1.5)
		_ = s.UpdateCounter(name, int64(i%97))
	}
}

// benchPrepareRouter собирает полный стек middleware, как в cmd/server.
func benchPrepareRouter(key string) *chi.Mux {
	mem := storage.NewMemStorage()
	fillStorage(mem, benchSeedMetrics)

	h := NewHandler(mem)
	if key != "" {
		h.SetKey(key)
	}

	r := chi.NewRouter()
	if key != "" {
		r.Use(h.HashMiddleware)
	}
	r.Use(h.GzipMiddleware)
	r.Use(h.LoggingMiddleware)

	r.Post("/update", h.UpdateJSONHandler)
	r.Post("/value", h.ValueJSONHandler)
	r.Post("/update/{type}/{name}/{value}", h.UpdateHandler)
	r.Get("/value/{type}/{name}", h.ValueHandler)
	r.Get("/", h.RootHandler)
	r.Post("/updates/", h.UpdatesJSONHandler)

	return r
}

// benchBody — переиспользуемое тело запроса, чтобы бенчмарк измерял
// работу хендлеров, а не аллокации тестовой обвязки.
type benchBody struct{ r *bytes.Reader }

func (b *benchBody) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *benchBody) Close() error               { return nil }

func benchServe(b *testing.B, h http.Handler, method, target string, payload []byte, headers map[string]string) {
	b.Helper()

	req := httptest.NewRequest(method, target, nil)
	body := &benchBody{r: bytes.NewReader(payload)}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if payload != nil {
			body.r.Reset(payload)
			req.Body = body
		}
		w.Body.Reset()
		w.Code = http.StatusOK
		clear(w.Header())

		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			b.Fatalf("unexpected status %d", w.Code)
		}
	}
}

func benchJSONBatch(b *testing.B, count int) []byte {
	b.Helper()

	names := benchNames(count)
	metrics := make([]models.Metrics, 0, count)
	for i, name := range names {
		if i%2 == 0 {
			v := float64(i) * 1.5
			metrics = append(metrics, models.Metrics{ID: name, MType: models.Gauge, Value: &v})
		} else {
			d := int64(i)
			metrics = append(metrics, models.Metrics{ID: name, MType: models.Counter, Delta: &d})
		}
	}

	data, err := json.Marshal(metrics)
	if err != nil {
		b.Fatal(err)
	}
	return data
}

func benchGzip(b *testing.B, data []byte) []byte {
	b.Helper()

	var buf bytes.Buffer
	buf.Grow(len(data) / 2)
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		b.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		b.Fatal(err)
	}
	return buf.Bytes()
}

// initBenchLogger отключает вывод логов в stderr, сохраняя при этом весь путь
// выполнения LoggingMiddleware (zerolog сериализует событие в io.Discard).
func initBenchLogger(b *testing.B) {
	b.Helper()
	prev := log.Logger
	log.Logger = zerolog.New(io.Discard)
	b.Cleanup(func() { log.Logger = prev })
}

func BenchmarkRouterUpdateJSON(b *testing.B) {
	initBenchLogger(b)
	router := benchPrepareRouter("")

	payload := []byte(`{"id":"HeapAlloc","type":"gauge","value":1234567.89}`)
	benchServe(b, router, http.MethodPost, "/update", payload,
		map[string]string{"Content-Type": "application/json"})
}

func BenchmarkRouterValueJSON(b *testing.B) {
	initBenchLogger(b)
	router := benchPrepareRouter("")

	payload := []byte(`{"id":"HeapAlloc","type":"gauge"}`)
	benchServe(b, router, http.MethodPost, "/value", payload,
		map[string]string{"Content-Type": "application/json"})
}

func BenchmarkRouterUpdatePath(b *testing.B) {
	initBenchLogger(b)
	router := benchPrepareRouter("")
	benchServe(b, router, http.MethodPost, "/update/gauge/HeapAlloc/1234567.89", nil, nil)
}

func BenchmarkRouterValuePath(b *testing.B) {
	initBenchLogger(b)
	router := benchPrepareRouter("")
	benchServe(b, router, http.MethodGet, "/value/gauge/HeapAlloc", nil, nil)
}

func BenchmarkRouterRoot5000Metrics(b *testing.B) {
	initBenchLogger(b)
	router := benchPrepareRouter("")
	benchServe(b, router, http.MethodGet, "/", nil, nil)
}

func BenchmarkRouterUpdatesJSON_100(b *testing.B) {
	initBenchLogger(b)
	router := benchPrepareRouter("")

	body := benchJSONBatch(b, 100)
	benchServe(b, router, http.MethodPost, "/updates/", body,
		map[string]string{"Content-Type": "application/json"})
}

func BenchmarkRouterUpdatesJSONGzip_100(b *testing.B) {
	initBenchLogger(b)
	router := benchPrepareRouter("")

	body := benchGzip(b, benchJSONBatch(b, 100))
	benchServe(b, router, http.MethodPost, "/updates/", body,
		map[string]string{"Content-Type": "application/json", "Content-Encoding": "gzip"})
}

func BenchmarkRouterUpdateJSONWithHash(b *testing.B) {
	initBenchLogger(b)
	router := benchPrepareRouter("bench-secret-key")

	payload := []byte(`{"id":"HeapAlloc","type":"gauge","value":1234567.89}`)
	benchServe(b, router, http.MethodPost, "/update", payload,
		map[string]string{"Content-Type": "application/json"})
}

func BenchmarkLoggingMiddlewareOnly(b *testing.B) {
	initBenchLogger(b)

	h := NewHandler(storage.NewMemStorage())
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	payload := []byte(`{"id":"HeapAlloc","type":"gauge","value":1234567.89}`)
	benchServe(b, h.LoggingMiddleware(next), http.MethodPost, "/update", payload, nil)
}
