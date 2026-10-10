// Command server запускает HTTP-сервер метрик.
//
// main не вызывает os.Exit напрямую (это запрещено анализатором noosexit);
// вся работа делегируется run(), а ошибка логируется через log.Print.
// Такое разделение заодно делает сервер тестируемым: run, loadConfigFrom
// и buildRouter можно вызывать из тестов без запуска процесса.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog/log"

	"github.com/subtotalstew/gometrics.git/internal/audit"
	"github.com/subtotalstew/gometrics.git/internal/handler"
	"github.com/subtotalstew/gometrics.git/internal/profiler"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

var (
	buildVersion string
	buildDate    string
	buildCommit  string
)

// config — параметры запуска сервера.
type config struct {
	addr          string
	storeInterval int
	filePath      string
	restore       bool
	databaseDSN   string
	key           string
	auditFile     string
	auditURL      string
	pprofAddr     string
}

func main() {
	printBuildInfo()

	if err := run(); err != nil {
		log.Error().Err(err).Msg("server stopped with error")
	}
}

func printBuildInfo() {
	fmt.Printf("Build version: %s\n", orNA(buildVersion))
	fmt.Printf("Build date: %s\n", orNA(buildDate))
	fmt.Printf("Build commit: %s\n", orNA(buildCommit))
}

func orNA(s string) string {
	if s == "" {
		return "N/A"
	}
	return s
}

// run загружает конфигурацию, собирает зависимости, запускает сервер
// и блокируется до получения SIGINT/SIGTERM.
func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	return serve(cfg)
}

// loadConfig парсит флаги и переменные окружения в проде.
func loadConfig() (config, error) {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	return loadConfigFrom(fs)
}

// loadConfigFromArgs — как loadConfig, но создаёт свежий FlagSet.
// Нужна для тестов, чтобы не переиспользовать глобальный flag.CommandLine.
func loadConfigFromArgs() (config, error) {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	return loadConfigFrom(fs)
}

// loadConfigFrom — общая логика парсинга флагов и env.
func loadConfigFrom(fs *flag.FlagSet) (config, error) {
	cfg := config{}

	fs.StringVar(&cfg.addr, "a", "localhost:8080", "address and port to run server, format: <hostname>:<port>")
	fs.IntVar(&cfg.storeInterval, "i", 300, "interval in seconds to persist metrics to disk (0 = synchronous save)")
	fs.StringVar(&cfg.filePath, "f", "metrics-store.json", "path to file for persisting metrics")
	fs.BoolVar(&cfg.restore, "r", true, "whether to restore previously saved metrics on start")
	fs.StringVar(&cfg.databaseDSN, "d", "", "database connection string")
	fs.StringVar(&cfg.key, "k", "", "key for decrypt")
	fs.StringVar(&cfg.auditFile, "audit-file", "", "path to audit log file")
	fs.StringVar(&cfg.auditURL, "audit-url", "", "URL for audit log delivery")
	fs.StringVar(&cfg.pprofAddr, "pprof-addr", "", "address of the pprof/debug HTTP server (empty = disabled)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return config{}, err
	}

	if v := os.Getenv("ADDRESS"); v != "" {
		cfg.addr = v
	}
	if v := os.Getenv("STORE_INTERVAL"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return config{}, fmt.Errorf("неверный формат STORE_INTERVAL: %w", err)
		}
		cfg.storeInterval = n
	}
	if v := os.Getenv("FILE_STORAGE_PATH"); v != "" {
		cfg.filePath = v
	}
	if v := os.Getenv("RESTORE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return config{}, fmt.Errorf("неверный формат RESTORE: %w", err)
		}
		cfg.restore = b
	}
	if v := os.Getenv("DATABASE_DSN"); v != "" {
		cfg.databaseDSN = v
	}
	if v := os.Getenv("KEY"); v != "" {
		cfg.key = v
	}
	if v := os.Getenv("AUDIT_FILE"); v != "" {
		cfg.auditFile = v
	}
	if v := os.Getenv("AUDIT_URL"); v != "" {
		cfg.auditURL = v
	}
	if v := os.Getenv("PPROF_ADDR"); v != "" {
		cfg.pprofAddr = v
	}
	return cfg, nil
}

