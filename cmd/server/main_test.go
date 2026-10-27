package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/subtotalstew/gometrics.git/internal/handler"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

var envKeys = []string{
	"ADDRESS", "STORE_INTERVAL", "FILE_STORAGE_PATH", "RESTORE",
	"DATABASE_DSN", "KEY", "CRYPTO_KEY", "AUDIT_FILE", "AUDIT_URL", "PPROF_ADDR",
}

// --- config ---

func TestLoadConfigFrom_Defaults(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"server"}
	defer func() { os.Args = oldArgs }()

	cfg, err := loadConfigFromArgs()
	if err != nil {
		t.Fatalf("loadConfigFromArgs() error = %v", err)
	}
	if cfg.addr != "localhost:8080" {
		t.Errorf("addr = %q, want localhost:8080", cfg.addr)
	}
	if cfg.storeInterval != 300 {
		t.Errorf("storeInterval = %d, want 300", cfg.storeInterval)
	}
	if cfg.filePath != "metrics-store.json" {
		t.Errorf("filePath = %q, want metrics-store.json", cfg.filePath)
	}
	if !cfg.restore {
		t.Error("restore = false, want true")
	}
	if cfg.databaseDSN != "" {
		t.Errorf("databaseDSN = %q, want empty", cfg.databaseDSN)
	}
	if cfg.cryptoKey != "" {
		t.Errorf("cryptoKey = %q, want empty", cfg.cryptoKey)
	}
}

func TestLoadConfigFrom_EnvOverridesFlags(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"server", "-a", "flagaddr:1234", "-i", "100"}
	defer func() { os.Args = oldArgs }()

	_ = os.Setenv("ADDRESS", "envaddr:5678")
	_ = os.Setenv("STORE_INTERVAL", "42")
	_ = os.Setenv("FILE_STORAGE_PATH", "/tmp/m.json")
	_ = os.Setenv("RESTORE", "false")
	_ = os.Setenv("DATABASE_DSN", "postgres://x")
	_ = os.Setenv("KEY", "k")
	_ = os.Setenv("CRYPTO_KEY", "/tmp/private.pem")
	_ = os.Setenv("AUDIT_FILE", "/tmp/audit.log")
	_ = os.Setenv("AUDIT_URL", "http://audit.example")
	_ = os.Setenv("PPROF_ADDR", "localhost:6060")

	cfg, err := loadConfigFromArgs()
	if err != nil {
		t.Fatalf("loadConfigFromArgs() error = %v", err)
	}
	if cfg.addr != "envaddr:5678" {
		t.Errorf("addr = %q, want envaddr:5678", cfg.addr)
	}
	if cfg.storeInterval != 42 {
		t.Errorf("storeInterval = %d, want 42", cfg.storeInterval)
	}
	if cfg.filePath != "/tmp/m.json" {
		t.Errorf("filePath = %q, want /tmp/m.json", cfg.filePath)
	}
	if cfg.restore {
		t.Error("restore = true, want false")
	}
	if cfg.databaseDSN != "postgres://x" {
		t.Errorf("databaseDSN = %q", cfg.databaseDSN)
	}
	if cfg.key != "k" {
		t.Errorf("key = %q, want k", cfg.key)
	}
	if cfg.cryptoKey != "/tmp/private.pem" {
		t.Errorf("cryptoKey = %q, want /tmp/private.pem", cfg.cryptoKey)
	}
	if cfg.auditFile != "/tmp/audit.log" {
		t.Errorf("auditFile = %q", cfg.auditFile)
	}
	if cfg.auditURL != "http://audit.example" {
		t.Errorf("auditURL = %q", cfg.auditURL)
	}
	if cfg.pprofAddr != "localhost:6060" {
		t.Errorf("pprofAddr = %q", cfg.pprofAddr)
	}
}

func TestLoadConfigFrom_InvalidStoreInterval(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"server"}
	defer func() { os.Args = oldArgs }()

	_ = os.Setenv("STORE_INTERVAL", "abc")
	if _, err := loadConfigFromArgs(); err == nil {
		t.Fatal("loadConfigFromArgs() = nil, want error for invalid STORE_INTERVAL")
	}
}

