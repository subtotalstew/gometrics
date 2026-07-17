package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	models "github.com/subtotalstew/gometrics.git/internal/model"
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

	for name, value := range a.collector.GetGauge() {
		valCopy := value
		m := models.Metrics{
			ID:    name,
			MType: models.Gauge,
			Value: &valCopy,
		}
		a.sendMetricJSON(client, m)
	}

	for name, value := range a.collector.GetCounter() {
		deltaCopy := value
		m := models.Metrics{
			ID:    name,
			MType: models.Counter,
			Delta: &deltaCopy,
		}
		a.sendMetricJSON(client, m)
	}
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

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		log.Error().
			Err(err).
			Str("metric", metric.ID).
			Msg("failed to create request")
		return
	}

	req.Header.Set("Content-Type", "application/json")

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
