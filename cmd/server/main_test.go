package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subtotalstew/gometrics.git/internal/handler"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

var envKeys = []string{
	"CONFIG", "ADDRESS", "STORE_INTERVAL", "FILE_STORAGE_PATH", "RESTORE",
	"DATABASE_DSN", "KEY", "CRYPTO_KEY", "AUDIT_FILE", "AUDIT_URL", "PPROF_ADDR",
	"TRUSTED_SUBNET", "GRPC_ADDRESS", "GRPC_TLS_CERT", "GRPC_TLS_KEY",
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
	if cfg.trustedSubnet != "" {
		t.Errorf("trustedSubnet = %q, want empty", cfg.trustedSubnet)
	}
	if cfg.grpcAddr != "" {
		t.Errorf("grpcAddr = %q, want empty (gRPC disabled)", cfg.grpcAddr)
	}
	if cfg.grpcTLSCert != "" || cfg.grpcTLSKey != "" {
		t.Errorf("grpc TLS = %q, %q; want empty", cfg.grpcTLSCert, cfg.grpcTLSKey)
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
	_ = os.Setenv("TRUSTED_SUBNET", "192.168.0.0/16")
	_ = os.Setenv("GRPC_ADDRESS", "localhost:9090")
	_ = os.Setenv("GRPC_TLS_CERT", "/tmp/grpc.crt")
	_ = os.Setenv("GRPC_TLS_KEY", "/tmp/grpc.key")

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
	if cfg.trustedSubnet != "192.168.0.0/16" {
		t.Errorf("trustedSubnet = %q, want 192.168.0.0/16", cfg.trustedSubnet)
	}
	if cfg.grpcAddr != "localhost:9090" {
		t.Errorf("grpcAddr = %q, want localhost:9090", cfg.grpcAddr)
	}
	if cfg.grpcTLSCert != "/tmp/grpc.crt" || cfg.grpcTLSKey != "/tmp/grpc.key" {
		t.Errorf("grpc TLS = %q, %q", cfg.grpcTLSCert, cfg.grpcTLSKey)
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

// TestLoadConfigFrom_FileStoreIntervalSubSecond проверяет, что субсекундный
// store_interval из файла отклоняется: 0 у сервера означает синхронное
// сохранение, поэтому "300ms" не должно молча округляться до нуля.
func TestLoadConfigFrom_FileStoreIntervalSubSecond(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	path := writeConfigFile(t, `{"store_interval": "300ms"}`)

	oldArgs := os.Args
	os.Args = []string{"server", "-c", path}
	defer func() { os.Args = oldArgs }()

	_, err := loadConfigFromArgs()
	if err == nil {
		t.Fatal("loadConfigFromArgs() = nil, want error for sub-second store_interval")
	}
	if !strings.Contains(err.Error(), "store_interval") {
		t.Errorf("error = %q, want mention of store_interval", err)
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

// TestLoadConfigFrom_TrustedSubnetFlag проверяет флаг -t: строка CIDR
// доверенной подсети, из которой агенту разрешено присылать метрики.
func TestLoadConfigFrom_TrustedSubnetFlag(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"server", "-t", "10.0.0.0/8"}
	defer func() { os.Args = oldArgs }()

	cfg, err := loadConfigFromArgs()
	if err != nil {
		t.Fatalf("loadConfigFromArgs() error = %v", err)
	}
	if cfg.trustedSubnet != "10.0.0.0/8" {
		t.Errorf("trustedSubnet = %q, want 10.0.0.0/8", cfg.trustedSubnet)
	}
}

// TestLoadConfigFrom_GRPCAddressFlag проверяет флаг -g (и его алиас
// -grpc-address): адрес, на котором сервер поднимает gRPC.
func TestLoadConfigFrom_GRPCAddressFlag(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"server", "-g", "localhost:9090", "-grpc-tls-cert", "/tmp/cert.pem", "-grpc-tls-key", "/tmp/key.pem"}
	cfg, err := loadConfigFromArgs()
	if err != nil {
		t.Fatalf("loadConfigFromArgs() error = %v", err)
	}
	if cfg.grpcAddr != "localhost:9090" {
		t.Errorf("grpcAddr = %q, want localhost:9090", cfg.grpcAddr)
	}
	if cfg.grpcTLSCert != "/tmp/cert.pem" || cfg.grpcTLSKey != "/tmp/key.pem" {
		t.Errorf("grpc TLS = %q, %q; want /tmp/cert.pem, /tmp/key.pem", cfg.grpcTLSCert, cfg.grpcTLSKey)
	}

	os.Args = []string{"server", "-grpc-address", "localhost:9091"}
	cfg, err = loadConfigFromArgs()
	if err != nil {
		t.Fatalf("loadConfigFromArgs() error = %v", err)
	}
	if cfg.grpcAddr != "localhost:9091" {
		t.Errorf("grpcAddr = %q, want localhost:9091", cfg.grpcAddr)
	}
}

// --- файл конфигурации ---

// TestLoadConfigFrom_FileConfig проверяет, что все опции сервера
// читаются из JSON-файла, заданного флагом -c.
func TestLoadConfigFrom_FileConfig(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	path := writeConfigFile(t, `{
		"address": "file:9999",
		"restore": false,
		"store_interval": "5s",
		"store_file": "/tmp/file.db",
		"database_dsn": "postgres://file",
		"crypto_key": "/tmp/file.pem",
		"key": "filekey",
		"audit_file": "/tmp/file-audit.log",
		"audit_url": "http://file-audit",
		"pprof_addr": "localhost:7070",
		"trusted_subnet": "172.16.0.0/12",
		"grpc_address": "localhost:9092",
		"grpc_tls_cert": "/tmp/file-grpc.crt",
		"grpc_tls_key": "/tmp/file-grpc.key"
	}`)

	oldArgs := os.Args
	os.Args = []string{"server", "-c", path}
	defer func() { os.Args = oldArgs }()

	cfg, err := loadConfigFromArgs()
	if err != nil {
		t.Fatalf("loadConfigFromArgs() error = %v", err)
	}
	if cfg.addr != "file:9999" {
		t.Errorf("addr = %q, want file:9999", cfg.addr)
	}
	if cfg.storeInterval != 5 {
		t.Errorf("storeInterval = %d, want 5 (from 5s)", cfg.storeInterval)
	}
	if cfg.filePath != "/tmp/file.db" {
		t.Errorf("filePath = %q, want /tmp/file.db", cfg.filePath)
	}
	if cfg.restore {
		t.Error("restore = true, want false")
	}
	if cfg.databaseDSN != "postgres://file" {
		t.Errorf("databaseDSN = %q", cfg.databaseDSN)
	}
	if cfg.cryptoKey != "/tmp/file.pem" {
		t.Errorf("cryptoKey = %q", cfg.cryptoKey)
	}
	if cfg.key != "filekey" {
		t.Errorf("key = %q", cfg.key)
	}
	if cfg.auditFile != "/tmp/file-audit.log" {
		t.Errorf("auditFile = %q", cfg.auditFile)
	}
	if cfg.auditURL != "http://file-audit" {
		t.Errorf("auditURL = %q", cfg.auditURL)
	}
	if cfg.pprofAddr != "localhost:7070" {
		t.Errorf("pprofAddr = %q", cfg.pprofAddr)
	}
	if cfg.trustedSubnet != "172.16.0.0/12" {
		t.Errorf("trustedSubnet = %q, want 172.16.0.0/12", cfg.trustedSubnet)
	}
	if cfg.grpcAddr != "localhost:9092" {
		t.Errorf("grpcAddr = %q, want localhost:9092", cfg.grpcAddr)
	}
	if cfg.grpcTLSCert != "/tmp/file-grpc.crt" || cfg.grpcTLSKey != "/tmp/file-grpc.key" {
		t.Errorf("grpc TLS = %q, %q", cfg.grpcTLSCert, cfg.grpcTLSKey)
	}
}

// TestLoadConfigFrom_FileConfigPriority проверяет приоритет источников:
// флаги и env перекрывают файл, файл — значения по умолчанию.
func TestLoadConfigFrom_FileConfigPriority(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	path := writeConfigFile(t, `{
		"address": "file:9999",
		"restore": false,
		"store_interval": "5s",
		"store_file": "/tmp/file.db"
	}`)

	oldArgs := os.Args
	os.Args = []string{"server", "-c", path, "-a", "flag:1234", "-i", "100"}
	defer func() { os.Args = oldArgs }()

	_ = os.Setenv("ADDRESS", "env:5678")
	_ = os.Setenv("RESTORE", "true")

	cfg, err := loadConfigFromArgs()
	if err != nil {
		t.Fatalf("loadConfigFromArgs() error = %v", err)
	}
	if cfg.addr != "env:5678" {
		t.Errorf("addr = %q, want env:5678 (env > flag > file)", cfg.addr)
	}
	if cfg.storeInterval != 100 {
		t.Errorf("storeInterval = %d, want 100 (flag > file)", cfg.storeInterval)
	}
	if !cfg.restore {
		t.Error("restore = false, want true (env > file)")
	}
	if cfg.filePath != "/tmp/file.db" {
		t.Errorf("filePath = %q, want /tmp/file.db (file > default)", cfg.filePath)
	}
}

// TestLoadConfigFrom_ConfigEnv проверяет, что путь к файлу можно задать
// переменной окружения CONFIG без флага.
func TestLoadConfigFrom_ConfigEnv(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	path := writeConfigFile(t, `{"address": "envpath:1111"}`)
	_ = os.Setenv("CONFIG", path)

	oldArgs := os.Args
	os.Args = []string{"server"}
	defer func() { os.Args = oldArgs }()

	cfg, err := loadConfigFromArgs()
	if err != nil {
		t.Fatalf("loadConfigFromArgs() error = %v", err)
	}
	if cfg.addr != "envpath:1111" {
		t.Errorf("addr = %q, want envpath:1111", cfg.addr)
	}
}

// TestLoadConfigFrom_FileConfigErrors проверяет, что битый или
// отсутствующий файл конфигурации — ошибка запуска.
func TestLoadConfigFrom_FileConfigErrors(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"server", "-config", filepath.Join(t.TempDir(), "missing.json")}
	if _, err := loadConfigFromArgs(); err == nil {
		t.Fatal("loadConfigFromArgs() = nil, want error for missing config file")
	}

	badPath := writeConfigFile(t, `{"store_interval": "не интервал"}`)
	os.Args = []string{"server", "-c", badPath}
	if _, err := loadConfigFromArgs(); err == nil {
		t.Fatal("loadConfigFromArgs() = nil, want error for invalid store_interval")
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

// --- доверенная подсеть ---

// TestRouter_TrustedSubnet проверяет, что при заданной доверенной подсети
// метрики принимаются только от агентов из неё, а чтение метрик не
// ограничивается.
func TestRouter_TrustedSubnet(t *testing.T) {
	t.Parallel()

	mem := storage.NewMemStorage()
	h := handler.NewHandler(mem)
	if err := h.SetTrustedSubnet("192.168.0.0/16"); err != nil {
		t.Fatalf("SetTrustedSubnet() error = %v", err)
	}
	r := buildRouter(h, "")

	batch := `[{"id":"g1","type":"gauge","value":1.5}]`

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		realIP     string
		wantStatus int
	}{
		{name: "batch from trusted IP", method: http.MethodPost, path: "/updates/", body: batch, realIP: "192.168.1.5", wantStatus: http.StatusOK},
		{name: "batch from foreign IP", method: http.MethodPost, path: "/updates/", body: batch, realIP: "10.0.0.1", wantStatus: http.StatusForbidden},
		{name: "batch without header", method: http.MethodPost, path: "/updates/", body: batch, wantStatus: http.StatusForbidden},
		{name: "update from foreign IP", method: http.MethodPost, path: "/update", body: `{"id":"g2","type":"gauge","value":1}`, realIP: "10.0.0.1", wantStatus: http.StatusForbidden},
		{name: "path update from trusted IP", method: http.MethodPost, path: "/update/gauge/g3/1", realIP: "192.168.0.7", wantStatus: http.StatusOK},
		{name: "root page is not restricted", method: http.MethodGet, path: "/", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body *strings.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			} else {
				body = strings.NewReader("")
			}
			req := httptest.NewRequest(tt.method, tt.path, body)
			req.Header.Set("Content-Type", "application/json")
			if tt.realIP != "" {
				req.Header.Set("X-Real-IP", tt.realIP)
			}
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("%s %s with X-Real-IP=%q -> %d, want %d", tt.method, tt.path, tt.realIP, w.Code, tt.wantStatus)
			}
		})
	}

	// Метрика из доверенного запроса действительно сохранилась.
	if _, ok := mem.GetGauge("g3"); !ok {
		t.Error("g3 not saved by trusted request")
	}
	if _, ok := mem.GetGauge("g1"); !ok {
		t.Error("g1 from trusted batch not saved")
	}
}

// TestRouter_EmptyTrustedSubnet проверяет, что при пустом trusted_subnet
// метрики обрабатываются без ограничений.
func TestRouter_EmptyTrustedSubnet(t *testing.T) {
	t.Parallel()

	mem := storage.NewMemStorage()
	h := handler.NewHandler(mem)
	r := buildRouter(h, "")

	req := httptest.NewRequest(http.MethodPost, "/updates/", strings.NewReader(`[{"id":"g1","type":"gauge","value":1}]`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("updates without trusted subnet -> %d, want 200", w.Code)
	}
	if _, ok := mem.GetGauge("g1"); !ok {
		t.Error("g1 not saved")
	}
}

// --- утилиты ---

// writeConfigFile создаёт временный JSON-файл конфигурации и возвращает путь.
func writeConfigFile(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

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
