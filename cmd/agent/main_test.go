package main

import (
	"os"
	"testing"
)

// envKeys — переменные окружения, которые читает loadConfigFrom.
// Используются в saveEnv/restoreEnv/clearEnv, чтобы не трогать чужие env.
var envKeys = []string{
	"ADDRESS",
	"POLL_INTERVAL",
	"REPORT_INTERVAL",
	"KEY",
	"RATE_LIMIT",
}

// TestLoadConfig_Defaults проверяет значения по умолчанию, когда
// ни флаги, ни переменные окружения не заданы.
func TestLoadConfig_Defaults(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	// Обнуляем args, чтобы flags.Parse() не увидел мусор из `go test`.
	oldArgs := os.Args
	os.Args = []string{"agent"}
	defer func() { os.Args = oldArgs }()

	cfg, err := loadConfigFromArgs()
	if err != nil {
		t.Fatalf("loadConfigFromArgs() error = %v", err)
	}
	if cfg.addr != "localhost:8080" {
		t.Errorf("addr = %q, want localhost:8080", cfg.addr)
	}
	if cfg.pollInterval != 2 {
		t.Errorf("pollInterval = %d, want 2", cfg.pollInterval)
	}
	if cfg.reportInterval != 10 {
		t.Errorf("reportInterval = %d, want 10", cfg.reportInterval)
	}
	if cfg.key != "" {
		t.Errorf("key = %q, want empty", cfg.key)
	}
	if cfg.rateLimit != 1 {
		t.Errorf("rateLimit = %d, want 1", cfg.rateLimit)
	}
}

// TestLoadConfig_FromEnv проверяет, что переменные окружения
// переопределяют значения по умолчанию.
func TestLoadConfig_FromEnv(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"agent"}
	defer func() { os.Args = oldArgs }()

	if err := os.Setenv("ADDRESS", "example.com:9090"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("POLL_INTERVAL", "5"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("REPORT_INTERVAL", "15"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("KEY", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("RATE_LIMIT", "3"); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfigFromArgs()
	if err != nil {
		t.Fatalf("loadConfigFromArgs() error = %v", err)
	}
	if cfg.addr != "example.com:9090" {
		t.Errorf("addr = %q, want example.com:9090", cfg.addr)
	}
	if cfg.pollInterval != 5 {
		t.Errorf("pollInterval = %d, want 5", cfg.pollInterval)
	}
	if cfg.reportInterval != 15 {
		t.Errorf("reportInterval = %d, want 15", cfg.reportInterval)
	}
	if cfg.key != "secret" {
		t.Errorf("key = %q, want secret", cfg.key)
	}
	if cfg.rateLimit != 3 {
		t.Errorf("rateLimit = %d, want 3", cfg.rateLimit)
	}
}

// TestLoadConfig_InvalidPollInterval проверяет ошибку при
// нечисловом POLL_INTERVAL.
func TestLoadConfig_InvalidPollInterval(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"agent"}
	defer func() { os.Args = oldArgs }()

	if err := os.Setenv("POLL_INTERVAL", "abc"); err != nil {
		t.Fatal(err)
	}

	if _, err := loadConfigFromArgs(); err == nil {
		t.Fatal("loadConfigFromArgs() = nil, want error for invalid POLL_INTERVAL")
	}
}

// TestLoadConfig_InvalidReportInterval проверяет ошибку при
// нечисловом REPORT_INTERVAL.
func TestLoadConfig_InvalidReportInterval(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"agent"}
	defer func() { os.Args = oldArgs }()

	if err := os.Setenv("REPORT_INTERVAL", "xyz"); err != nil {
		t.Fatal(err)
	}

	if _, err := loadConfigFromArgs(); err == nil {
		t.Fatal("loadConfigFromArgs() = nil, want error for invalid REPORT_INTERVAL")
	}
}

// TestLoadConfig_InvalidRateLimit проверяет ошибку при
// нечисловом RATE_LIMIT.
func TestLoadConfig_InvalidRateLimit(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"agent"}
	defer func() { os.Args = oldArgs }()

	if err := os.Setenv("RATE_LIMIT", "not-a-number"); err != nil {
		t.Fatal(err)
	}

	if _, err := loadConfigFromArgs(); err == nil {
		t.Fatal("loadConfigFromArgs() = nil, want error for invalid RATE_LIMIT")
	}
}

