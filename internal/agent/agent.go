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
	models "github.com/subtotalstew/gometrics.git/internal/model"
	"github.com/subtotalstew/gometrics.git/internal/retry"
)

type Collector struct {
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

func (c *Collector) GetGauge() map[string]float64 {
	result := make(map[string]float64)
	for k, v := range c.gauge {
		result[k] = v
	}
	return result
}

func (c *Collector) GetCounter() map[string]int64 {
	result := make(map[string]int64)
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
}

func NewAgent(serverAddr string, pollInterval, reportInterval int) *Agent {
	return &Agent{
		collector:      NewCollector(),
		serverAddr:     serverAddr,
		pollInterval:   pollInterval,
		reportInterval: reportInterval,
	}
}

func (a *Agent) Run() {
	log.Info().
		Int("poll_interval", a.pollInterval).
		Int("report_interval", a.reportInterval).
		Str("server_addr", a.serverAddr).
		Msg("starting agent")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	pollTicker := time.NewTicker(time.Duration(a.pollInterval) * time.Second)
	defer pollTicker.Stop()

	reportTicker := time.NewTicker(time.Duration(a.reportInterval) * time.Second)
	defer reportTicker.Stop()

	a.collector.UpdateMetrics()
	a.sendMetrics()
	log.Info().Msg("initial metrics collected and sent")

	for {
		select {
		case <-pollTicker.C:
			a.collector.UpdateMetrics()
			log.Debug().
				Int64("poll_count", a.collector.counter["PollCount"]).
				Msg("metrics updated")
		case <-reportTicker.C:
			a.sendMetrics()
			log.Info().Msg("metrics sent to server")
		case signal := <-sigChan:
			log.Info().
				Str("signal", signal.String()).
				Msg("agent shutting down gracefully")
			return
		}
	}
}

func (a *Agent) sendMetrics() {
	client := &http.Client{Timeout: 5 * time.Second}
	a.sendMetricsBatch(client)
}

func (a *Agent) sendMetricsBatch(client *http.Client) {
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

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if isRetriableStatus(resp.StatusCode) {
			return fmt.Errorf("server returned retriable status %d", resp.StatusCode)
		}

		if resp.StatusCode != http.StatusOK {
			return nil
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

	log.Debug().
		Str("metric_id", metric.ID).
		Int("original_size", len(body)).
		Int("compressed_size", buf.Len()).
		Interface("headers", req.Header).
		Msg("sending outgoing agent request")

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
