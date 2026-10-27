// Command agent собирает метрики рантайма и системы и отправляет их
// на сервер метрик батчами через POST /updates/. Тело запроса можно
// зашифровать публичным ключом (флаг -crypto-key / CRYPTO_KEY).
//
// main не вызывает os.Exit напрямую (это запрещено анализатором noosexit);
// вся работа делегируется run(), а ошибка конфигурации логируется через
// log.Printf. Так сохраняются все defer-очистки внутри run() и агент
// завершается контролируемо.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/subtotalstew/gometrics.git/internal/agent"
)

// config — параметры запуска агента.
type config struct {
	configPath     string
	addr           string
	pollInterval   int
	reportInterval int
	key            string
	rateLimit      int
	cryptoKey      string
}

var (
	buildVersion = "N/A"
	buildDate    = "N/A"
	buildCommit  = "N/A"
)

// main не вызывает os.Exit и не вызывает log.Fatal*, чтобы удовлетворять
// анализатору noosexit.
func main() {
	printBuildInfo()

	if err := run(); err != nil {
		log.Printf("agent: %v", err)
	}
}

func printBuildInfo() {
	fmt.Printf("Build version: %s\n", buildVersion)
	fmt.Printf("Build date: %s\n", buildDate)
	fmt.Printf("Build commit: %s\n", buildCommit)
}

// run парсит конфигурацию, создаёт и запускает агент.
func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	a := agent.NewAgent(
		"http://"+cfg.addr,
		cfg.pollInterval,
		cfg.reportInterval,
		cfg.rateLimit,
		cfg.key,
	)

	// Публичный ключ читается на старте: ошибка в пути должна быть видна
	// сразу, а не после первого цикла отправки метрик.
	if cfg.cryptoKey != "" {
		if err := a.SetCryptoKey(cfg.cryptoKey); err != nil {
			return err
		}
		log.Printf("agent: асимметричное шифрование включено, публичный ключ: %s", cfg.cryptoKey)
	}

	a.Run()
	return nil
}

// loadConfig парсит флаги и env для продакшн-запуска.
func loadConfig() (config, error) {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	return loadConfigFrom(fs)
}

// loadConfigFromArgs создаёт свежий FlagSet и парсит os.Args.
// Нужна для тестов: глобальный flag.CommandLine нельзя переиспользовать.
func loadConfigFromArgs() (config, error) {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	return loadConfigFrom(fs)
}

// loadConfigFrom — общая логика для loadConfig и loadConfigFromArgs.
// Приоритет источников: файл конфигурации < флаги < переменные окружения.
func loadConfigFrom(fs *flag.FlagSet) (config, error) {
	cfg := config{}

	fs.StringVar(&cfg.configPath, "c", "", "path to JSON config file")
	fs.StringVar(&cfg.configPath, "config", "", "path to JSON config file (alias for -c)")
	fs.StringVar(&cfg.addr, "a", "localhost:8080", "server address")
	fs.IntVar(&cfg.pollInterval, "p", 2, "poll interval in seconds")
	fs.IntVar(&cfg.reportInterval, "r", 10, "report interval in seconds")
	fs.StringVar(&cfg.key, "k", "", "key for encrypt")
	fs.StringVar(&cfg.cryptoKey, "crypto-key", "", "path to public key for asymmetric encryption")
	fs.IntVar(&cfg.rateLimit, "l", 1, "max number of simultaneously outgoing requests to the server")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return config{}, err
	}

	// Запоминаем явно заданные флаги: их значения файл не перекрывает.
	explicit := make(map[string]bool, fs.NFlag())
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	// CONFIG задаёт путь к файлу и, как и остальные env, приоритетнее флага.
	if v := os.Getenv("CONFIG"); v != "" {
		cfg.configPath = v
	}
	if err := applyFileConfig(&cfg, cfg.configPath, func(name string) bool {
		return explicit[name]
	}); err != nil {
		return config{}, err
	}

	if envAddr := os.Getenv("ADDRESS"); envAddr != "" {
		cfg.addr = envAddr
	}
	if v := os.Getenv("POLL_INTERVAL"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return config{}, fmt.Errorf("неверный формат POLL_INTERVAL: %w", err)
		}
		cfg.pollInterval = n
	}
	if v := os.Getenv("REPORT_INTERVAL"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return config{}, fmt.Errorf("неверный формат REPORT_INTERVAL: %w", err)
		}
		cfg.reportInterval = n
	}
	if v := os.Getenv("KEY"); v != "" {
		cfg.key = v
	}
	if v := os.Getenv("CRYPTO_KEY"); v != "" {
		cfg.cryptoKey = v
	}
	if v := os.Getenv("RATE_LIMIT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return config{}, fmt.Errorf("неверный формат RATE_LIMIT: %w", err)
		}
		cfg.rateLimit = n
	}

	if cfg.pollInterval <= 0 {
		return config{}, errors.New("poll interval must be > 0")
	}
	if cfg.reportInterval <= 0 {
		return config{}, errors.New("report interval must be > 0")
	}
	return cfg, nil
}
