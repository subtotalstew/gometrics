package handler

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/subtotalstew/gometrics.git/internal/crypto"
	"github.com/subtotalstew/gometrics.git/internal/hash"
	"github.com/subtotalstew/gometrics.git/internal/models"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

func TestSetGauge(t *testing.T) {
	s := storage.NewMemStorage()
	s.SetGauge("Test", 55)

	val, exists := s.GetGauge("Test")
	assert.True(t, exists, "Метрика должна существовать")
	assert.Equal(t, float64(55), val)
}

func TestUpdateCounter(t *testing.T) {
	s := storage.NewMemStorage()
	s.UpdateCounter("Test", 1)

	val, exists := s.GetCounter("Test")
	assert.True(t, exists, "Метрика должна существовать")
	assert.Equal(t, int64(1), val)
}

func TestUpdateHandler(t *testing.T) {
	type requestdata struct {
		method      string
		contentType string
		url         string
	}
	type responsedata struct {
		code        int
		response    string
		contentType string
	}

	tests := []struct {
		name         string
		requestdata  requestdata
		responsedata responsedata
	}{
		// {
		// 	name: "check Content-Type, negative scenario.",
		// 	responsedata: responsedata{
		// 		code:        http.StatusUnsupportedMediaType,
		// 		response:    "",
		// 		contentType: "",
		// 	},
		// 	requestdata: requestdata{
		// 		method:      http.MethodPost,
		// 		contentType: "application/json",
		// 		url:         "/update/counter/TestMetric/1",
		// 	},
		// },
		// {
		// 	name: "check Content-Type, positive scenario.",
		// 	responsedata: responsedata{
		// 		code:        http.StatusOK,
		// 		response:    "",
		// 		contentType: "",
		// 	},
		// 	requestdata: requestdata{
		// 		method:      http.MethodPost,
		// 		contentType: "text/plain",
		// 		url:         "/update/gauge/testSetGet57/734487.733",
		// 	},
		// },
		{
			name: "check Method, negative",
			responsedata: responsedata{
				code:        http.StatusMethodNotAllowed,
				response:    "",
				contentType: "",
			},
			requestdata: requestdata{
				method:      http.MethodGet,
				contentType: "text/plain",
				url:         "/update/counter/TestMetric/1",
			},
		},
		{
			name: "check URL, negative",
			responsedata: responsedata{
				code:        http.StatusNotFound,
				response:    "",
				contentType: "",
			},
			requestdata: requestdata{
				method:      http.MethodPost,
				contentType: "text/plain",
				url:         "/update/counter/TestMetric/1/2",
			},
		},
		{
			name: "check metricType, negative",
			responsedata: responsedata{
				code:        http.StatusBadRequest,
				response:    "",
				contentType: "",
			},
			requestdata: requestdata{
				method:      http.MethodPost,
				contentType: "text/plain",
				url:         "/update/sdsdsd/TestMetric/1",
			},
		},
		{
			name: "check metricName, negative",
			responsedata: responsedata{
				code:        http.StatusNotFound,
				response:    "",
				contentType: "",
			},
			requestdata: requestdata{
				method:      http.MethodPost,
				contentType: "text/plain",
				url:         "/update/counter//1",
			},
		},
		{
			name: "check counter, negative",
			responsedata: responsedata{
				code:        http.StatusBadRequest,
				response:    "",
				contentType: "",
			},
			requestdata: requestdata{
				method:      http.MethodPost,
				contentType: "text/plain",
				url:         "/update/counter/SomeCount/one",
			},
		},
		{
			name: "check Gauge, negative",
			responsedata: responsedata{
				code:        http.StatusBadRequest,
				response:    "",
				contentType: "",
			},
			requestdata: requestdata{
				method:      http.MethodPost,
				contentType: "text/plain",
				url:         "/update/gauge/SomeGauge/one",
			},
		},
		{
			name: "Check counter",
			responsedata: responsedata{
				code:        http.StatusOK,
				response:    "",
				contentType: "",
			},
			requestdata: requestdata{
				method:      http.MethodPost,
				contentType: "text/plain",
				url:         "/update/counter/SomeCounter/1",
			},
		},
		{
			name: "Check Gauge",
			responsedata: responsedata{
				code:        http.StatusOK,
				response:    "",
				contentType: "",
			},
			requestdata: requestdata{
				method:      http.MethodPost,
				contentType: "text/plain",
				url:         "/update/gauge/SomeCounter/1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.requestdata.method, tt.requestdata.url, nil)

			request.Header.Add("Content-Type", tt.requestdata.contentType)

			w := httptest.NewRecorder()

			s := storage.NewMemStorage()

			h := NewHandler(s)

			r := chi.NewRouter()
			r.Post("/update/{type}/{name}/{value}", h.UpdateHandler)
			r.ServeHTTP(w, request)

			res := w.Result()
			defer res.Body.Close()

			assert.Equal(t, tt.responsedata.code, res.StatusCode)

			resBody, err := io.ReadAll(res.Body)
			require.NoError(t, err)

			if tt.responsedata.response != "" {
				assert.Equal(t, tt.responsedata.response, string(resBody))
			}

			if tt.responsedata.contentType != "" {
				assert.Equal(t, tt.responsedata.contentType, res.Header.Get("Content-Type"))
			}
		})
	}
}

