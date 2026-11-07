// Package agent собирает метрики рантайма и системы (gopsutil) и отправляет
// их на сервер метрик батчами через POST /updates/ (gzip + подпись HMAC,
// опционально асимметричное шифрование тела запроса). В каждом запросе
// отправляется заголовок X-Real-IP с адресом хоста агента — по нему сервер
// проверяет, что агент входит в доверенную подсеть (trusted_subnet).
package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"

	"github.com/subtotalstew/gometrics.git/internal/crypto"
	"github.com/subtotalstew/gometrics.git/internal/hash"
	"github.com/subtotalstew/gometrics.git/internal/models"
	"github.com/subtotalstew/gometrics.git/internal/retry"
)

// gzipWriterPool переиспользует gzip.Writer: каждый новый экземпляр
// резервирует сотни килобайт под словари и состояние deflate, а агент
// сжимает батч на каждом цикле отправки.
var gzipWriterPool = sync.Pool{
	New: func() any {
		return gzip.NewWriter(io.Discard)
	},
}

// compressGzip сжимает data в dst, переиспользуя writer из пула.
func compressGzip(dst *bytes.Buffer, data []byte) error {
	gz := gzipWriterPool.Get().(*gzip.Writer)
	defer gzipWriterPool.Put(gz)

	gz.Reset(dst)
	if _, err := gz.Write(data); err != nil {
		return err
	}
	return gz.Close()
}

// HeaderRealIP — заголовок, в котором агент сообщает серверу свой IP-адрес.
// Сервер сверяет его с доверенной подсетью (trusted_subnet).
const HeaderRealIP = "X-Real-IP"

// HostIP возвращает IPv4-адрес хоста агента для заголовка X-Real-IP.
//
// Сначала берётся адрес, с которого ОС реально отправляет пакеты наружу
// (UDP-соединение не шлёт данных, поэтому вызов дешёвый и не требует
// доступности адреса). Если маршрута нет — первый не-loopback IPv4 из
// поднятых интерфейсов. Если внешних адресов нет вовсе, возвращается
// 127.0.0.1: сервер с подсетью 127.0.0.0/8 пустит такой запрос.
func HostIP() string {
	if conn, err := net.Dial("udp4", "8.8.8.8:80"); err == nil {
		addr := conn.LocalAddr()
		_ = conn.Close()
		if udp, ok := addr.(*net.UDPAddr); ok && udp.IP != nil {
			if ip4 := udp.IP.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}

	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, a := range addrs {
				var ip net.IP
				switch v := a.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
					continue
				}
				if ip4 := ip.To4(); ip4 != nil {
					return ip4.String()
				}
			}
		}
	}

	return "127.0.0.1"
}

// Collector накапливает метрики в памяти: gauge — текущие значения
// рантайма и системы, counter — число циклов опроса (PollCount).
// Безопасен для конкурентного использования.
//
// generate:reset
type Collector struct {
	mu      sync.Mutex
	gauge   map[string]float64
	counter map[string]int64
}

// NewCollector создаёт пустой сборщик метрик.
func NewCollector() *Collector {
	return &Collector{
		gauge:   make(map[string]float64),
		counter: make(map[string]int64),
	}
}

