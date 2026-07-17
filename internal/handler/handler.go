package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	models "github.com/subtotalstew/gometrics.git/internal/model"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

type Handler struct {
	storage storage.Storage
}

func NewHandler(storage storage.Storage) *Handler {
	return &Handler{storage: storage}
}

func (h *Handler) UpdateHandler(w http.ResponseWriter, r *http.Request) {

	metricType := chi.URLParam(r, "type")
	metricName := chi.URLParam(r, "name")
	metricValue := chi.URLParam(r, "value")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not Allowed.", http.StatusMethodNotAllowed)
		return
	}

	if metricName == "" {
		http.Error(w, "Metric name is empty", http.StatusNotFound)
		return
	}

	switch metricType {
	case "gauge":
		value, err := strconv.ParseFloat(metricValue, 64)
		if err != nil {
			http.Error(w, "Invalid gauge value", http.StatusBadRequest)
			return
		}
		if err := h.storage.SetGauge(metricName, value); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)

	case "counter":
		value, err := strconv.ParseInt(metricValue, 10, 64)
		if err != nil {
			http.Error(w, "Invalid counter value", http.StatusBadRequest)
			return
		}
		if err := h.storage.UpdateCounter(metricName, value); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)

	default:
		http.Error(w, "Invalid metric type", http.StatusBadRequest)
	}
}

func (h *Handler) ValueHandler(w http.ResponseWriter, r *http.Request) {
	metricType := chi.URLParam(r, "type")
	metricName := chi.URLParam(r, "name")

	switch metricType {
	case "gauge":
		value, exists := h.storage.GetGauge(metricName)
		if !exists {
			http.Error(w, "Metric not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%g", value)
	case "counter":
		value, exists := h.storage.GetCounter(metricName)
		if !exists {
			http.Error(w, "Metric not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%d", value)

	default:
		http.Error(w, "Invalid metric type", http.StatusBadRequest)
	}
}

func (h *Handler) RootHandler(w http.ResponseWriter, r *http.Request) {
	gauges, counters := h.storage.GetAllMetrics()

	var html strings.Builder
	html.WriteString(`<!DOCTYPE html>
<html>
<head>
    <title>Metrics</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        table { border-collapse: collapse; width: 100%; max-width: 600px; }
        th, td { border: 1px solid #ddd; padding: 8px; text-align: left; }
        th { background-color: #f2f2f2; }
    </style>
</head>
<body>
    <h1>All Metrics</h1>
    <h2>Gauge Metrics</h2>
    <table>
        <tr><th>Name</th><th>Value</th></tr>`)

	if len(gauges) == 0 {
		html.WriteString(`<tr><td colspan="2">No gauge metrics</td></tr>`)
	} else {
		for name, value := range gauges {
			fmt.Fprintf(&html, `<tr><td>%s</td><td>%g</td></tr>`, name, value)
		}
	}

	html.WriteString(`</table>
    <h2>Counter Metrics</h2>
    <table>
        <tr><th>Name</th><th>Value</th></tr>`)

	if len(counters) == 0 {
		html.WriteString(`<tr><td colspan="2">No counter metrics</td></tr>`)
	} else {
		for name, value := range counters {
			fmt.Fprintf(&html, `<tr><td>%s</td><td>%d</td></tr>`, name, value)
		}
	}

	html.WriteString(`</table>
</body>
</html>`)

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html.String()))
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.status = code
	lrw.ResponseWriter.WriteHeader(code)
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
	if lrw.status == 0 {
		lrw.status = http.StatusOK
	}
	size, err := lrw.ResponseWriter.Write(b)
	lrw.size += size
	return size, err
}

func (h *Handler) LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		start := time.Now()

		lrw := &loggingResponseWriter{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

		next.ServeHTTP(lrw, r)

		duration := time.Since(start)

		log.Info().
			Str("uri", r.RequestURI).
			Str("method", r.Method).
			Str("duration", duration.String()).
			Int("status", lrw.status).
			Int("size", lrw.size).
			Msg("HTTP request processed")
	})
}

func (h *Handler) ValueJSONHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req models.Metrics
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	switch req.MType {
	case models.Gauge:
		value, exists := h.storage.GetGauge(req.ID)
		if !exists {
			http.Error(w, `{"error":"metric not found"}`, http.StatusNotFound)
			return
		}
		req.Value = &value

	case models.Counter:
		value, exists := h.storage.GetCounter(req.ID)
		if !exists {
			http.Error(w, `{"error":"metric not found"}`, http.StatusNotFound)
			return
		}
		req.Delta = &value

	default:
		http.Error(w, `{"error":"invalid metric type"}`, http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(req)
}

func (h *Handler) UpdateJSONHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req models.Metrics
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(w, `{"error":"metric name is empty"}`, http.StatusNotFound)
		return
	}

	switch req.MType {
	case models.Gauge:
		if req.Value == nil {
			http.Error(w, `{"error":"missing value for gauge"}`, http.StatusBadRequest)
			return
		}
		if err := h.storage.SetGauge(req.ID, *req.Value); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

	case models.Counter:
		if req.Delta == nil {
			http.Error(w, `{"error":"missing delta for counter"}`, http.StatusBadRequest)
			return
		}
		if err := h.storage.UpdateCounter(req.ID, *req.Delta); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// По ТЗ возвращаем обновленное суммарное значение counter
		current, _ := h.storage.GetCounter(req.ID)
		req.Delta = &current

	default:
		http.Error(w, `{"error":"invalid metric type"}`, http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(req)
}
