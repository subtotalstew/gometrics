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
	fmt.Println("Starting agent...")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	pollTicker := time.NewTicker(time.Duration(a.pollInterval) * time.Second)
	defer pollTicker.Stop()

	reportTicker := time.NewTicker(time.Duration(a.reportInterval) * time.Second)
	defer reportTicker.Stop()

	a.collector.UpdateMetrics()
	a.sendMetrics()
	fmt.Println("Initial metrics collected and sent")

	for {
		select {
		case <-pollTicker.C:
			a.collector.UpdateMetrics()
			fmt.Printf("Metrics updated. PollCount: %d\n", a.collector.counter["PollCount"])
		case <-reportTicker.C:
			a.sendMetrics()
			fmt.Printf("Metrics sent to server\n")
		case signal := <-sigChan:
			fmt.Printf("Received signal %v. Agent is shutting down gracefully...\n", signal)
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
		fmt.Printf("Error marshaling metric %s: %v\n", metric.ID, err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		fmt.Printf("Error creating request for %s: %v\n", metric.ID, err)
		return
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error sending metric %s: %v\n", metric.ID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Unexpected status code for %s: %d\n", metric.ID, resp.StatusCode)
	}
}