// UpdateMetrics считывает метрики рантайма Go (runtime.MemStats), пишет их
// в gauge-метрики и увеличивает счётчик PollCount на единицу.
// Дополнительно заполняется случайная метрика RandomValue.
func (c *Collector) UpdateMetrics() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.gauge["Alloc"] = float64(m.Alloc)
	c.gauge["BuckHashSys"] = float64(m.BuckHashSys)
	c.gauge["Frees"] = float64(m.Frees)
	c.gauge["GCCPUFraction"] = m.GCCPUFraction
	c.gauge["GCSys"] = float64(m.GCSys)
	c.gauge["HeapAlloc"] = float64(m.HeapAlloc)
	c.gauge["HeapIdle"] = float64(m.HeapIdle)
	c.gauge["HeapInuse"] = float64(m.HeapInuse)
	c.gauge["HeapObjects"] = float64(m.HeapObjects)
	c.gauge["HeapReleased"] = float64(m.HeapReleased)
	c.gauge["HeapSys"] = float64(m.HeapSys)
	c.gauge["LastGC"] = float64(m.LastGC)
	c.gauge["Lookups"] = float64(m.Lookups)
	c.gauge["MCacheInuse"] = float64(m.MCacheInuse)
	c.gauge["MCacheSys"] = float64(m.MCacheSys)
	c.gauge["MSpanInuse"] = float64(m.MSpanInuse)
	c.gauge["MSpanSys"] = float64(m.MSpanSys)
	c.gauge["Mallocs"] = float64(m.Mallocs)
	c.gauge["NextGC"] = float64(m.NextGC)
	c.gauge["NumForcedGC"] = float64(m.NumForcedGC)
	c.gauge["NumGC"] = float64(m.NumGC)
	c.gauge["OtherSys"] = float64(m.OtherSys)
	c.gauge["PauseTotalNs"] = float64(m.PauseTotalNs)
	c.gauge["StackInuse"] = float64(m.StackInuse)
	c.gauge["StackSys"] = float64(m.StackSys)
	c.gauge["Sys"] = float64(m.Sys)
	c.gauge["TotalAlloc"] = float64(m.TotalAlloc)
	c.gauge["RandomValue"] = rand.Float64()
	c.counter["PollCount"]++
}

// UpdatePSUtilMetrics добавляет системные метрики через gopsutil:
// TotalMemory, FreeMemory и CPUutilization<N> по каждому ядру.
// Ошибка означает, что часть метрик не собрана; ранее собранные
// значения при этом не затираются.
func (c *Collector) UpdatePSUtilMetrics() error {
	vm, err := mem.VirtualMemory()
	if err != nil {
		return fmt.Errorf("gopsutil: не удалось получить данные о памяти: %w", err)
	}

	// interval=0 -> мгновенный процент с момента предыдущего вызова, без блокировки.
	percentages, err := cpu.Percent(0, true)
	if err != nil {
		return fmt.Errorf("gopsutil: не удалось получить загрузку CPU: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.gauge["TotalMemory"] = float64(vm.Total)
	c.gauge["FreeMemory"] = float64(vm.Free)
	for i, p := range percentages {
		c.gauge[fmt.Sprintf("CPUutilization%d", i+1)] = p
	}

	return nil
}

// GetGauge возвращает копию текущих gauge-метрик.
func (c *Collector) GetGauge() map[string]float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[string]float64, len(c.gauge))
	for k, v := range c.gauge {
		result[k] = v
	}
	return result
}

// GetCounter возвращает копию текущих counter-метрик.
func (c *Collector) GetCounter() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[string]int64, len(c.counter))
	for k, v := range c.counter {
		result[k] = v
	}
	return result
}

// Agent собирает метрики по расписанию и отправляет их на сервер метрик
// пулом воркеров. Один Agent соответствует одному запущенному процессу агента.
type Agent struct {
	mu             sync.RWMutex
	collector      *Collector
	serverAddr     string
	pollInterval   int
	reportInterval int
	rateLimit      int
	key            string
	pubKey         *rsa.PublicKey
	hostIP         string
	client         *http.Client

	// dirty — были ли измерения после последнего снимка, поставленного
	// в очередь отправки. Нужен, чтобы при остановке не отправлять
	// повторно то, что уже ушло на сервер.
	dirty atomic.Bool

	// flushTimeout ограничивает ожидание свободного воркера при
	// завершающем сбросе снимка; по умолчанию defaultFlushTimeout.
	flushTimeout time.Duration

	// shutdownTimeout ограничивает доработку батчей воркерами после
	// сигнала; по умолчанию defaultShutdownTimeout.
	shutdownTimeout time.Duration
}

