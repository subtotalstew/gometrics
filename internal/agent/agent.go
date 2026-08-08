package agent

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/subtotalstew/gometrics.git/internal/hash"
	models "github.com/subtotalstew/gometrics.git/internal/model"
	"github.com/subtotalstew/gometrics.git/internal/retry"
)

type Collector struct {
	mu      sync.RWMutex
	gauge   map[string]float64
	counter map[string]int64
}

func NewCollector() *Collector {
	return &Collector{
		gauge:   make(map[string]float64),
		counter: make(map[string]int64),
	}
}

// UpdateMetrics собирает runtime-метрики.
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

// UpdateGopsutilMetrics собирает метрики памяти и CPU через gopsutil.
func (c *Collector) UpdateGopsutilMetrics() {
	v, err := mem.VirtualMemory()
	if err != nil {
		log.Error().Err(err).Msg("failed to get virtual memory stats")
		return
	}

	cpuPercents, err := cpu.Percent(0, true)
	if err != nil {
		log.Error().Err(err).Msg("failed to get cpu stats")
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.gauge["TotalMemory"] = float64(v.Total)
	c.gauge["FreeMemory"] = float64(v.Free)

	for i, percent := range cpuPercents {
		c.gauge[fmt.Sprintf("CPUutilization%d", i+1)] = percent
	}
}

func (c *Collector) GetGauge() map[string]float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]float64, len(c.gauge))
	for k, v := range c.gauge {
		result[k] = v
	}
	return result
}

func (c *Collector) GetCounter() map[string]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]int64, len(c.counter))
	for k, v := range c.counter {
		result[k] = v
	}
	return result
}

type Agent struct {
	mu             sync.RWMutex
	collector      *Collector
	serverAddr     string
	pollInterval   int
	reportInterval int
	key            string
	rateLimit      int
	jobs           chan []models.Metrics
}

func NewAgent(serverAddr string, pollInterval, reportInterval int, key string, rateLimit int) *Agent {
	if rateLimit <= 0 {
		rateLimit = 1
	}
	return &Agent{
		collector:      NewCollector(),
		serverAddr:     serverAddr,
		pollInterval:   pollInterval,
		reportInterval: reportInterval,
		key:            key,
		rateLimit:      rateLimit,
		jobs:           make(chan []models.Metrics, rateLimit),
	}
}

func (a *Agent) Run() {
	log.Info().
		Int("poll_interval", a.pollInterval).
		Int("report_interval", a.reportInterval).
		Int("rate_limit", a.rateLimit).
		Str("server_addr", a.serverAddr).
		Msg("starting agent")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Запускаем worker pool — rateLimit воркеров читают из jobs и отправляют батчи.
	var wg sync.WaitGroup
	for i := 0; i < a.rateLimit; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.worker()
		}()
	}

	pollTicker := time.NewTicker(time.Duration(a.pollInterval) * time.Second)
	defer pollTicker.Stop()

	reportTicker := time.NewTicker(time.Duration(a.reportInterval) * time.Second)
	defer reportTicker.Stop()

	// Горутина 1: сбор runtime-метрик.
	go func() {
		for range pollTicker.C {
			a.collector.UpdateMetrics()
			log.Debug().Msg("runtime metrics updated")
		}
	}()

	// Горутина 2: сбор gopsutil-метрик (память, CPU).
	go func() {
		for range pollTicker.C {
			a.collector.UpdateGopsutilMetrics()
			log.Debug().Msg("gopsutil metrics updated")
		}
	}()

	// Основной цикл: по тикеру формируем батч и кладём в jobs для воркеров.
	for {
		select {
		case <-reportTicker.C:
			batch := a.collectBatch()
			if len(batch) > 0 {
				a.jobs <- batch
			}
		case sig := <-sigChan:
			log.Info().Str("signal", sig.String()).Msg("agent shutting down gracefully")
			// Закрываем канал — воркеры завершатся после обработки оставшихся батчей.
			close(a.jobs)
			wg.Wait()
			return
		}
	}
}

// collectBatch снимает текущий снапшот метрик и формирует батч для отправки.
func (a *Agent) collectBatch() []models.Metrics {
	gauges := a.collector.GetGauge()
	counters := a.collector.GetCounter()

	metrics := make([]models.Metrics, 0, len(gauges)+len(counters))

	for name, value := range gauges {
		v := value
		metrics = append(metrics, models.Metrics{
			ID:    name,
			MType: models.Gauge,
			Value: &v,
		})
	}

	for name, value := range counters {
		d := value
		metrics = append(metrics, models.Metrics{
			ID:    name,
			MType: models.Counter,
			Delta: &d,
		})
	}

	return metrics
}

// worker читает батчи из канала jobs и отправляет их на сервер.
// Завершается когда канал закрыт.
func (a *Agent) worker() {
	client := &http.Client{Timeout: 5 * time.Second}

	for batch := range a.jobs {
		if err := a.sendBatch(client, batch); err != nil {
			log.Error().Err(err).Msg("worker failed to send batch")
		}
	}
}

// sendBatch сжимает батч метрик и отправляет на сервер с retry-логикой.
func (a *Agent) sendBatch(client *http.Client, metrics []models.Metrics) error {
	body, err := json.Marshal(metrics)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(body); err != nil {
		return fmt.Errorf("gzip write: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("gzip close: %w", err)
	}
	compressedBytes := buf.Bytes()

	url := fmt.Sprintf("%s/updates/", a.serverAddr)

	return retry.Do("send_batch", func() error {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(compressedBytes))
		if err != nil {
			return err
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Encoding", "gzip")
		req.Header.Set("Accept-Encoding", "gzip")

		if a.key != "" {
			req.Header.Set("HashSHA256", hash.Compute(compressedBytes, a.key))
		}

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if isRetriableStatus(resp.StatusCode) {
			return fmt.Errorf("%w: status %d", ErrRetriableStatus, resp.StatusCode)
		}

		return nil
	}, isRetriableHTTPError)
}