func TestValueHandler(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		setupStore func(s *storage.MemStorage)
		wantCode   int
		wantBody   string
	}{
		{
			name: "Get existing gauge",
			url:  "/value/gauge/test_gauge",
			setupStore: func(s *storage.MemStorage) {
				s.SetGauge("test_gauge", 123.456)
			},
			wantCode: http.StatusOK,
			wantBody: "123.456",
		},
		{
			name:       "Get non-existing gauge",
			url:        "/value/gauge/missing_gauge",
			setupStore: func(s *storage.MemStorage) {},
			wantCode:   http.StatusNotFound,
			wantBody:   "Metric not found\n",
		},
		{
			name: "Get existing counter",
			url:  "/value/counter/test_counter",
			setupStore: func(s *storage.MemStorage) {
				s.UpdateCounter("test_counter", 42)
			},
			wantCode: http.StatusOK,
			wantBody: "42",
		},
		{
			name: "Invalid metric type",
			url:  "/value/unknown_type/test",
			setupStore: func(s *storage.MemStorage) {
			},
			wantCode: http.StatusBadRequest,
			wantBody: "Invalid metric type\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.url, nil)
			w := httptest.NewRecorder()
			s := storage.NewMemStorage()

			if tt.setupStore != nil {
				tt.setupStore(s)
			}

			h := NewHandler(s)
			r := chi.NewRouter()
			r.Get("/value/{type}/{name}", h.ValueHandler)
			r.ServeHTTP(w, request)

			res := w.Result()
			defer res.Body.Close()

			assert.Equal(t, tt.wantCode, res.StatusCode)

			resBody, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			assert.Equal(t, tt.wantBody, string(resBody))
		})
	}
}

