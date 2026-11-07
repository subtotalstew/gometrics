package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/subtotalstew/gometrics.git/internal/models"
)

// TestPollers_StopOnContextCancel проверяет, что поллеры (runtime, gopsutil,
// репортёр) корректно завершаются при отмене контекста. На Windows основной
// тест Run пропускается (нельзя послать SIGINT), поэтому остановка поллеров
// проверяется напрямую.
func TestPollers_StopOnContextCancel(t *testing.T) {
	a := NewAgent("http://unused:8080", 1, 1, 1, "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	jobs := make(chan []models.Metrics, 1)
	var wg sync.WaitGroup

	for _, run := range []func(context.Context, *sync.WaitGroup){
		a.runRuntimePoller,
		a.runPSUtilPoller,
	} {
		wg.Add(1)
		go run(ctx, &wg)
	}

	wg.Add(1)
	go a.runReporter(ctx, jobs, &wg)

	wg.Wait()

	// Поллеры успели один раз собрать метрики до выхода.
	if gauges := a.collector.GetGauge(); len(gauges) == 0 {
		t.Error("gauges are empty, pollers did not collect metrics")
	}
	if counters := a.collector.GetCounter(); counters["PollCount"] == 0 {
		t.Error("PollCount = 0, runtime poller did not run")
	}
}
