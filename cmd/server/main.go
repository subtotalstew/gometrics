// Command server запускает HTTP-сервер метрик и, если задан -g/GRPC_ADDRESS,
// gRPC-сервер с тем же хранилищем.
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
	"net"
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
	"google.golang.org/grpc"

	"github.com/subtotalstew/gometrics.git/internal/audit"
	fileconfig "github.com/subtotalstew/gometrics.git/internal/config"
	"github.com/subtotalstew/gometrics.git/internal/grpcserver"
	"github.com/subtotalstew/gometrics.git/internal/grpctls"
	"github.com/subtotalstew/gometrics.git/internal/handler"
	"github.com/subtotalstew/gometrics.git/internal/profiler"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

var (
	buildVersion = "N/A"
	buildDate    = "N/A"
	buildCommit  = "N/A"
)

// shutdownTimeout — сколько ждём завершения уже начатых HTTP-запросов при
// остановке. Это время нужно, чтобы их данные успели дойти до хранилища
// до финального сохранения.
const shutdownTimeout = 10 * time.Second

// config — параметры запуска сервера.
type config struct {
	configPath    string
	addr          string
	storeInterval int
	filePath      string
	restore       bool
	databaseDSN   string
	key           string
	cryptoKey     string
	auditFile     string
	auditURL      string
	pprofAddr     string
	trustedSubnet string
	grpcAddr      string
	grpcTLSCert   string
	grpcTLSKey    string
}

func main() {
	printBuildInfo()

	if err := run(); err != nil {
		log.Error().Err(err).Msg("server stopped with error")
	}
}

func printBuildInfo() {
	fmt.Printf("Build version: %s\n", buildVersion)
	fmt.Printf("Build date: %s\n", buildDate)
	fmt.Printf("Build commit: %s\n", buildCommit)
}

// run загружает конфигурацию, собирает зависимости, запускает сервер
// и блокируется до получения SIGINT/SIGTERM/SIGQUIT.
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

