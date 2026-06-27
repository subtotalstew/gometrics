package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

type Handler struct {
	storage storage.Storage
}

func NewHandler(storage storage.Storage) *Handler {
	return &Handler{storage: storage}
}

func (h *Handler) UpdateHandler(w http.ResponseWriter, r *http.Request) {

	for k, v := range r.Header {
		fmt.Printf("Got Header: %s with Value: %s", k, v)
		fmt.Println()
	}

	metricType := chi.URLParam(r, "type")
	metricName := chi.URLParam(r, "name")
	metricValue := chi.URLParam(r, "value")

	contentType := r.Header.Get("Content-Type")
	if contentType != "text/plain" {
		http.Error(w, "Content-Type not text/plain.", http.StatusUnsupportedMediaType)
		return
	}

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
		gauges, _ := h.storage.GetAllMetrics()
		if _, exists := gauges[metricName]; !exists {
			http.Error(w, "Metric not found", http.StatusNotFound)
			return
		}
		value := h.storage.GetGauge(metricName)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%g", value)
	case "counter":
		_, counters := h.storage.GetAllMetrics()
		if _, exists := counters[metricName]; !exists {
			http.Error(w, "Metric not found", http.StatusNotFound)
			return
		}
		value := h.storage.GetCounter(metricName)
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
