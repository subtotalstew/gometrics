package handler

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/subtotalstew/gometrics.git/internal/audit"
	"github.com/subtotalstew/gometrics.git/internal/hash"
	models "github.com/subtotalstew/gometrics.git/internal/model"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

type Handler struct {
	storage  storage.Storage
	syncSave func()
	db       *sql.DB
	key      string
	audit    *audit.Subject
}

func (h *Handler) SetKey(key string) {
	h.key = key
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

func (h *Handler) SetAudit(subject *audit.Subject) {
	h.audit = subject
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Первый адрес в цепочке — клиентский
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *Handler) notifyAudit(r *http.Request, metricNames []string) {
	if h.audit == nil || len(metricNames) == 0 {
		return
	}
	h.audit.Notify(audit.NewEvent(metricNames, clientIP(r)))
}

func (h *Handler) SetDB(db *sql.DB) {
	h.db = db
}

func (h *Handler) PingHandler(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		http.Error(w, "database not configured", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := h.db.PingContext(ctx); err != nil {
		http.Error(w, "database ping error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
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
		h.trySyncSave()
		h.notifyAudit(r, []string{metricName})
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
		h.trySyncSave()
		h.notifyAudit(r, []string{metricName})
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

const (
	rootPageGaugeHead = `<!DOCTYPE html>
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
        <tr><th>Name</th><th>Value</th></tr>`

	rootPageCounterHead = `</table>
    <h2>Counter Metrics</h2>
    <table>
        <tr><th>Name</th><th>Value</th></tr>`

	rootPageTail = `</table>
</body>
</html>`

	rootPageNoGauges   = `<tr><td colspan="2">No gauge metrics</td></tr>`
	rootPageNoCounters = `<tr><td colspan="2">No counter metrics</td></tr>`

	rootPageRowGaugeOpen = `<tr><td>`
	rootPageRowMid       = `</td><td>`
	rootPageRowClose     = `</td></tr>`
)

// rootPageSize оценивает длину HTML-страницы, чтобы собрать её без
// многократного роста буфера.
func rootPageSize(gauges, counters int) int {
	const (
		headerSize  = len(rootPageGaugeHead) + len(rootPageCounterHead) + len(rootPageTail)
		rowOverhead = len(rootPageRowGaugeOpen) + len(rootPageRowMid) + len(rootPageRowClose)
		nameSize    = 24
		valueSize   = 16
	)
	return headerSize + (gauges+counters)*(rowOverhead+nameSize+valueSize)
}

func (h *Handler) RootHandler(w http.ResponseWriter, r *http.Request) {
	gauges, counters := h.storage.GetAllMetrics()

	buf := make([]byte, 0, rootPageSize(len(gauges), len(counters)))

	buf = append(buf, rootPageGaugeHead...)
	if len(gauges) == 0 {
		buf = append(buf, rootPageNoGauges...)
	} else {
		for name, value := range gauges {
			buf = append(buf, rootPageRowGaugeOpen...)
			buf = append(buf, name...)
			buf = append(buf, rootPageRowMid...)
			buf = strconv.AppendFloat(buf, value, 'g', -1, 64)
			buf = append(buf, rootPageRowClose...)
		}
	}

	buf = append(buf, rootPageCounterHead...)
	if len(counters) == 0 {
		buf = append(buf, rootPageNoCounters...)
	} else {
		for name, value := range counters {
			buf = append(buf, rootPageRowGaugeOpen...)
			buf = append(buf, name...)
			buf = append(buf, rootPageRowMid...)
			buf = strconv.AppendInt(buf, value, 10)
			buf = append(buf, rootPageRowClose...)
		}
	}

	buf = append(buf, rootPageTail...)

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf)
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

		// Читаем и восстанавливаем тело запроса. Копию тела нельзя
		// возвращать в sync.Pool: net/http дочитывает r.Body уже после
		// возврата из хендлера.
		var bodyBytes []byte
		if r.Body != nil {
			bodyBytes, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		lrw := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(lrw, r)
		duration := time.Since(start)

		// Логгируем детали запроса и ответа. Bytes (в отличие от
		// Str(string(body))) не создаёт промежуточную строку-копию тела.
		log.Info().
			Str("uri", r.RequestURI).
			Str("method", r.Method).
			Str("duration", duration.String()).
			Int("status", lrw.status).
			Strs("req_content_type", r.Header.Values("Content-Type")).
			Strs("req_content_encoding", r.Header.Values("Content-Encoding")).
			Bytes("req_body_raw", bodyBytes).
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
		h.notifyAudit(r, []string{req.ID})

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
		h.notifyAudit(r, []string{req.ID})
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

// gzipWriterPool переиспользует gzip.Writer: создание нового writer'а
// требует ~600 КБ на словари и состояние deflate, а на каждый ответ
// создавался новый экземпляр.
var gzipWriterPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return w
	},
}

// gzipReaderPool переиспользует gzip.Reader для входящих сжатых запросов.
var gzipReaderPool = sync.Pool{
	New: func() any {
		return &gzip.Reader{}
	},
}

type compressReader struct {
	r  io.ReadCloser
	zr *gzip.Reader
}

func newCompressReader(r io.ReadCloser) (*compressReader, error) {
	zr := gzipReaderPool.Get().(*gzip.Reader)
	if err := zr.Reset(r); err != nil {
		gzipReaderPool.Put(zr)
		return nil, err
	}
	return &compressReader{r: r, zr: zr}, nil
}

func (cr compressReader) Read(p []byte) (n int, err error) {
	return cr.zr.Read(p)
}

func (cr *compressReader) Close() error {
	if cr.zr == nil {
		return cr.r.Close()
	}
	errRead := cr.r.Close()
	errGzip := cr.zr.Close()
	gzipReaderPool.Put(cr.zr)
	cr.zr = nil

	if errRead != nil {
		return errRead
	}
	return errGzip
}

func (h *Handler) GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ow := w

		acceptEncoding := r.Header.Get("Accept-Encoding")
		supportsGzip := strings.Contains(acceptEncoding, "gzip")

		if supportsGzip {
			gz := gzipWriterPool.Get().(*gzip.Writer)
			gz.Reset(w)

			defer func() {
				_ = gz.Close()
				gzipWriterPool.Put(gz)
			}()

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

func (h *Handler) UpdatesJSONHandler(w http.ResponseWriter, r *http.Request) {
	var metrics []models.Metrics

	if err := json.NewDecoder(r.Body).Decode(&metrics); err != nil {
		http.Error(w, "JSON invalid", http.StatusBadRequest)
		return
	}

	if len(metrics) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	for _, m := range metrics {
		if m.MType != models.Gauge && m.MType != models.Counter {
			http.Error(w, "invalid metric type: "+m.ID, http.StatusBadRequest)
			return
		}
		if m.MType == models.Gauge && m.Value == nil {
			http.Error(w, "missing value for gauge: "+m.ID, http.StatusBadRequest)
			return
		}
		if m.MType == models.Counter && m.Delta == nil {
			http.Error(w, "missing delta for counter: "+m.ID, http.StatusBadRequest)
			return
		}
	}

	if err := h.storage.UpdateBatch(metrics); err != nil {
		http.Error(w, "failed to update metrics", http.StatusInternalServerError)
		return
	}

	h.trySyncSave()
	names := make([]string, 0, len(metrics))
	for _, m := range metrics {
		names = append(names, m.ID)
	}
	h.notifyAudit(r, names)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) HashMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.key == "" {
			next.ServeHTTP(w, r)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusInternalServerError)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		if incoming := r.Header.Get("HashSHA256"); incoming != "" {
			expected := hash.Compute(body, h.key)
			if !hmac.Equal([]byte(incoming), []byte(expected)) {
				http.Error(w, "invalid hash", http.StatusBadRequest)
				return
			}
		}

		hrw := &hashResponseWriter{
			ResponseWriter: w,
			key:            h.key,
			status:         http.StatusOK,
		}

		next.ServeHTTP(hrw, r)

		hrw.finalize()
	})
}

type hashResponseWriter struct {
	http.ResponseWriter
	key    string
	body   bytes.Buffer
	status int
}

func (hrw *hashResponseWriter) Write(b []byte) (int, error) {
	return hrw.body.Write(b)
}

func (hrw *hashResponseWriter) WriteHeader(statusCode int) {
	hrw.status = statusCode
}

func (hrw *hashResponseWriter) finalize() {
	if hrw.key != "" && hrw.body.Len() > 0 {
		hrw.ResponseWriter.Header().Set("HashSHA256", hash.Compute(hrw.body.Bytes(), hrw.key))
	}
	hrw.ResponseWriter.WriteHeader(hrw.status)
	if hrw.body.Len() > 0 {
		_, _ = hrw.ResponseWriter.Write(hrw.body.Bytes())
	}
}
