package agent

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/subtotalstew/gometrics.git/internal/models"
)

// TestSendMetricsBatch_Success проверяет отправку батча на сервер.
func TestSendMetricsBatch_Success(t *testing.T) {
	var (
		mu   sync.Mutex
		got  []byte
		head http.Header
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		// Проверяем, что тело пришло gzip.
		if r.Header.Get("Content-Encoding") != "gzip" {
			t.Errorf("Content-Encoding = %q, want gzip", r.Header.Get("Content-Encoding"))
		}
		gr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("gzip.NewReader: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer func() { _ = gr.Close() }()
		body, _ := io.ReadAll(gr)

		mu.Lock()
		got = body
		head = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := NewAgent(srv.URL, 1, 1, 1, "")
	v := 1.5
	batch := []models.Metrics{
		{ID: "g1", MType: models.Gauge, Value: &v},
	}
	a.sendMetricsBatch(batch)

	mu.Lock()
	defer mu.Unlock()
	if got == nil {
		t.Fatal("server did not receive body")
	}
	if head.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", head.Get("Content-Type"))
	}

	var decoded []models.Metrics
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded) != 1 || decoded[0].ID != "g1" {
		t.Errorf("decoded = %+v, want one metric with ID=g1", decoded)
	}
}

// TestSendMetricsBatch_ServerError проверяет, что 5xx не паникует.
func TestSendMetricsBatch_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	a := NewAgent(srv.URL, 1, 1, 1, "")
	v := 1.0
	a.sendMetricsBatch([]models.Metrics{{ID: "g", MType: models.Gauge, Value: &v}})
}

// TestEnqueueMetrics проверяет, что метрики собираются в батч и
// уходят в канал.
func TestEnqueueMetrics(t *testing.T) {
	a := NewAgent("http://127.0.0.1:1", 1, 1, 1, "")
	a.collector.UpdateMetrics()

	jobs := make(chan []models.Metrics, 1)
	a.enqueueMetrics(context.Background(), jobs)

	select {
	case batch := <-jobs:
		if len(batch) == 0 {
			t.Error("enqueued empty batch")
		}
	case <-time.After(time.Second):
		t.Fatal("no batch enqueued within 1s")
	}
}

// TestEnqueueMetrics_ContextCancelled проверяет, что при отменённом
// контексте enqueueMetrics не блокируется.
func TestEnqueueMetrics_ContextCancelled(t *testing.T) {
	a := NewAgent("http://127.0.0.1:1", 1, 1, 1, "")
	a.collector.UpdateMetrics()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // сразу отменён

	jobs := make(chan []models.Metrics) // без буфера, никто не читает

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.enqueueMetrics(ctx, jobs)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("enqueueMetrics blocked on cancelled context")
	}
}

// TestWorker_SendsBatch проверяет, что worker читает из канала
// и вызывает sendMetricsBatch.
func TestWorker_SendsBatch(t *testing.T) {
	called := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case called <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := NewAgent(srv.URL, 1, 1, 1, "")

	jobs := make(chan []models.Metrics, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go a.worker(0, jobs, &wg)

	v := 1.0
	jobs <- []models.Metrics{{ID: "g", MType: models.Gauge, Value: &v}}
	close(jobs)
	wg.Wait()

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Error("server was not called by worker")
	}
}

// TestSendMetricJSON проверяет отправку одиночной метрики через /update.
func TestSendMetricJSON(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := NewAgent(srv.URL, 1, 1, 1, "")
	v := 3.14
	a.sendMetricJSON(srv.Client(), models.Metrics{ID: "g", MType: models.Gauge, Value: &v})

	if !called {
		t.Error("server was not called")
	}
}

// TestSendMetricJSON_Non200 проверяет, что не-200 не роняет функцию.
func TestSendMetricJSON_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	a := NewAgent(srv.URL, 1, 1, 1, "")
	v := 1.0
	a.sendMetricJSON(srv.Client(), models.Metrics{ID: "g", MType: models.Gauge, Value: &v})
}

// TestUpdatePSUtilMetrics проверяет сбор системных метрик.
// На CI/cross-platform некоторые значения могут быть 0 —
// главное, что функция не падает.
func TestUpdatePSUtilMetrics(t *testing.T) {
	c := NewCollector()
	if err := c.UpdatePSUtilMetrics(); err != nil {
		t.Fatalf("UpdatePSUtilMetrics() error = %v", err)
	}
	gauges := c.GetGauge()
	if _, ok := gauges["TotalMemory"]; !ok {
		t.Error("TotalMemory not collected")
	}
	if _, ok := gauges["FreeMemory"]; !ok {
		t.Error("FreeMemory not collected")
	}
}

// TestAgent_Run_GracefulShutdown проверяет, что Run завершается
// по сигналу (в тесте — через SIGINT).
func TestAgent_Run_GracefulShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported on Windows")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// pollInterval=1, reportInterval=1, rateLimit=1 — минимальные интервалы.
	a := NewAgent(srv.URL, 1, 1, 1, "")

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Run()
	}()

	// Даём агенту поработать немного.
	time.Sleep(300 * time.Millisecond)

	// Отправляем SIGINT текущему процессу.
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Agent.Run did not stop after SIGINT")
	}
}

func TestCollector_Reset(t *testing.T) {
	t.Parallel()

	c := NewCollector()
	c.UpdateMetrics()
	c.UpdatePSUtilMetrics()

	if len(c.GetGauge()) == 0 {
		t.Fatal("gauge should not be empty before reset")
	}
	if len(c.GetCounter()) == 0 {
		t.Fatal("counter should not be empty before reset")
	}

	c.Reset()

	if got := len(c.GetGauge()); got != 0 {
		t.Errorf("after Reset: gauge len = %d, want 0", got)
	}
	if got := len(c.GetCounter()); got != 0 {
		t.Errorf("after Reset: counter len = %d, want 0", got)
	}

	var nilCollector *Collector
	nilCollector.Reset() // не должно паниковать
}
