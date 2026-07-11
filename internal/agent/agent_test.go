package agent

import (
	"testing"
)

func TestCollector(t *testing.T) {
	c := NewCollector()
	if c == nil {
		t.Errorf("NewCollector() returned nil")
	}
	if c.gauge == nil {
		t.Errorf("gauge map is nil, expected empty map")
	}
	if c.counter == nil {
		t.Errorf("counter map is nil, expected empty map")
	}
	if len(c.gauge) != 0 {
		t.Errorf("gauge map should be empty, got %d items", len(c.gauge))
	}
	if len(c.counter) != 0 {
		t.Errorf("counter map should be empty, got %d items", len(c.counter))
	}
}

func TestCollector_GetGauge_SpecificMetrics(t *testing.T) {
	c := NewCollector()
	c.UpdateMetrics()

	tests := []struct {
		name    string
		metric  string
		wantMin float64
		wantMax float64
	}{
		{
			name:    "Alloc should be > 0",
			metric:  "Alloc",
			wantMin: 0,
			wantMax: 1e9,
		},
		{
			name:    "RandomValue should be between 0 and 1",
			metric:  "RandomValue",
			wantMin: 0,
			wantMax: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.GetGauge()
			value, ok := got[tt.metric]
			if !ok {
				t.Errorf("Metric %s not found in GetGauge()", tt.metric)
				return
			}
			if value < tt.wantMin || value > tt.wantMax {
				t.Errorf("GetGauge()[%s] = %f, want between %f and %f",
					tt.metric, value, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestCollector_GetCounter_PollCount(t *testing.T) {
	c := NewCollector()

	c.UpdateMetrics()
	got1 := c.GetCounter()
	count1, ok1 := got1["PollCount"]
	if !ok1 {
		t.Error("PollCount not found in GetCounter()")
	}
	if count1 != 1 {
		t.Errorf("After first update: PollCount = %d, want 1", count1)
	}

	c.UpdateMetrics()
	got2 := c.GetCounter()
	count2, ok2 := got2["PollCount"]
	if !ok2 {
		t.Error("PollCount not found after second update")
	}
	if count2 != 2 {
		t.Errorf("After second update: PollCount = %d, want 2", count2)
	}
}

func TestCollector_GetGauge_ReturnsCopy(t *testing.T) {
	c := NewCollector()
	c.UpdateMetrics()

	got := c.GetGauge()
	originalSize := len(c.gauge)

	got["test_should_not_affect_original"] = 123.45

	if len(c.gauge) != originalSize {
		t.Errorf("Original gauge map size changed from %d to %d",
			originalSize, len(c.gauge))
	}
	if _, ok := c.gauge["test_should_not_affect_original"]; ok {
		t.Error("Modifying copy affected original gauge map")
	}
}

func TestCollector_AllMetricsPresent(t *testing.T) {
	c := NewCollector()
	c.UpdateMetrics()

	expectedGaugeMetrics := []string{
		"Alloc", "BuckHashSys", "Frees", "GCCPUFraction",
		"GCSys", "HeapAlloc", "HeapIdle", "HeapInuse",
		"HeapObjects", "HeapReleased", "HeapSys", "LastGC",
		"Lookups", "MCacheInuse", "MCacheSys", "MSpanInuse",
		"MSpanSys", "Mallocs", "NextGC", "NumForcedGC",
		"NumGC", "OtherSys", "PauseTotalNs", "StackInuse",
		"StackSys", "Sys", "TotalAlloc", "RandomValue",
	}

	got := c.GetGauge()
	for _, metric := range expectedGaugeMetrics {
		if _, ok := got[metric]; !ok {
			t.Errorf("Missing gauge metric: %s", metric)
		}
	}

	counterGot := c.GetCounter()
	if _, ok := counterGot["PollCount"]; !ok {
		t.Error("Missing counter metric: PollCount")
	}
}

func TestNewAgent(t *testing.T) {
	serverAddr := "http://localhost:8080"
	pollInterval := 2
	reportInterval := 10

	a := NewAgent(serverAddr, pollInterval, reportInterval)

	if a == nil {
		t.Fatalf("NewAgent() return nil")
	}

	if a.collector == nil {
		t.Errorf("Collector is nil")
	}

	if a.serverAddr != serverAddr {
		t.Errorf("serverAddr = %s, want %s", a.serverAddr, serverAddr)
	}
	if a.pollInterval != pollInterval {
		t.Errorf("pollInterval = %d, want %d", a.pollInterval, pollInterval)
	}
	if a.reportInterval != reportInterval {
		t.Errorf("reportInterval = %d, want %d", a.reportInterval, reportInterval)
	}
}