// NewAgent создаёт агент, который опрашивает метрики каждые pollInterval
// секунд, отправляет их каждые reportInterval секунд и делает не более
// rateLimit одновременных исходящих запросов. serverAddr указывается
// с схемой, например http://localhost:8080.
// Пустой key отключает подпись HashSHA256; rateLimit <= 0 трактуется как 1.
//
// Адрес хоста для заголовка X-Real-IP определяется один раз при создании
// агента (HostIP): он не меняется, а запросов много.
//
// Асимметричное шифрование включается отдельно методом SetCryptoKey.
func NewAgent(serverAddr string, pollInterval, reportInterval, rateLimit int, key string) *Agent {
	if rateLimit <= 0 {
		rateLimit = 1
	}
	return &Agent{
		collector:       NewCollector(),
		serverAddr:      serverAddr,
		pollInterval:    pollInterval,
		reportInterval:  reportInterval,
		rateLimit:       rateLimit,
		key:             key,
		hostIP:          HostIP(),
		client:          &http.Client{Timeout: 5 * time.Second},
		flushTimeout:    defaultFlushTimeout,
		shutdownTimeout: defaultShutdownTimeout,
	}
}

// SetCryptoKey включает асимметричное шифрование тела запросов: публичный
// ключ читается из файла по пути path. Пустой path оставляет шифрование
// выключенным.
//
// Метод нужно вызывать до Run: ключ читается один раз на старте, а не на
// каждом запросе, поэтому опечатка в пути видна сразу при запуске агента.
func (a *Agent) SetCryptoKey(path string) error {
	if path == "" {
		a.pubKey = nil
		return nil
	}

	pub, err := crypto.LoadPublicKey(path)
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	a.pubKey = pub
	return nil
}

// defaultFlushTimeout — сколько агент готов ждать свободного воркера, чтобы
// поставить в очередь завершающий батч при остановке. Больше ждать нет
// смысла: если воркеры заняты ретраями, сервер, скорее всего, недоступен.
const defaultFlushTimeout = 5 * time.Second

// defaultShutdownTimeout — сколько после сигнала агент даёт воркерам на
// доработку батчей, которые уже в отправке. Ограничение нужно, чтобы
// недоступный сервер не затягивал остановку: по истечении времени ретраи
// прерываются, а незавершённый батч теряется.
const defaultShutdownTimeout = 10 * time.Second

