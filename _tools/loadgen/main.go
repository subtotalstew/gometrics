// Command loadgen создаёт нагрузку на сервис метрик, чтобы снять
// профиль памяти (pprof) на «горячем» сервисе.
//
// Пример запуска:
//
//	go run ./_tools/loadgen -addr=http://127.0.0.1:8080 -workers=8 -duration=30s -metrics=5000
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/subtotalstew/gometrics.git/internal/loadgen"
)

func main() {
	var cfg loadgen.Config

	flag.StringVar(&cfg.Addr, "addr", "http://127.0.0.1:8080", "базовый адрес сервиса метрик")
	flag.IntVar(&cfg.Workers, "workers", 8, "количество параллельных клиентов")
	flag.DurationVar(&cfg.Duration, "duration", 30*time.Second, "длительность нагрузки")
	flag.IntVar(&cfg.SeedMetrics, "metrics", 5000, "количество уникальных метрик для сидирования")
	flag.IntVar(&cfg.BatchSize, "batch", 100, "размер батча в запросе POST /updates")
	flag.IntVar(&cfg.Requests, "requests", 0, "остановиться после N запросов (0 = работать по времени)")
	flag.StringVar(&cfg.Key, "key", "", "ключ подписи HashSHA256 (пусто = без подписи)")
	flag.Parse()

	stats, err := loadgen.Run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		os.Exit(1)
	}

	out, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
