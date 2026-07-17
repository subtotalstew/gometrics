package handler_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/subtotalstew/gometrics.git/internal/handler"
	models "github.com/subtotalstew/gometrics.git/internal/model"
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

			h := handler.NewHandler(s)

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

			h := handler.NewHandler(s)
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
	h := handler.NewHandler(s)

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
	h := handler.NewHandler(s)

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
	h := handler.NewHandler(s)

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