func TestLoadConfigFrom_InvalidRestore(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"server"}
	defer func() { os.Args = oldArgs }()

	_ = os.Setenv("RESTORE", "notabool")
	if _, err := loadConfigFromArgs(); err == nil {
		t.Fatal("loadConfigFromArgs() = nil, want error for invalid RESTORE")
	}
}

// TestLoadConfigFrom_CryptoKeyFlag проверяет флаг -crypto-key: путь до
// приватного ключа для расшифрования запросов агента.
func TestLoadConfigFrom_CryptoKeyFlag(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"server", "-crypto-key", "/tmp/flag-private.pem"}
	defer func() { os.Args = oldArgs }()

	cfg, err := loadConfigFromArgs()
	if err != nil {
		t.Fatalf("loadConfigFromArgs() error = %v", err)
	}
	if cfg.cryptoKey != "/tmp/flag-private.pem" {
		t.Errorf("cryptoKey = %q, want /tmp/flag-private.pem", cfg.cryptoKey)
	}
}

// --- buildRouter ---

func TestBuildRouter_AllRoutes(t *testing.T) {
	t.Parallel()

	h := handler.NewHandler(storage.NewMemStorage())
	r := buildRouter(h, "")

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/update"},
		{http.MethodPost, "/value"},
		{http.MethodPost, "/update/"},
		{http.MethodPost, "/value/"},
		{http.MethodPost, "/update/gauge/x/1"},
		{http.MethodGet, "/value/gauge/x"},
		{http.MethodGet, "/"},
		{http.MethodGet, "/ping"},
		{http.MethodPost, "/updates"},
		{http.MethodPost, "/updates/"},
	}
	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		// 404 = маршрут не зарегистрирован; 405 = метод не разрешён.
		if w.Code == http.StatusNotFound {
			t.Errorf("%s %s -> 404, route not registered", rt.method, rt.path)
		}
	}
}

func TestBuildRouter_WithKey(t *testing.T) {
	t.Parallel()

	h := handler.NewHandler(storage.NewMemStorage())
	r := buildRouter(h, "secret")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatal("GET / with key -> 404, route not registered")
	}
}

// --- сквозные сценарии через router ---

func TestRouter_UpdateAndRead(t *testing.T) {
	t.Parallel()

	mem := storage.NewMemStorage()
	h := handler.NewHandler(mem)
	r := buildRouter(h, "")

	// 1. Обновляем gauge через JSON.
	body := strings.NewReader(`{"id":"g1","type":"gauge","value":12.5}`)
	req := httptest.NewRequest(http.MethodPost, "/update", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update -> %d, want 200", w.Code)
	}

	// 2. Читаем gauge через path-URL.
	req = httptest.NewRequest(http.MethodGet, "/value/gauge/g1", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("read -> %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "12.5") {
		t.Errorf("body = %q, want contains 12.5", w.Body.String())
	}
}

func TestRouter_UpdatesBatch(t *testing.T) {
	t.Parallel()

	mem := storage.NewMemStorage()
	h := handler.NewHandler(mem)
	r := buildRouter(h, "")

	body := strings.NewReader(`[
		{"id":"g1","type":"gauge","value":1.5},
		{"id":"c1","type":"counter","delta":10}
	]`)
	req := httptest.NewRequest(http.MethodPost, "/updates", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("updates -> %d, want 200", w.Code)
	}

	if _, ok := mem.GetGauge("g1"); !ok {
		t.Error("g1 not saved via batch")
	}
	if _, ok := mem.GetCounter("c1"); !ok {
		t.Error("c1 not saved via batch")
	}
}

// --- утилиты ---

func saveEnv() map[string]string {
	saved := make(map[string]string, len(envKeys))
	for _, k := range envKeys {
		saved[k] = os.Getenv(k)
	}
	return saved
}

func restoreEnv(saved map[string]string) {
	for k, v := range saved {
		if v == "" {
			_ = os.Unsetenv(k)
		} else {
			_ = os.Setenv(k, v)
		}
	}
}

func clearEnv() {
	for _, k := range envKeys {
		_ = os.Unsetenv(k)
	}
}