// Run запускает агент и блокируется до получения SIGINT, SIGTERM или SIGQUIT.
// Внутри поднимаются три горутины-поллера (runtime, gopsutil, отправка)
// и пул из rateLimit воркеров; при завершении они останавливаются
// по контексту, а очередь отправки закрывается.
//
// По сигналу агент не теряет уже собранные данные, но время завершения
// остаётся предсказуемым:
//
//   - поллеры останавливаются, репортёр не ждёт занятых ретраями воркеров
//     (снимок, не вставший в очередь, заменяется завершающим сбросом);
//   - последний снимок досылается в очередь с ожиданием не дольше
//     flushTimeout (если все воркеры заняты, снимок пропускается
//     с предупреждением);
//   - батчи, уже принятые воркерами, дорабатываются с ретраями, но сброс
//     и доработка вместе укладываются в общий бюджет shutdownTimeout:
//     иначе недоступный сервер держал бы агент живым всё время ретраев.
func (a *Agent) Run() {
	log.Info().
		Int("poll_interval", a.pollInterval).
		Int("report_interval", a.reportInterval).
		Str("server_addr", a.serverAddr).
		Msg("starting agent")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// sendCtx управляет отправкой батчей. При остановке он отменяется по
	// таймауту, чтобы ретраи не затягивали завершение агента.
	sendCtx, cancelSend := context.WithCancel(context.Background())
	defer cancelSend()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	jobs := make(chan []models.Metrics, a.rateLimit)

	var workersWG sync.WaitGroup
	for i := 0; i < a.rateLimit; i++ {
		workersWG.Add(1)
		go a.worker(sendCtx, i, jobs, &workersWG)
	}

	var pollersWG sync.WaitGroup

	pollersWG.Add(1)
	go a.runRuntimePoller(ctx, &pollersWG)

	pollersWG.Add(1)
	go a.runPSUtilPoller(ctx, &pollersWG)

	pollersWG.Add(1)
	go a.runReporter(ctx, jobs, &pollersWG)

	sig := <-sigChan
	// Снимаем обработчик: повторный сигнал завершит процесс сразу,
	// не дожидаясь, пока воркеры доработают повторы.
	signal.Stop(sigChan)
	log.Info().Str("signal", sig.String()).Msg("agent shutting down gracefully")

	// 1. Останавливаем поллеры: новых снимков метрик больше не будет.
	cancel()
	pollersWG.Wait()

	// Бюджет остановки общий: и завершающий сброс, и доработка батчей
	// должны уложиться в shutdownTimeout, иначе недоступный сервер
	// затягивал бы завершение агента.
	deadline := time.Now().Add(a.shutdownTimeout)

	// 2. Досылаем последний снимок: данные, собранные между двумя
	//    отчётами, иначе потерялись бы вместе с процессом. Ждём воркера
	//    не дольше flushTimeout и не дольше остатка бюджета.
	flushWait := a.flushTimeout
	if left := time.Until(deadline); left < flushWait {
		flushWait = left
	}
	a.flushMetrics(jobs, flushWait)

	// 3. Закрываем очередь: воркеры отправят всё уже поставленное.
	close(jobs)

	// 4. Ждём воркеров до конца бюджета: по его истечении отправка
	//    прерывается, чтобы ретраи не держали агент живым.
	left := time.Until(deadline)
	if left < 0 {
		left = 0
	}
	stopTimer := time.AfterFunc(left, func() {
		log.Warn().
			Dur("timeout", a.shutdownTimeout).
			Msg("воркеры не успели доработать батчи, отправка прервана")
		cancelSend()
	})
	workersWG.Wait()
	stopTimer.Stop()

	log.Info().Msg("agent stopped")
}

func (a *Agent) runRuntimePoller(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	ticker := time.NewTicker(time.Duration(a.pollInterval) * time.Second)
	defer ticker.Stop()

	a.collector.UpdateMetrics()
	a.dirty.Store(true)

	for {
		select {
		case <-ticker.C:
			a.collector.UpdateMetrics()
			a.dirty.Store(true)
			log.Debug().Msg("runtime metrics updated")
		case <-ctx.Done():
			return
		}
	}
}

func (a *Agent) runPSUtilPoller(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	ticker := time.NewTicker(time.Duration(a.pollInterval) * time.Second)
	defer ticker.Stop()

	if err := a.collector.UpdatePSUtilMetrics(); err != nil {
		log.Error().Err(err).Msg("failed to collect gopsutil metrics")
	} else {
		a.dirty.Store(true)
	}

	for {
		select {
		case <-ticker.C:
			if err := a.collector.UpdatePSUtilMetrics(); err != nil {
				log.Error().Err(err).Msg("failed to collect gopsutil metrics")
				continue
			}
			a.dirty.Store(true)
			log.Debug().Msg("gopsutil metrics updated")
		case <-ctx.Done():
			return
		}
	}
}

func (a *Agent) runReporter(ctx context.Context, jobs chan<- []models.Metrics, wg *sync.WaitGroup) {
	defer wg.Done()

	ticker := time.NewTicker(time.Duration(a.reportInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.enqueueMetrics(ctx, jobs)
		case <-ctx.Done():
			return
		}
	}
}

