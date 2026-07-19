package handler

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
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
	storage  storage.Storage
	syncSave func()
}

func NewHandler(storage storage.Storage) *Handler {
	return &Handler{storage: storage}
}

func (h *Handler) SetSyncSave(fn func()) {
	h.syncSave = fn
}

func (h *Handler) trySyncSave() {
	if h.syncSave != nil {
		h.syncSave()
	}
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
		h.trySyncSave()

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
		h.trySyncSave()

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

		// Читаем и восстанавливаем тело запроса
		var bodyBytes []byte
		if r.Body != nil {
			bodyBytes, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}

		lrw := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(lrw, r)
		duration := time.Since(start)

		// Логгируем детали запроса и ответа
		log.Info().
			Str("uri", r.RequestURI).
			Str("method", r.Method).
			Str("duration", duration.String()).
			Int("status", lrw.status).
			Interface("req_headers", map[string][]string{
				"Content-Type": r.Header["Content-Type"], "Content-Encoding": r.Header["Content-Encoding"],
			}).
			Str("req_body_raw", string(bodyBytes)).
			Msg("HTTP request processed")
	})
}

func (h *Handler) ValueJSONHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, `{"error":"unsupported content type"}`, http.StatusUnsupportedMediaType)
		return
	}

	var req models.Metrics
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(w, `{"error":"metric name is empty"}`, http.StatusBadRequest)
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(req)
}

func (h *Handler) UpdateJSONHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, `{"error":"unsupported content type"}`, http.StatusUnsupportedMediaType)
		return
	}

	var req models.Metrics
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(w, `{"error":"metric name is empty"}`, http.StatusBadRequest)
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
		h.trySyncSave()

	case models.Counter:
		if req.Delta == nil {
			http.Error(w, `{"error":"missing delta for counter"}`, http.StatusBadRequest)
			return
		}
		if err := h.storage.UpdateCounter(req.ID, *req.Delta); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.trySyncSave()
		cur, _ := h.storage.GetCounter(req.ID)
		req.Delta = &cur

	default:
		http.Error(w, `{"error":"invalid metric type"}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(req)
}

type compressWriter struct {
	http.ResponseWriter
	w io.Writer
}

func (cw *compressWriter) Write(b []byte) (int, error) {
	return cw.w.Write(b)
}

type compressReader struct {
	r  io.ReadCloser
	zr *gzip.Reader
}

func newCompressReader(r io.ReadCloser) (*compressReader, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	return &compressReader{r: r, zr: zr}, nil
}

func (cr compressReader) Read(p []byte) (n int, err error) {
	return cr.zr.Read(p)
}

func (cr *compressReader) Close() error {
	if err := cr.r.Close(); err != nil {
		return err
	}
	return cr.zr.Close()
}
func (h *Handler) GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ow := w

		acceptEncoding := r.Header.Get("Accept-Encoding")
		supportsGzip := strings.Contains(acceptEncoding, "gzip")

		if supportsGzip {
			gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer gz.Close()

			ow = &compressWriter{ResponseWriter: w, w: gz}
		}

		contentEncoding := r.Header.Get("Content-Encoding")
		sendedGzip := strings.Contains(contentEncoding, "gzip")

		if sendedGzip {
			cr, err := newCompressReader(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer cr.Close()
			r.Body = cr
		}

		finalWriter := &contentTypeCheckWriter{ResponseWriter: ow, rawWriter: w}

		next.ServeHTTP(finalWriter, r)
	})
}

type contentTypeCheckWriter struct {
	http.ResponseWriter
	rawWriter http.ResponseWriter
}

func (ctw *contentTypeCheckWriter) Write(b []byte) (int, error) {
	contentType := ctw.Header().Get("Content-Type")
	if strings.Contains(contentType, "application/json") || strings.Contains(contentType, "text/html") {
		ctw.rawWriter.Header().Set("Content-Encoding", "gzip")
	} else {
		return ctw.rawWriter.Write(b)
	}
	return ctw.ResponseWriter.Write(b)
}

func (ctw *contentTypeCheckWriter) WriteHeader(statusCode int) {
	contentType := ctw.Header().Get("Content-Type")
	if strings.Contains(contentType, "application/json") || strings.Contains(contentType, "text/html") {
		ctw.rawWriter.Header().Set("Content-Encoding", "gzip")
	}
	ctw.ResponseWriter.WriteHeader(statusCode)
}
