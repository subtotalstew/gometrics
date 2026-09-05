package agent

import (
	"bytes"
	"compress/gzip"
	"context"
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
	mu      sync.Mutex
	gauge   map[string]float64
	counter map[string]int64
}

func NewCollector() *Collector {
	return &Collector{
		gauge:   make(map[string]float64),
		counter: make(map[string]int64),
	}
}

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

func (c *Collector) GetGauge() map[string]float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[string]float64, len(c.gauge))
	for k, v := range c.gauge {
		result[k] = v
	}
	return result
}

func (c *Collector) GetCounter() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	rateLimit      int
	key            string
	client         *http.Client
}

func NewAgent(serverAddr string, pollInterval, reportInterval, rateLimit int, key string) *Agent {
	if rateLimit <= 0 {
		rateLimit = 1
	}
	return &Agent{
		collector:      NewCollector(),
		serverAddr:     serverAddr,
		pollInterval:   pollInterval,
		reportInterval: reportInterval,
		rateLimit:      rateLimit,
		key:            key,
		client:         &http.Client{Timeout: 5 * time.Second},
	}
}

func (a *Agent) Run() {
	log.Info().
		Int("poll_interval", a.pollInterval).
		Int("report_interval", a.reportInterval).
		Str("server_addr", a.serverAddr).
		Msg("starting agent")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	jobs := make(chan []models.Metrics, a.rateLimit)

	var workersWG sync.WaitGroup
	for i := 0; i < a.rateLimit; i++ {
		workersWG.Add(1)
		go a.worker(i, jobs, &workersWG)
	}

	var pollersWG sync.WaitGroup

	pollersWG.Add(1)
	go a.runRuntimePoller(ctx, &pollersWG)

	pollersWG.Add(1)
	go a.runPSUtilPoller(ctx, &pollersWG)

	pollersWG.Add(1)
	go a.runReporter(ctx, jobs, &pollersWG)

	<-sigChan
	log.Info().Msg("agent shutting down gracefully")

	cancel()
	pollersWG.Wait()

	close(jobs)
	workersWG.Wait()

	log.Info().Msg("agent stopped")
}

func (a *Agent) runRuntimePoller(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	ticker := time.NewTicker(time.Duration(a.pollInterval) * time.Second)
	defer ticker.Stop()

	a.collector.UpdateMetrics()

	for {
		select {
		case <-ticker.C:
			a.collector.UpdateMetrics()
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
	}

	for {
		select {
		case <-ticker.C:
			if err := a.collector.UpdatePSUtilMetrics(); err != nil {
				log.Error().Err(err).Msg("failed to collect gopsutil metrics")
				continue
			}
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

func (a *Agent) enqueueMetrics(ctx context.Context, jobs chan<- []models.Metrics) {
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

	if len(metrics) == 0 {
		log.Debug().Msg("нет метрик для отправки, батч пропущен")
		return
	}

	select {
	case jobs <- metrics:
	case <-ctx.Done():
	}
}

func (a *Agent) worker(id int, jobs <-chan []models.Metrics, wg *sync.WaitGroup) {
	defer wg.Done()
	for metrics := range jobs {
		log.Debug().Int("worker_id", id).Int("count", len(metrics)).Msg("worker sending batch")
		a.sendMetricsBatch(metrics)
	}
}

func (a *Agent) sendMetricsBatch(metrics []models.Metrics) {
	body, err := json.Marshal(metrics)
	if err != nil {
		log.Error().Err(err).Int("count", len(metrics)).Msg("failed to marshal metrics batch")
		return
	}

	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(body); err != nil {
		log.Error().Err(err).Msg("failed to write gzip body")
		return
	}
	if err := gz.Close(); err != nil {
		log.Error().Err(err).Msg("failed to close gzip writer")
		return
	}
	compressedBytes := compressed.Bytes()

	url := fmt.Sprintf("%s/updates/", a.serverAddr)

	sendOnce := func() error {
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

		resp, err := a.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if isRetriableStatus(resp.StatusCode) {
			return fmt.Errorf("server returned retriable status %d", resp.StatusCode)
		}

		return nil
	}

	err = retry.Do("send_metrics_batch", sendOnce, func(err error) bool {
		return isRetriableHTTPError(err)
	})

	if err != nil {
		log.Error().Err(err).Int("count", len(metrics)).Msg("failed to send metrics batch after retries")
		return
	}

	log.Info().Int("count", len(metrics)).Msg("metrics batch sent successfully")
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

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(body); err != nil {
		log.Error().Err(err).Msg("failed to write gzip body")
		return
	}
	if err := gz.Close(); err != nil {
		log.Error().Err(err).Msg("failed to close gzip writer")
		return
	}

	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		log.Error().
			Err(err).
			Str("metric", metric.ID).
			Msg("failed to create request")
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Accept-Encoding", "gzip")

	if a.key != "" {
		req.Header.Set("HashSHA256", hash.Compute(buf.Bytes(), a.key))
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
