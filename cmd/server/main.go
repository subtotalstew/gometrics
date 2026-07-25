package main

import (
	"database/sql"
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
	"github.com/subtotalstew/gometrics.git/internal/handler"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

func main() {

	var (
		addr          string
		storeInterval int
		filePath      string
		restore       bool
		databaseDSN   string
	)

	flag.StringVar(&addr, "a", "localhost:8080", "address and port to run server, format: <hostname>:<port>")
	flag.IntVar(&storeInterval, "i", 300, "interval in seconds to persist metrics to disk (0 = synchronous save)")
	flag.StringVar(&filePath, "f", "metrics-store.json", "path to file for persisting metrics")
	flag.BoolVar(&restore, "r", true, "whether to restore previously saved metrics on start")
	flag.StringVar(&databaseDSN, "d", "", "database connection string")

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

	log.Info().Msgf("Starting server on %s", addr)
	log.Info().
		Int("store_interval", storeInterval).
		Str("file_storage_path", filePath).
		Bool("restore", restore).
		Bool("database_configured", databaseDSN != "").
		Msg("persistence configuration")

	memstorage := storage.NewMemStorage()

	if restore && filePath != "" {
		if err := storage.LoadFromFile(memstorage, filePath); err != nil {
			log.Error().Err(err).Msg("не удалось восстановить метрики из файла")
		}
	}

	h := handler.NewHandler(memstorage)

	var db *sql.DB
	if databaseDSN != "" {
		var err error
		db, err = sql.Open("postgres", databaseDSN)
		if err != nil {
			log.Error().Err(err).Msg("не удалось установить соединение с БД")
		} else {
			h.SetDB(db)
		}
	}

	var stop chan struct{}
	var done chan struct{}

	if filePath != "" {
		if storeInterval == 0 {
			h.SetSyncSave(func() {
				if err := storage.SaveToFile(memstorage, filePath); err != nil {
					log.Error().Err(err).Msg("не удалось синхронно сохранить метрики")
				}
			})
		} else {
			stop = make(chan struct{})
			done = make(chan struct{})
			go func() {
				defer close(done)
				storage.RunPeriodicSave(memstorage, filePath, storeInterval, stop)
			}()
		}
	}

	r := chi.NewRouter()

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

	srv := &http.Server{Addr: addr, Handler: r}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Msg(err.Error())
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Info().Msg("shutting down, saving metrics before exit")

	if stop != nil {
		close(stop)
		<-done
	}

	if filePath != "" {
		if err := storage.SaveToFile(memstorage, filePath); err != nil {
			log.Error().Err(err).Msg("не удалось сохранить метрики при завершении работы")
		}
	}

	_ = srv.Close()
}
