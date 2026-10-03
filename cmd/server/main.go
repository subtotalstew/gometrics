package main

import (
	"flag"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog/log"

	"github.com/subtotalstew/gometrics.git/internal/audit"
	"github.com/subtotalstew/gometrics.git/internal/handler"
	"github.com/subtotalstew/gometrics.git/internal/profiler"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

func main() {

	var (
		addr          string
		storeInterval int
		filePath      string
		restore       bool
		databaseDSN   string
		key           string
		auditFile     string
		auditURL      string
		pprofAddr     string
	)

	flag.StringVar(&addr, "a", "localhost:8080", "address and port to run server, format: <hostname>:<port>")
	flag.IntVar(&storeInterval, "i", 300, "interval in seconds to persist metrics to disk (0 = synchronous save)")
	flag.StringVar(&filePath, "f", "metrics-store.json", "path to file for persisting metrics")
	flag.BoolVar(&restore, "r", true, "whether to restore previously saved metrics on start")
	flag.StringVar(&databaseDSN, "d", "", "database connection string")
	flag.StringVar(&key, "k", "", "key for decrypt")
	flag.StringVar(&auditFile, "audit-file", "", "path to audit log file")
	flag.StringVar(&auditURL, "audit-url", "", "URL for audit log delivery")
	flag.StringVar(&pprofAddr, "pprof-addr", "", "address of the pprof/debug HTTP server (empty = disabled)")

	flag.Parse()

	if envAddr := os.Getenv("ADDRESS"); envAddr != "" {
		addr = envAddr
	}
	if envInterval := os.Getenv("STORE_INTERVAL"); envInterval != "" {
		val, err := strconv.Atoi(envInterval)
		if err != nil {
			log.Fatal().Err(err).Msg("неверный формат STORE_INTERVAL")
		}
		storeInterval = val
	}
	if envFile := os.Getenv("FILE_STORAGE_PATH"); envFile != "" {
		filePath = envFile
	}
	if envRestore := os.Getenv("RESTORE"); envRestore != "" {
		val, err := strconv.ParseBool(envRestore)
		if err != nil {
			log.Fatal().Err(err).Msg("неверный формат RESTORE")
		}
		restore = val
	}
	if envDSN := os.Getenv("DATABASE_DSN"); envDSN != "" {
		databaseDSN = envDSN
	}
	if envKey := os.Getenv("KEY"); envKey != "" {
		key = envKey
	}
	if envAuditFile := os.Getenv("AUDIT_FILE"); envAuditFile != "" {
		auditFile = envAuditFile
	}
	if envAuditURL := os.Getenv("AUDIT_URL"); envAuditURL != "" {
		auditURL = envAuditURL
	}
	if envPprofAddr := os.Getenv("PPROF_ADDR"); envPprofAddr != "" {
		pprofAddr = envPprofAddr
	}
	log.Info().Msgf("Starting server on %s", addr)

	var pprofSrv *http.Server
	if pprofAddr != "" {
		var err error
		pprofSrv, err = profiler.Start(pprofAddr)
		if err != nil {
			log.Fatal().Err(err).Msg("не удалось запустить pprof-сервер")
		}
		log.Info().Str("addr", pprofSrv.Addr).Msg("pprof-сервер запущен")
	}

	var (
		metricsStorage storage.Storage
		dbStorage      *storage.DBStorage
	)

	switch {
	case databaseDSN != "":
		var err error
		dbStorage, err = storage.NewDBStorage(databaseDSN, "migrations")
		if err != nil {
			log.Fatal().Err(err).Msg("не удалось инициализировать хранилище PostgreSQL")
		}
		metricsStorage = dbStorage
		log.Info().Msg("используется хранилище: PostgreSQL")

	default:
		mem := storage.NewMemStorage()
		metricsStorage = mem

		if filePath != "" {
			log.Info().Msg("используется хранилище: файл (с бэкапом в память)")
			if restore {
				if err := storage.LoadFromFile(mem, filePath); err != nil {
					log.Error().Err(err).Msg("не удалось восстановить метрики из файла")
				}
			}
		} else {
			log.Info().Msg("используется хранилище: память")
		}
	}

	h := handler.NewHandler(metricsStorage)

	auditSubject := audit.NewSubject()
	if auditFile != "" {
		auditSubject.Register(audit.NewFileObserver(auditFile))
		log.Info().Str("file", auditFile).Msg("аудит: включён файловый приёмник")
	}
	if auditURL != "" {
		auditSubject.Register(audit.NewURLObserver(auditURL))
		log.Info().Str("url", auditURL).Msg("аудит: включён удалённый приёмник")
	}
	h.SetAudit(auditSubject)

	if dbStorage != nil {
		h.SetDB(dbStorage.DB())
	}

	var stop chan struct{}
	var done chan struct{}

	if databaseDSN == "" && filePath != "" {
		if memStorage, ok := metricsStorage.(*storage.MemStorage); ok {
			if storeInterval == 0 {
				h.SetSyncSave(func() {
					if err := storage.SaveToFile(memStorage, filePath); err != nil {
						log.Error().Err(err).Msg("не удалось синхронно сохранить метрики")
					}
				})
			} else {
				stop = make(chan struct{})
				done = make(chan struct{})
				go func() {
					defer close(done)
					storage.RunPeriodicSave(memStorage, filePath, storeInterval, stop)
				}()
			}
		}
	}

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

	srv := &http.Server{Addr: addr, Handler: r}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Msg(err.Error())
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

	if databaseDSN == "" && filePath != "" {
		if memStorage, ok := metricsStorage.(*storage.MemStorage); ok {
			if err := storage.SaveToFile(memStorage, filePath); err != nil {
				log.Error().Err(err).Msg("не удалось сохранить метрики при завершении работы")
			}
		}
	}

	if dbStorage != nil {
		_ = dbStorage.Close()
	}

	if pprofSrv != nil {
		_ = pprofSrv.Close()
	}

	_ = srv.Close()
}
