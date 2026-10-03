// Package loadgen генерирует HTTP-нагрузку на сервис метрик.
// Он используется для снятия профилей памяти (pprof) на «горячем» сервисе
// с реалистичным непустым набором метрик.
package loadgen

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/subtotalstew/gometrics.git/internal/hash"
	models "github.com/subtotalstew/gometrics.git/internal/model"
)

// Config описывает параметры нагрузки.
type Config struct {
	// Addr — базовый адрес сервиса, например http://127.0.0.1:8080.
	Addr string
	// Workers — количество параллельных клиентов.
	Workers int
	// Duration — длительность фазы нагрузки (сидирование не входит).
	Duration time.Duration
	// SeedMetrics — сколько уникальных метрик создать перед нагрузкой.
	SeedMetrics int
	// BatchSize — размер батча в запросе POST /updates.
	BatchSize int
	// Requests — если больше нуля, нагрузка останавливается после
	// указанного числа запросов (детерминированный прогон для
	// сравнения профилей).
	Requests int
	// Key — ключ подписи (HashSHA256); пусто = подпись не используется.
	Key string
}

// Stats — результат прогона.
type Stats struct {
	Requests     int64 `json:"requests"`
	Errors       int64 `json:"errors"`
	UpdateBatch  int64 `json:"update_batch"`
	ValueJSON    int64 `json:"value_json"`
	ValuePath    int64 `json:"value_path"`
	RootPage     int64 `json:"root_page"`
	BytesSent    int64 `json:"bytes_sent"`
	SeedRequests int64 `json:"seed_requests"`
}

func (c Config) withDefaults() Config {
	if c.Workers <= 0 {
		c.Workers = 4
	}
	if c.Duration <= 0 && c.Requests <= 0 {
		c.Duration = 10 * time.Second
	}
	if c.SeedMetrics <= 0 {
		c.SeedMetrics = 1000
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 100
	}
	return c
}

func metricName(i int) string {
	return fmt.Sprintf("metric_%d", i)
}

func metricType(i int) string {
	if i%2 == 0 {
		return models.Gauge
	}
	return models.Counter
}

// buildBatch собирает батч метрик с индексами [from, to).
func buildBatch(from, to int) []models.Metrics {
	metrics := make([]models.Metrics, 0, to-from)
	for i := from; i < to; i++ {
		if metricType(i) == models.Gauge {
			v := float64(i) * 1.5
			metrics = append(metrics, models.Metrics{ID: metricName(i), MType: models.Gauge, Value: &v})
		} else {
			d := int64(i % 97)
			metrics = append(metrics, models.Metrics{ID: metricName(i), MType: models.Counter, Delta: &d})
		}
	}
	return metrics
}

func newClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        cfgMaxIdleConns,
			MaxIdleConnsPerHost: cfgMaxIdleConns,
			IdleConnTimeout:     30 * time.Second,
		},
	}
}

const cfgMaxIdleConns = 128

type counters struct {
	requests    atomic.Int64
	errors      atomic.Int64
	updateBatch atomic.Int64
	valueJSON   atomic.Int64
	valuePath   atomic.Int64
	rootPage    atomic.Int64
	bytesSent   atomic.Int64
	seed        atomic.Int64

	limit  int64
	cancel context.CancelFunc
}

// Run выполняет сидирование метрик и затем держит нагрузку до истечения
// Duration, достижения Requests или отмены контекста.
func Run(ctx context.Context, cfg Config) (Stats, error) {
	cfg = cfg.withDefaults()
	if cfg.Addr == "" {
		return Stats{}, fmt.Errorf("loadgen: не задан адрес сервиса")
	}

	c := &counters{}
	client := newClient()
	defer client.CloseIdleConnections()

	if err := seed(ctx, client, cfg, c); err != nil {
		return Stats{}, err
	}

	loadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.limit = int64(cfg.Requests)
	c.cancel = cancel

	if cfg.Duration > 0 {
		timer := time.AfterFunc(cfg.Duration, cancel)
		defer timer.Stop()
	}

	var wg sync.WaitGroup
	for w := 0; w < cfg.Workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(seed))
			for loadCtx.Err() == nil {
				doRequest(loadCtx, client, cfg, c, rnd)
			}
		}(int64(w)*7919 + 13)
	}
	wg.Wait()

	return Stats{
		Requests:     c.requests.Load(),
		Errors:       c.errors.Load(),
		UpdateBatch:  c.updateBatch.Load(),
		ValueJSON:    c.valueJSON.Load(),
		ValuePath:    c.valuePath.Load(),
		RootPage:     c.rootPage.Load(),
		BytesSent:    c.bytesSent.Load(),
		SeedRequests: c.seed.Load(),
	}, nil
}