// enqueueMetrics ставит в очередь снимок текущих метрик.
//
// Ожидание свободного воркера прерывается отменой контекста: при остановке
// агент не должен зависеть от того, заняты ли воркеры долгими ретраями.
// Непоставленный снимок при этом не теряется — он остаётся в коллекторе, и
// его досылает flushMetrics уже со своим таймаутом.
func (a *Agent) enqueueMetrics(ctx context.Context, jobs chan<- []models.Metrics) {
	// Флаг «есть неотправленное» снимаем до сборки снимка: если поллер
	// успеет обновить метрики между сборкой и отправкой, флаг поднимется
	// снова, и при остановке эти данные всё равно будут досланы.
	a.dirty.Store(false)

	metrics := a.buildBatch()
	if len(metrics) == 0 {
		log.Debug().Msg("нет метрик для отправки, батч пропущен")
		return
	}

	select {
	case jobs <- metrics:
	case <-ctx.Done():
		// Агент останавливается: не ждём воркеров, занятых ретраями.
		// Снимок подберёт завершающий сброс в Run.
		a.dirty.Store(true)
		log.Debug().Msg("остановка: батч не поставлен в очередь, данные досошлёт завершающий сброс")
	}
}

// flushMetrics досылает последний снимок метрик перед остановкой агента.
//
// Если после последнего поставленного в очередь снимка измерений не было
// (dirty == false), отправлять нечего и flushMetrics не блокируется.
// Ожидание свободного воркера ограничено wait: доставка данных важнее
// скорости, но остановка не должна зависать, если все воркеры заняты.
func (a *Agent) flushMetrics(jobs chan<- []models.Metrics, wait time.Duration) {
	if !a.dirty.Load() {
		log.Debug().Msg("нет неотправленных метрик, завершающий сброс не нужен")
		return
	}

	metrics := a.buildBatch()
	if len(metrics) == 0 {
		return
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case jobs <- metrics:
		log.Info().Int("count", len(metrics)).Msg("завершающий батч поставлен в очередь")
	case <-timer.C:
		log.Warn().
			Int("count", len(metrics)).
			Dur("timeout", wait).
			Msg("не удалось поставить завершающий батч в очередь: воркеры заняты, снимок пропущен")
	}
}

// buildBatch собирает снимок текущих метрик в батч для отправки.
func (a *Agent) buildBatch() []models.Metrics {
	gauges := a.collector.GetGauge()
	counters := a.collector.GetCounter()

	metrics := make([]models.Metrics, 0, len(gauges)+len(counters))

	for name, value := range gauges {
		valCopy := value
		metrics = append(metrics, models.Metrics{
			ID:    name,
			MType: models.Gauge,
			Value: &valCopy,
		})
	}

	for name, value := range counters {
		deltaCopy := value
		metrics = append(metrics, models.Metrics{
			ID:    name,
			MType: models.Counter,
			Delta: &deltaCopy,
		})
	}

	return metrics
}

func (a *Agent) worker(ctx context.Context, id int, jobs <-chan []models.Metrics, wg *sync.WaitGroup) {
	defer wg.Done()
	for metrics := range jobs {
		log.Debug().Int("worker_id", id).Int("count", len(metrics)).Msg("worker sending batch")
		a.sendMetricsBatch(ctx, metrics)
	}
}

func (a *Agent) sendMetricsBatch(ctx context.Context, metrics []models.Metrics) {
	body, err := json.Marshal(metrics)
	if err != nil {
		log.Error().Err(err).Int("count", len(metrics)).Msg("failed to marshal metrics batch")
		return
	}

	payload, headers, err := a.preparePayload(body)
	if err != nil {
		log.Error().Err(err).Int("count", len(metrics)).Msg("failed to prepare metrics batch")
		return
	}

	url := fmt.Sprintf("%s/updates/", a.serverAddr)

	sendOnce := func() error {
		req, err := a.newRequest(ctx, url, payload, headers)
		if err != nil {
			return err
		}

		resp, err := a.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		return checkResponse(resp)
	}

	err = retry.DoContext(ctx, "send_metrics_batch", sendOnce, isRetriableHTTPError)

	if err != nil {
		log.Error().Err(err).Int("count", len(metrics)).Msg("failed to send metrics batch after retries")
		return
	}

	log.Info().Int("count", len(metrics)).Msg("metrics batch sent successfully")
}