// buildRouter собирает chi-роутер с полным стеком middleware.
// Вынесено из serve(), чтобы тестировать маршрутизацию без запуска сервера.
func buildRouter(h *handler.Handler, key string) *chi.Mux {
	r := chi.NewRouter()

	if key != "" {
		h.SetKey(key)
		r.Use(h.HashMiddleware)
	}
	r.Use(h.GzipMiddleware)
	r.Use(h.LoggingMiddleware)
	r.Use(middleware.Recoverer)

	r.Post("/update", h.UpdateJSONHandler)
	r.Post("/value", h.ValueJSONHandler)
	r.Post("/update/", h.UpdateJSONHandler)
	r.Post("/value/", h.ValueJSONHandler)

	r.Post("/update/{type}/{name}/{value}", h.UpdateHandler)
	r.Get("/value/{type}/{name}", h.ValueHandler)
	r.Get("/", h.RootHandler)
	r.Get("/ping", h.PingHandler)

	r.Post("/updates", h.UpdatesJSONHandler)
	r.Post("/updates/", h.UpdatesJSONHandler)

	return r
}

// serve — вся оркестрация: pprof, хранилище, аудит, роутер, сервер, сигналы.
func serve(cfg config) error {
	log.Info().Msgf("Starting server on %s", cfg.addr)

	var pprofSrv *http.Server
	if cfg.pprofAddr != "" {
		var err error
		pprofSrv, err = profiler.Start(cfg.pprofAddr)
		if err != nil {
			return fmt.Errorf("не удалось запустить pprof-сервер: %w", err)
		}
		defer func() { _ = pprofSrv.Close() }()
		log.Info().Str("addr", pprofSrv.Addr).Msg("pprof-сервер запущен")
	}

	var (
		metricsStorage storage.Storage
		dbStorage      *storage.DBStorage
	)

	switch {
	case cfg.databaseDSN != "":
		var err error
		dbStorage, err = storage.NewDBStorage(cfg.databaseDSN, "migrations")
		if err != nil {
			return fmt.Errorf("не удалось инициализировать хранилище PostgreSQL: %w", err)
		}
		defer func() { _ = dbStorage.Close() }()
		metricsStorage = dbStorage
		log.Info().Msg("используется хранилище: PostgreSQL")

	default:
		mem := storage.NewMemStorage()
		metricsStorage = mem

		if cfg.filePath != "" {
			log.Info().Msg("используется хранилище: файл (с бэкапом в память)")
			if cfg.restore {
				if err := storage.LoadFromFile(mem, cfg.filePath); err != nil {
					log.Error().Err(err).Msg("не удалось восстановить метрики из файла")
				}
			}
		} else {
			log.Info().Msg("используется хранилище: память")
		}
	}

	h := handler.NewHandler(metricsStorage)

	// Аудит включается, только если задан хотя бы один приёмник.
	if cfg.auditFile != "" || cfg.auditURL != "" {
		auditSubject := audit.NewSubject()
		defer func() {
			if err := auditSubject.Close(); err != nil {
				log.Error().Err(err).Msg("не удалось корректно закрыть аудит")
			}
		}()

		if cfg.auditFile != "" {
			auditSubject.Register(audit.NewFileObserver(cfg.auditFile))
			log.Info().Str("file", cfg.auditFile).Msg("аудит: включён файловый приёмник")
		}
		if cfg.auditURL != "" {
			auditSubject.Register(audit.NewURLObserver(cfg.auditURL))
			log.Info().Str("url", cfg.auditURL).Msg("аудит: включён удалённый приёмник")
		}
		h.SetAudit(auditSubject)
	}

	if dbStorage != nil {
		h.SetDB(dbStorage.DB())
	}

	var stop, done chan struct{}

	if cfg.databaseDSN == "" && cfg.filePath != "" {
		if memStorage, ok := metricsStorage.(*storage.MemStorage); ok {
			if cfg.storeInterval == 0 {
				h.SetSyncSave(func() {
					if err := storage.SaveToFile(memStorage, cfg.filePath); err != nil {
						log.Error().Err(err).Msg("не удалось синхронно сохранить метрики")
					}
				})
			} else {
				stop = make(chan struct{})
				done = make(chan struct{})
				go func() {
					defer close(done)
					storage.RunPeriodicSave(memStorage, cfg.filePath, cfg.storeInterval, stop)
				}()
			}
		}
	}

	r := buildRouter(h, cfg.key)
	srv := &http.Server{Addr: cfg.addr, Handler: r}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("HTTP-сервер завершился с ошибкой")
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Info().Msg("shutting down")

	if stop != nil {
		close(stop)
		<-done
	}

	if cfg.databaseDSN == "" && cfg.filePath != "" {
		if memStorage, ok := metricsStorage.(*storage.MemStorage); ok {
			if err := storage.SaveToFile(memStorage, cfg.filePath); err != nil {
				log.Error().Err(err).Msg("не удалось сохранить метрики при завершении работы")
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)

	return nil
}