func seed(ctx context.Context, client *http.Client, cfg Config, c *counters) error {
	for from := 0; from < cfg.SeedMetrics; from += cfg.BatchSize {
		to := from + cfg.BatchSize
		if to > cfg.SeedMetrics {
			to = cfg.SeedMetrics
		}
		if err := postBatch(ctx, client, cfg, buildBatch(from, to), c); err != nil {
			return fmt.Errorf("loadgen: сидирование метрик: %w", err)
		}
		c.seed.Add(1)
	}
	return nil
}

func doRequest(ctx context.Context, client *http.Client, cfg Config, c *counters, rnd *rand.Rand) {
	switch roll := rnd.Intn(100); {
	case roll < 55:
		from := rnd.Intn(cfg.SeedMetrics)
		to := from + cfg.BatchSize
		if to > cfg.SeedMetrics {
			to = cfg.SeedMetrics
		}
		if err := postBatch(ctx, client, cfg, buildBatch(from, to), c); err != nil {
			return
		}
		c.updateBatch.Add(1)

	case roll < 75:
		idx := rnd.Intn(cfg.SeedMetrics)
		body, err := json.Marshal(models.Metrics{ID: metricName(idx), MType: metricType(idx)})
		if err != nil {
			c.errors.Add(1)
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Addr+"/value", bytes.NewReader(body))
		if err != nil {
			c.errors.Add(1)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		do(ctx, client, req, c)
		c.valueJSON.Add(1)

	case roll < 85:
		idx := rnd.Intn(cfg.SeedMetrics)
		url := cfg.Addr + "/value/" + metricType(idx) + "/" + metricName(idx)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			c.errors.Add(1)
			return
		}
		do(ctx, client, req, c)
		c.valuePath.Add(1)

	default:
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Addr+"/", nil)
		if err != nil {
			c.errors.Add(1)
			return
		}
		do(ctx, client, req, c)
		c.rootPage.Add(1)
	}
}

func postBatch(ctx context.Context, client *http.Client, cfg Config, metrics []models.Metrics, c *counters) error {
	body, err := json.Marshal(metrics)
	if err != nil {
		c.errors.Add(1)
		return err
	}

	var buf bytes.Buffer
	buf.Grow(len(body) / 2)
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(body); err != nil {
		c.errors.Add(1)
		return err
	}
	if err := gz.Close(); err != nil {
		c.errors.Add(1)
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Addr+"/updates/", bytes.NewReader(buf.Bytes()))
	if err != nil {
		c.errors.Add(1)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Accept-Encoding", "gzip")
	if cfg.Key != "" {
		req.Header.Set("HashSHA256", hash.Compute(buf.Bytes(), cfg.Key))
	}

	// Возвращаем ошибку транспорта: на этапе сидирования это означает,
	// что сервис недоступен и нагрузку запускать бессмысленно.
	return do(ctx, client, req, c)
}

func do(ctx context.Context, client *http.Client, req *http.Request, c *counters) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		// Отмена по истечении времени нагрузки — не ошибка сервиса.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.errors.Add(1)
		return err
	}
	defer resp.Body.Close()

	c.requests.Add(1)
	c.bytesSent.Add(req.ContentLength)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		c.errors.Add(1)
	}
	// Тело нужно вычитать полностью, чтобы соединение вернулось в пул.
	_, _ = io.Copy(io.Discard, resp.Body)

	if c.limit > 0 && c.requests.Load() >= c.limit && c.cancel != nil {
		c.cancel()
	}
	return nil
}