// preparePayload готовит тело запроса к отправке: сжимает его gzip и, если
// асимметричное шифрование включено, упаковывает в конверт.
//
// Возвращаемые заголовки описывают именно то, что уходит в сеть. Без
// шифрования в сеть идёт gzip, поэтому Content-Encoding: gzip корректен.
// С шифрованием в сеть идёт конверт: Content-Encoding для него не ставится
// (посредник не должен пытаться распаковать шифртекст), а кодировка
// открытого текста сообщается отдельным заголовком, который сервер
// применяет уже после расшифрования.
func (a *Agent) preparePayload(body []byte) ([]byte, http.Header, error) {
	var compressed bytes.Buffer
	if err := compressGzip(&compressed, body); err != nil {
		return nil, nil, fmt.Errorf("agent: не удалось сжать тело запроса: %w", err)
	}

	payload, err := a.encryptPayload(compressed.Bytes())
	if err != nil {
		return nil, nil, err
	}

	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept-Encoding", "gzip")

	if a.pubKey == nil {
		headers.Set("Content-Encoding", "gzip")
	} else {
		headers.Set(crypto.HeaderPayloadEncoding, "gzip")
	}

	return payload, headers, nil
}

// newRequest собирает POST-запрос с готовым телом и заголовками.
// Контекст позволяет прервать попытку отправки при остановке агента.
// Подпись считается по тому, что реально уходит в сеть: при включённом
// шифровании — по шифртексту.
func (a *Agent) newRequest(ctx context.Context, url string, payload []byte, headers http.Header) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	req.Header = headers.Clone()
	// X-Real-IP нужен серверу, чтобы сверить агента с доверенной подсетью.
	// Запросы к серверу без trusted_subnet этот заголовок просто игнорируют.
	ip := a.hostIP
	if ip == "" {
		ip = HostIP()
	}
	req.Header.Set(HeaderRealIP, ip)
	if a.key != "" {
		req.Header.Set("HashSHA256", hash.Compute(payload, a.key))
	}

	return req, nil
}

// checkResponse превращает неуспешный ответ сервера в ошибку отправки.
//
// 5xx помечаются как временные (их имеет смысл повторить), остальные не-2xx
// не повторяются, но и молча считаться успехом не должны: чаще всего это
// несовпадение ключей шифрования или подписи.
func checkResponse(resp *http.Response) error {
	if isRetriableStatus(resp.StatusCode) {
		return fmt.Errorf("server returned retriable status %d", resp.StatusCode)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("server rejected metrics batch with status %d", resp.StatusCode)
	}
	return nil
}

// encryptPayload шифрует тело запроса публичным ключом, если асимметричное
// шифрование включено (SetCryptoKey). Без ключа тело уходит как есть.
func (a *Agent) encryptPayload(payload []byte) ([]byte, error) {
	if a.pubKey == nil {
		return payload, nil
	}
	encrypted, err := crypto.Encrypt(a.pubKey, payload)
	if err != nil {
		return nil, fmt.Errorf("agent: не удалось зашифровать тело запроса: %w", err)
	}
	return encrypted, nil
}

func (a *Agent) sendMetricJSON(client *http.Client, metric models.Metrics) {
	url := fmt.Sprintf("%s/update", a.serverAddr)
	body, err := json.Marshal(metric)
	if err != nil {
		log.Error().
			Err(err).
			Str("metric", metric.ID).
			Str("type", metric.MType).
			Msg("failed to marshal metric")
		return
	}

	payload, headers, err := a.preparePayload(body)
	if err != nil {
		log.Error().
			Err(err).
			Str("metric", metric.ID).
			Msg("failed to prepare metric")
		return
	}

	req, err := a.newRequest(context.Background(), url, payload, headers)
	if err != nil {
		log.Error().
			Err(err).
			Str("metric", metric.ID).
			Msg("failed to create request")
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Error().
			Err(err).
			Str("metric", metric.ID).
			Msg("failed to send metric")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Warn().
			Int("status_code", resp.StatusCode).
			Str("metric", metric.ID).
			Msg("unexpected response status")
	}
}