// TestLoadConfig_ZeroPollInterval проверяет валидацию pollInterval <= 0.
func TestLoadConfig_ZeroPollInterval(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"agent"}
	defer func() { os.Args = oldArgs }()

	if err := os.Setenv("POLL_INTERVAL", "0"); err != nil {
		t.Fatal(err)
	}

	if _, err := loadConfigFromArgs(); err == nil {
		t.Fatal("loadConfigFromArgs() = nil, want error for pollInterval <= 0")
	}
}

// TestLoadConfig_ZeroReportInterval проверяет валидацию reportInterval <= 0.
func TestLoadConfig_ZeroReportInterval(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"agent"}
	defer func() { os.Args = oldArgs }()

	if err := os.Setenv("REPORT_INTERVAL", "0"); err != nil {
		t.Fatal(err)
	}

	if _, err := loadConfigFromArgs(); err == nil {
		t.Fatal("loadConfigFromArgs() = nil, want error for reportInterval <= 0")
	}
}

// TestLoadConfigFrom_FlagsOverrideDefaults проверяет, что флаги
// имеют приоритет над значениями по умолчанию.
func TestLoadConfigFrom_FlagsOverrideDefaults(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"agent", "-a", "flagaddr:1234", "-p", "7", "-r", "21", "-k", "flagkey", "-l", "4"}
	defer func() { os.Args = oldArgs }()

	cfg, err := loadConfigFromArgs()
	if err != nil {
		t.Fatalf("loadConfigFromArgs() error = %v", err)
	}
	if cfg.addr != "flagaddr:1234" {
		t.Errorf("addr = %q, want flagaddr:1234", cfg.addr)
	}
	if cfg.pollInterval != 7 {
		t.Errorf("pollInterval = %d, want 7", cfg.pollInterval)
	}
	if cfg.reportInterval != 21 {
		t.Errorf("reportInterval = %d, want 21", cfg.reportInterval)
	}
	if cfg.key != "flagkey" {
		t.Errorf("key = %q, want flagkey", cfg.key)
	}
	if cfg.rateLimit != 4 {
		t.Errorf("rateLimit = %d, want 4", cfg.rateLimit)
	}
}

// TestLoadConfig_EnvOverridesFlags проверяет, что env имеет приоритет
// над флагами.
func TestLoadConfig_EnvOverridesFlags(t *testing.T) {
	saved := saveEnv()
	defer restoreEnv(saved)
	clearEnv()

	oldArgs := os.Args
	os.Args = []string{"agent", "-a", "flagaddr:1234", "-p", "7"}
	defer func() { os.Args = oldArgs }()

	if err := os.Setenv("ADDRESS", "envaddr:5678"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("POLL_INTERVAL", "9"); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfigFromArgs()
	if err != nil {
		t.Fatalf("loadConfigFromArgs() error = %v", err)
	}
	if cfg.addr != "envaddr:5678" {
		t.Errorf("addr = %q, want envaddr:5678 (env wins over flag)", cfg.addr)
	}
	if cfg.pollInterval != 9 {
		t.Errorf("pollInterval = %d, want 9 (env wins over flag)", cfg.pollInterval)
	}
}

// --- вспомогательные функции ---

// saveEnv сохраняет текущие значения env-переменных, чтобы
// восстановить их после теста.
func saveEnv() map[string]string {
	saved := make(map[string]string, len(envKeys))
	for _, k := range envKeys {
		saved[k] = os.Getenv(k)
	}
	return saved
}

// restoreEnv восстанавливает env-переменные из saveEnv.
func restoreEnv(saved map[string]string) {
	for k, v := range saved {
		if v == "" {
			_ = os.Unsetenv(k)
		} else {
			_ = os.Setenv(k, v)
		}
	}
}

// clearEnv очищает env-переменные, которые читает loadConfigFrom,
// чтобы тесты не зависели от окружения CI.
func clearEnv() {
	for _, k := range envKeys {
		_ = os.Unsetenv(k)
	}
}