// loadConfigFrom — общая логика парсинга флагов, файла конфигурации и env.
// Приоритет источников: файл конфигурации < флаги < переменные окружения.
func loadConfigFrom(fs *flag.FlagSet) (config, error) {
	cfg := config{}

	fs.StringVar(&cfg.configPath, "c", "", "path to JSON config file")
	fs.StringVar(&cfg.configPath, "config", "", "path to JSON config file (alias for -c)")
	fs.StringVar(&cfg.addr, "a", "localhost:8080", "address and port to run server, format: <hostname>:<port>")
	fs.IntVar(&cfg.storeInterval, "i", 300, "interval in seconds to persist metrics to disk (0 = synchronous save)")
	fs.StringVar(&cfg.filePath, "f", "metrics-store.json", "path to file for persisting metrics")
	fs.BoolVar(&cfg.restore, "r", true, "whether to restore previously saved metrics on start")
	fs.StringVar(&cfg.databaseDSN, "d", "", "database connection string")
	fs.StringVar(&cfg.key, "k", "", "key for decrypt")
	fs.StringVar(&cfg.cryptoKey, "crypto-key", "", "path to private key file for asymmetric decryption")
	fs.StringVar(&cfg.auditFile, "audit-file", "", "path to audit log file")
	fs.StringVar(&cfg.auditURL, "audit-url", "", "URL for audit log delivery")
	fs.StringVar(&cfg.pprofAddr, "pprof-addr", "", "address of the pprof/debug HTTP server (empty = disabled)")
	fs.StringVar(&cfg.trustedSubnet, "t", "", "trusted subnet in CIDR notation; metrics from other IPs are rejected with 403 (empty = no restriction)")
	fs.StringVar(&cfg.grpcAddr, "g", "", "address and port to run the gRPC server (empty = disabled)")
	fs.StringVar(&cfg.grpcAddr, "grpc-address", "", "address and port to run the gRPC server (alias for -g)")
	fs.StringVar(&cfg.grpcTLSCert, "grpc-tls-cert", "", "path to PEM certificate (or certificate chain) for the gRPC server")
	fs.StringVar(&cfg.grpcTLSKey, "grpc-tls-key", "", "path to PEM private key for the gRPC server")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return config{}, err
	}

	// CONFIG задаёт путь к файлу и, как и остальные env, приоритетнее флага.
	if v := os.Getenv("CONFIG"); v != "" {
		cfg.configPath = v
	}
	// fileconfig.ExplicitFlags учитывает флаги, заданные явно: их значения
	// файл не перекрывает.
	if err := applyFileConfig(&cfg, cfg.configPath, fileconfig.ExplicitFlags(fs)); err != nil {
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
	if v := os.Getenv("CRYPTO_KEY"); v != "" {
		cfg.cryptoKey = v
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
	if v := os.Getenv("TRUSTED_SUBNET"); v != "" {
		cfg.trustedSubnet = v
	}
	if v := os.Getenv("GRPC_ADDRESS"); v != "" {
		cfg.grpcAddr = v
	}
	if v := os.Getenv("GRPC_TLS_CERT"); v != "" {
		cfg.grpcTLSCert = v
	}
	if v := os.Getenv("GRPC_TLS_KEY"); v != "" {
		cfg.grpcTLSKey = v
	}
	return cfg, nil
}

// buildRouter собирает chi-роутер с полным стеком middleware.
// Вынесено из serve(), чтобы тестировать маршрутизацию без запуска сервера.
//
// Порядок middleware важен: подпись проверяется по байтам, пришедшим по сети
// (по шифртексту), затем тело расшифровывается приватным ключом, и только
// после этого распаковывается gzip. Ключ расшифрования задаётся в serve через
// handler.SetCryptoKey; без ключа DecryptMiddleware ничего не делает.
//
// Запись метрик дополнительно защищена проверкой X-Real-IP: если сервер
// запущен с trusted_subnet, запросы на /update*, /updates* из чужих адресов
// получают 403. Чтение метрик (/, /value, /ping) не ограничивается.
func buildRouter(h *handler.Handler, key string) *chi.Mux {
	r := chi.NewRouter()

	if key != "" {
		h.SetKey(key)
		r.Use(h.HashMiddleware)
	}
	r.Use(h.DecryptMiddleware)
	r.Use(h.GzipMiddleware)
	r.Use(h.LoggingMiddleware)
	r.Use(middleware.Recoverer)

	// Запись метрик — в отдельной группе, потому что только эти маршруты
	// проверяют доверенную подсеть. Middleware подключается, только если
	// подсеть действительно задана: при пустом trusted_subnet в цепочке
	// не должно быть лишнего звена.
	r.Group(func(r chi.Router) {
		if h.HasTrustedSubnet() {
			r.Use(h.TrustedSubnetMiddleware)
		}

		r.Post("/update", h.UpdateJSONHandler)
		r.Post("/update/", h.UpdateJSONHandler)

		r.Post("/update/{type}/{name}/{value}", h.UpdateHandler)

		r.Post("/updates", h.UpdatesJSONHandler)
		r.Post("/updates/", h.UpdatesJSONHandler)
	})

	r.Post("/value", h.ValueJSONHandler)
	r.Post("/value/", h.ValueJSONHandler)

	r.Get("/value/{type}/{name}", h.ValueHandler)
	r.Get("/", h.RootHandler)
	r.Get("/ping", h.PingHandler)

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

	// Доверенная подсеть разбирается на старте: битый CIDR в конфигурации
	// должен останавливать сервер, а не отключать проверку незаметно.
	if err := h.SetTrustedSubnet(cfg.trustedSubnet); err != nil {
		return err
	}
	if cfg.trustedSubnet != "" {
		log.Info().Str("subnet", cfg.trustedSubnet).Msg("проверка X-Real-IP: включена")
	}

	// Приватный ключ читается на старте: опечатка в пути или битый файл
	// должны останавливать сервер сразу, а не ломать каждый батч агента.
	if cfg.cryptoKey != "" {
		if err := h.SetCryptoKey(cfg.cryptoKey); err != nil {
			return fmt.Errorf("не удалось загрузить приватный ключ: %w", err)
		}
		log.Info().Str("key", cfg.cryptoKey).Msg("асимметричное шифрование: включено")
	}

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

	// syncSave — общий колбэк синхронного сохранения для HTTP- и gRPC-пути.
	var syncSave func()

	// stopPeriodic останавливает фоновое сохранение и ждёт, пока текущая
	// запись на диск закончится. Повторный вызов ничего не делает.
	stopPeriodic := func() {
		if stop == nil {
			return
		}
		close(stop)
		<-done
		stop = nil
	}
	defer stopPeriodic()

	if cfg.databaseDSN == "" && cfg.filePath != "" {
		if memStorage, ok := metricsStorage.(*storage.MemStorage); ok {
			if cfg.storeInterval == 0 {
				syncSave = func() {
					if err := storage.SaveToFile(memStorage, cfg.filePath); err != nil {
						log.Error().Err(err).Msg("не удалось синхронно сохранить метрики")
					}
				}
				h.SetSyncSave(syncSave)
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

	// gRPC-сервер работает параллельно с HTTP и использует то же хранилище.
	// Подсеть та же: интерсептор проверяет x-real-ip из метаданных запроса.
	var grpcSrv *grpc.Server
	grpcErr := make(chan error, 1)

	if cfg.grpcAddr != "" {
		// gRPC работает только по TLS. Если сертификат и ключ не заданы,
		// генерируется самоподписанный — этого достаточно для учебного
		// запуска; для проверки сервера агенту нужен CA (или он работает
		// без проверки сертификата).
		tlsCreds, generated, err := grpctls.ServerCredentials(cfg.grpcTLSCert, cfg.grpcTLSKey)
		if err != nil {
			return fmt.Errorf("не удалось настроить TLS gRPC: %w", err)
		}
		if generated {
			log.Warn().Msg("gRPC: файлы сертификата не заданы, сгенерирован самоподписанный TLS-сертификат")
		}

		gs, err := grpcserver.NewGRPCServer(metricsStorage, syncSave, cfg.trustedSubnet, grpc.Creds(tlsCreds))
		if err != nil {
			return err
		}

		lis, err := net.Listen("tcp", cfg.grpcAddr)
		if err != nil {
			return fmt.Errorf("не удалось запустить gRPC-сервер: %w", err)
		}

		grpcSrv = gs
		defer func() { grpcSrv.Stop() }()
		log.Info().Str("addr", lis.Addr().String()).Msg("gRPC-сервер запущен")

		go func() {
			if err := gs.Serve(lis); err != nil {
				grpcErr <- err
			}
		}()
	}

	// Ошибка запуска (например, занятый порт) не должна оставлять процесс
	// висеть в ожидании сигнала.
	srvErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr <- err
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	select {
	case sig := <-sigChan:
		// Снимаем обработчик: повторный сигнал завершит процесс сразу,
		// не дожидаясь, пока доработают активные запросы.
		signal.Stop(sigChan)
		log.Info().Str("signal", sig.String()).Msg("получен сигнал завершения")
	case err := <-srvErr:
		signal.Stop(sigChan)
		return fmt.Errorf("HTTP-сервер не смог запуститься: %w", err)
	case err := <-grpcErr:
		signal.Stop(sigChan)
		return fmt.Errorf("gRPC-сервер не смог запуститься: %w", err)
	}

	// 1. Перестаём принимать новые запросы и дожидаемся уже начатых:
	//    их данные ещё должны попасть в хранилище. Финальное сохранение
	//    имеет смысл только после этого шага.
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("не удалось дождаться активных запросов, закрываем соединения принудительно")
		_ = srv.Close()
	}

	// gRPC останавливаем мягко: GracefulStop ждёт завершения уже принятых
	// вызовов, но не дольше общего таймаута остановки.
	if grpcSrv != nil {
		stopped := make(chan struct{})
		go func() {
			grpcSrv.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-ctx.Done():
			grpcSrv.Stop()
		}
	}

	// 2. Останавливаем фоновое сохранение: дальше файл пишем только мы.
	stopPeriodic()

	// 3. Сохраняем всё, что осталось в памяти, включая данные запросов,
	//    обслуженных во время остановки.
	if cfg.databaseDSN == "" && cfg.filePath != "" {
		if memStorage, ok := metricsStorage.(*storage.MemStorage); ok {
			if err := storage.SaveToFile(memStorage, cfg.filePath); err != nil {
				return fmt.Errorf("не удалось сохранить метрики при завершении работы: %w", err)
			}
			log.Info().Str("path", cfg.filePath).Msg("метрики сохранены при завершении работы")
		}
	}

	return nil
}
