package agent

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/subtotalstew/gometrics.git/internal/crypto"
	"github.com/subtotalstew/gometrics.git/internal/hash"
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

// TestCheckResponse проверяет, что неуспешные ответы сервера становятся
// ошибкой отправки: иначе батч, отклонённый сервером из-за несовпадения
// ключей, выглядел бы успешно отправленным.
func TestCheckResponse(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "200", status: http.StatusOK},
		{name: "204", status: http.StatusNoContent},
		{name: "400", status: http.StatusBadRequest, wantErr: true},
		{name: "401", status: http.StatusUnauthorized, wantErr: true},
		{name: "500", status: http.StatusInternalServerError, wantErr: true},
		{name: "503", status: http.StatusServiceUnavailable, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkResponse(&http.Response{StatusCode: tt.status})
			if tt.wantErr && err == nil {
				t.Fatalf("checkResponse(%d) = nil, want error", tt.status)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("checkResponse(%d) = %v, want nil", tt.status, err)
			}
		})
	}
}

// TestEnqueueMetrics проверяет, что метрики собираются в батч и
// уходят в канал.
func TestEnqueueMetrics(t *testing.T) {
	a := NewAgent("http://127.0.0.1:1", 1, 1, 1, "")
	a.collector.UpdateMetrics()

	jobs := make(chan []models.Metrics, 1)
	a.enqueueMetrics(jobs)

	select {
	case batch := <-jobs:
		if len(batch) == 0 {
			t.Error("enqueued empty batch")
		}
	case <-time.After(time.Second):
		t.Fatal("no batch enqueued within 1s")
	}
}

// TestEnqueueMetrics_NotDropped проверяет, что батч не теряется при
// остановке агента: отправка в очередь не прерывается контекстом, а ждёт
// воркера, который его заберёт.
func TestEnqueueMetrics_NotDropped(t *testing.T) {
	a := NewAgent("http://127.0.0.1:1", 1, 1, 1, "")
	a.collector.UpdateMetrics()

	jobs := make(chan []models.Metrics) // без буфера, читателя ещё нет

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.enqueueMetrics(jobs)
	}()

	select {
	case <-done:
		t.Fatal("enqueueMetrics returned before a worker took the batch")
	case <-time.After(50 * time.Millisecond):
	}

	select {
	case batch := <-jobs:
		if len(batch) == 0 {
			t.Error("dropped batch is empty")
		}
	case <-time.After(time.Second):
		t.Fatal("no batch enqueued")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("enqueueMetrics blocked after the batch was taken")
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

// --- асимметричное шифрование ---

// TestSetCryptoKey проверяет загрузку публичного ключа и ошибки на пустом
// и несуществующем пути.
func TestSetCryptoKey(t *testing.T) {
	a := NewAgent("http://127.0.0.1:1", 1, 1, 1, "")

	if err := a.SetCryptoKey(""); err != nil {
		t.Fatalf("SetCryptoKey(\"\") = %v, want nil", err)
	}
	if a.pubKey != nil {
		t.Error("pubKey != nil: пустой путь должен отключать шифрование")
	}

	if err := a.SetCryptoKey(filepath.Join(t.TempDir(), "absent.pem")); err == nil {
		t.Fatal("SetCryptoKey(absent) = nil, want error")
	}
	if a.pubKey != nil {
		t.Error("pubKey установлен после ошибки загрузки ключа")
	}

	priv := generateTestKey(t)
	if err := a.SetCryptoKey(writePublicKeyFile(t, &priv.PublicKey)); err != nil {
		t.Fatalf("SetCryptoKey(valid) = %v, want nil", err)
	}
	if a.pubKey == nil {
		t.Fatal("pubKey == nil после загрузки корректного ключа")
	}
}

// TestSendMetricsBatch_Encrypted проверяет, что при заданном ключе тело
// запроса уходит зашифрованным, подпись считается по шифртексту, а сервер
// восстанавливает исходный батч приватным ключом.
func TestSendMetricsBatch_Encrypted(t *testing.T) {
	priv := generateTestKey(t)

	var (
		mu       sync.Mutex
		raw      []byte
		head     http.Header
		encoding string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		raw = body
		head = r.Header.Clone()
		encoding = r.Header.Get("Content-Encoding")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := NewAgent(srv.URL, 1, 1, 1, "secret")
	if err := a.SetCryptoKey(writePublicKeyFile(t, &priv.PublicKey)); err != nil {
		t.Fatalf("SetCryptoKey: %v", err)
	}

	value := 1.5
	batch := []models.Metrics{{ID: "g1", MType: models.Gauge, Value: &value}}
	a.sendMetricsBatch(batch)

	mu.Lock()
	defer mu.Unlock()

	if len(raw) == 0 {
		t.Fatal("сервер не получил тело запроса")
	}
	if encoding != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", encoding)
	}
	// В том, что ушло по сети, не должно быть открытого JSON.
	if bytes.Contains(raw, []byte(`"id"`)) || bytes.Contains(raw, []byte("g1")) {
		t.Error("тело запроса не зашифровано: в нём виден открытый JSON")
	}
	// Подпись должна считаться по шифртексту, иначе сервер её не проверит.
	if got, want := head.Get("HashSHA256"), hash.Compute(raw, "secret"); got != want {
		t.Errorf("HashSHA256 = %q, want %q (подпись по шифртексту)", got, want)
	}

	decrypted, err := crypto.Decrypt(priv, raw)
	if err != nil {
		t.Fatalf("crypto.Decrypt: %v", err)
	}

	gr, err := gzip.NewReader(bytes.NewReader(decrypted))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer func() { _ = gr.Close() }()

	plain, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}

	var decoded []models.Metrics
	if err := json.Unmarshal(plain, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(decoded) != 1 || decoded[0].ID != "g1" {
		t.Fatalf("decoded = %+v, want один gauge g1", decoded)
	}
	if decoded[0].Value == nil || *decoded[0].Value != value {
		t.Errorf("decoded value = %v, want %v", decoded[0].Value, value)
	}
}

// generateTestKey создаёт RSA-ключ для тестов шифрования.
func generateTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return priv
}

// writePublicKeyFile сохраняет публичный ключ в PEM-файл (PKIX) и возвращает
// путь к нему.
func writePublicKeyFile(t *testing.T, pub *rsa.PublicKey) string {
	t.Helper()

	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("x509.MarshalPKIXPublicKey: %v", err)
	}

	path := filepath.Join(t.TempDir(), "public.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	return path
}