func TestUpdateJSONHandler_Gauge(t *testing.T) {
	s := storage.NewMemStorage()
	h := NewHandler(s)

	body := `{"id":"LastGC","type":"gauge","value":1744184459}`
	req := httptest.NewRequest(http.MethodPost, "/update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Post("/update", h.UpdateJSONHandler)
	r.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json", res.Header.Get("Content-Type"))

	got, ok := s.GetGauge("LastGC")
	require.True(t, ok)
	assert.Equal(t, 1744184459.0, got)
}

func TestUpdateJSONHandler_Counter(t *testing.T) {
	s := storage.NewMemStorage()
	h := NewHandler(s)

	body := `{"id":"PollCount","type":"counter","delta":5}`
	req := httptest.NewRequest(http.MethodPost, "/update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Post("/update", h.UpdateJSONHandler)
	r.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json", res.Header.Get("Content-Type"))

	got, ok := s.GetCounter("PollCount")
	require.True(t, ok)
	assert.Equal(t, int64(5), got)
}

func TestValueJSONHandler(t *testing.T) {
	s := storage.NewMemStorage()
	_ = s.SetGauge("LastGC", 1744184459)
	h := NewHandler(s)

	body := `{"id":"LastGC","type":"gauge"}`
	req := httptest.NewRequest(http.MethodPost, "/value", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Post("/value", h.ValueJSONHandler)
	r.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json", res.Header.Get("Content-Type"))

	var got models.Metrics
	require.NoError(t, json.NewDecoder(res.Body).Decode(&got))
	assert.Equal(t, "LastGC", got.ID)
	assert.Equal(t, "gauge", got.MType)
	require.NotNil(t, got.Value)
	assert.Equal(t, 1744184459.0, *got.Value)
}

func compressData(t *testing.T, data []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, err := gz.Write(data)
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func TestGzipMiddleware_DecompressRequest(t *testing.T) {
	s := storage.NewMemStorage()
	h := NewHandler(s)
	r := chi.NewRouter()
	r.Use(h.GzipMiddleware)
	r.Post("/update", h.UpdateJSONHandler)

	jsonBody := `{"id":"TestGzipGauge","type":"gauge","value":42.42}`
	compressedBody := compressData(t, []byte(jsonBody))

	req := httptest.NewRequest(http.MethodPost, "/update", bytes.NewReader(compressedBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
	val, _ := s.GetGauge("TestGzipGauge")
	assert.Equal(t, 42.42, val)
}

func TestGzipMiddleware_CompressJSONResponse(t *testing.T) {
	s := storage.NewMemStorage()
	_ = s.SetGauge("TestGzipGauge", 100.5)
	h := NewHandler(s)
	r := chi.NewRouter()
	r.Use(h.GzipMiddleware)
	r.Post("/value", h.ValueJSONHandler)

	req := httptest.NewRequest(http.MethodPost, "/value", strings.NewReader(`{"id":"TestGzipGauge","type":"gauge"}`))
	req.Header.Set("Content-Type", "application/json") // <-- ДОБАВИТЬ ЭТУ СТРОКУ
	req.Header.Set("Accept-Encoding", "gzip")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
}

func TestPingHandler_NoDB(t *testing.T) {
	h := NewHandler(storage.NewMemStorage())

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()

	h.PingHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("PingHandler() status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestUpdatesJSONHandler(t *testing.T) {
	s := storage.NewMemStorage()
	h := NewHandler(s)

	gaugeVal := 42.5
	counterVal := int64(7)

	metrics := []models.Metrics{
		{ID: "TestGauge", MType: "gauge", Value: &gaugeVal},
		{ID: "TestCounter", MType: "counter", Delta: &counterVal},
	}

	body, err := json.Marshal(metrics)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/updates/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Post("/updates/", h.UpdatesJSONHandler)
	r.ServeHTTP(w, req)

	res := w.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)

	gauge, ok := s.GetGauge("TestGauge")
	require.True(t, ok)
	assert.Equal(t, 42.5, gauge)

	counter, ok := s.GetCounter("TestCounter")
	require.True(t, ok)
	assert.Equal(t, int64(7), counter)
}

func TestUpdatesJSONHandler_EmptyBatch(t *testing.T) {
	s := storage.NewMemStorage()
	h := NewHandler(s)

	req := httptest.NewRequest(http.MethodPost, "/updates/", strings.NewReader("[]"))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Post("/updates/", h.UpdatesJSONHandler)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func TestUpdatesJSONHandler_InvalidJSON(t *testing.T) {
	s := storage.NewMemStorage()
	h := NewHandler(s)

	req := httptest.NewRequest(http.MethodPost, "/updates/", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Post("/updates/", h.UpdatesJSONHandler)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{
			name:       "RemoteAddr with port",
			remoteAddr: "192.168.0.42:12345",
			want:       "192.168.0.42",
		},
		{
			name:       "RemoteAddr without port",
			remoteAddr: "192.168.0.42",
			want:       "192.168.0.42",
		},
		{
			name:       "X-Forwarded-For single",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Forwarded-For": "192.168.0.42"},
			want:       "192.168.0.42",
		},
		{
			name:       "X-Forwarded-For chain",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Forwarded-For": "192.168.0.42, 10.0.0.2, 10.0.0.3"},
			want:       "192.168.0.42",
		},
		{
			name:       "X-Real-IP",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Real-IP": "192.168.0.42"},
			want:       "192.168.0.42",
		},
		{
			name:       "X-Forwarded-For takes precedence over X-Real-IP",
			remoteAddr: "10.0.0.1:12345",
			headers: map[string]string{
				"X-Forwarded-For": "192.168.0.42",
				"X-Real-IP":       "10.0.0.99",
			},
			want: "192.168.0.42",
		},
		{
			name:       "IPv6 RemoteAddr",
			remoteAddr: "[2001:db8::1]:12345",
			want:       "2001:db8::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			got := clientIP(req)
			if got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
func TestHandler_SetDB(t *testing.T) {
	t.Parallel()
	h := NewHandler(storage.NewMemStorage())
	h.SetDB(nil)
}

func TestHandler_SetSyncSave(t *testing.T) {
	t.Parallel()

	h := NewHandler(storage.NewMemStorage())

	called := false
	h.SetSyncSave(func() { called = true })

	// SetSyncSave должен только сохранить callback, но не вызывать его.
	if called {
		t.Error("SetSyncSave invoked callback during registration")
	}
}

// --- асимметричное шифрование ---

var (
	handlerKeyOnce sync.Once
	handlerKey     *rsa.PrivateKey
	handlerKeyErr  error
)

// testPrivateKey возвращает один RSA-ключ на все тесты пакета: генерация
// ключа заметно дороже самих тестов.
func testPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	handlerKeyOnce.Do(func() {
		handlerKey, handlerKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	require.NoError(t, handlerKeyErr)
	return handlerKey
}

// writePrivateKeyFile сохраняет приватный ключ в PEM-файл указанного формата
// и возвращает путь к нему.
func writePrivateKeyFile(t *testing.T, blockType string, der []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "private.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

// cryptoRouter собирает роутер в том же порядке middleware, что и сервер:
// подпись → расшифрование → gzip.
func cryptoRouter(h *Handler, key string) *chi.Mux {
	r := chi.NewRouter()
	if key != "" {
		h.SetKey(key)
		r.Use(h.HashMiddleware)
	}
	r.Use(h.DecryptMiddleware)
	r.Use(h.GzipMiddleware)
	r.Post("/updates/", h.UpdatesJSONHandler)
	r.Get("/", h.RootHandler)
	return r
}

// TestHandler_SetCryptoKey проверяет загрузку приватного ключа и ошибки на
// пустом и несуществующем пути.
func TestHandler_SetCryptoKey(t *testing.T) {
	h := NewHandler(storage.NewMemStorage())

	require.NoError(t, h.SetCryptoKey(""))
	assert.Nil(t, h.cryptoKey, "пустой путь должен отключать расшифрование")

	require.Error(t, h.SetCryptoKey(filepath.Join(t.TempDir(), "absent.pem")))
	assert.Nil(t, h.cryptoKey, "после ошибки ключ не должен устанавливаться")

	priv := testPrivateKey(t)
	path := writePrivateKeyFile(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(priv))
	require.NoError(t, h.SetCryptoKey(path))
	assert.NotNil(t, h.cryptoKey)
}

// TestHandler_SetCryptoKey_PKCS8 проверяет, что ключ в формате PKCS#8
// (его по умолчанию пишет OpenSSL 3.x) тоже принимается.
func TestHandler_SetCryptoKey_PKCS8(t *testing.T) {
	priv := testPrivateKey(t)

	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)

	h := NewHandler(storage.NewMemStorage())
	require.NoError(t, h.SetCryptoKey(writePrivateKeyFile(t, "PRIVATE KEY", pkcs8)))
	assert.NotNil(t, h.cryptoKey)
}

// TestDecryptMiddleware_NoKey_PassThrough проверяет, что без ключа
// middleware не мешает обычным открытым запросам.
func TestDecryptMiddleware_NoKey_PassThrough(t *testing.T) {
	s := storage.NewMemStorage()
	h := NewHandler(s)
	r := cryptoRouter(h, "")

	body := `[{"id":"plain","type":"gauge","value":1.5}]`
	req := httptest.NewRequest(http.MethodPost, "/updates/", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
	_, ok := s.GetGauge("plain")
	assert.True(t, ok, "открытый запрос должен обрабатываться как раньше")
}

// TestDecryptMiddleware_DecryptsBody проверяет, что зашифрованное тело
// расшифровывается и батч сохраняется.
func TestDecryptMiddleware_DecryptsBody(t *testing.T) {
	priv := testPrivateKey(t)
	s := storage.NewMemStorage()
	h := NewHandler(s)
	require.NoError(t, h.SetCryptoKey(writePrivateKeyFile(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(priv))))
	r := cryptoRouter(h, "")

	body := `[{"id":"secret","type":"gauge","value":2.5}]`
	envelope, err := crypto.Encrypt(&priv.PublicKey, []byte(body))
	require.NoError(t, err)

	// Content-Encoding не нужен: тело зашифровано, но не сжато.
	req := httptest.NewRequest(http.MethodPost, "/updates/", bytes.NewReader(envelope))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	val, ok := s.GetGauge("secret")
	require.True(t, ok)
	assert.Equal(t, 2.5, val)
}

// TestDecryptMiddleware_RejectsPlaintext проверяет, что при заданном ключе
// незашифрованный батч отклоняется.
func TestDecryptMiddleware_RejectsPlaintext(t *testing.T) {
	priv := testPrivateKey(t)
	h := NewHandler(storage.NewMemStorage())
	require.NoError(t, h.SetCryptoKey(writePrivateKeyFile(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(priv))))
	r := cryptoRouter(h, "")

	body := `[{"id":"plain","type":"gauge","value":1.5}]`
	req := httptest.NewRequest(http.MethodPost, "/updates/", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
}

// TestDecryptMiddleware_RejectsGarbage проверяет, что повреждённый конверт
// даёт 400, а не панику и не 200.
func TestDecryptMiddleware_RejectsGarbage(t *testing.T) {
	priv := testPrivateKey(t)
	s := storage.NewMemStorage()
	h := NewHandler(s)
	require.NoError(t, h.SetCryptoKey(writePrivateKeyFile(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(priv))))
	r := cryptoRouter(h, "")

	envelope, err := crypto.Encrypt(&priv.PublicKey, []byte(`[{"id":"x","type":"gauge","value":1}]`))
	require.NoError(t, err)
	envelope[len(envelope)-1] ^= 0xff // портим тег аутентификации

	req := httptest.NewRequest(http.MethodPost, "/updates/", bytes.NewReader(envelope))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	_, ok := s.GetGauge("x")
	assert.False(t, ok, "повреждённый батч не должен сохраняться")
}

// TestDecryptMiddleware_EmptyBody проверяет, что запросы без тела (GET /)
// не ломаются при включённом ключе.
func TestDecryptMiddleware_EmptyBody(t *testing.T) {
	priv := testPrivateKey(t)
	h := NewHandler(storage.NewMemStorage())
	require.NoError(t, h.SetCryptoKey(writePrivateKeyFile(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(priv))))
	r := cryptoRouter(h, "")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
}

// TestDecryptMiddleware_FullChain проверяет всю цепочку как у реального
// агента: тело сжато gzip, зашифровано и подписано. Если middleware в
// роутере окажутся в неверном порядке (например, gzip раньше расшифрования),
// тест упадёт.
func TestDecryptMiddleware_FullChain(t *testing.T) {
	priv := testPrivateKey(t)
	s := storage.NewMemStorage()
	h := NewHandler(s)
	require.NoError(t, h.SetCryptoKey(writePrivateKeyFile(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(priv))))
	r := cryptoRouter(h, "sign-key")

	body := `[{"id":"hash_gauge","type":"gauge","value":10.25}]`
	gzipped := compressData(t, []byte(body))

	envelope, err := crypto.Encrypt(&priv.PublicKey, gzipped)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/updates/", bytes.NewReader(envelope))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("HashSHA256", hash.Compute(envelope, "sign-key"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	val, ok := s.GetGauge("hash_gauge")
	require.True(t, ok)
	assert.Equal(t, 10.25, val)
}
