package handler_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-playground/assert"
	"github.com/stretchr/testify/require"
	"github.com/subtotalstew/gometrics.git/internal/handler"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

func TestSetGauge(t *testing.T) {
	s := &storage.MemStorage{
		Gauge:   make(map[string]float64),
		Counter: make(map[string]int64),
	}
	s.SetGauge("Test", float64(55))
	if s.Gauge["Test"] != 55 {
		t.Errorf("Test gauge failed, got: %f, want: %f", s.Gauge["Test"], float64(55))
	}
}

func TestUpdateCounter(t *testing.T) {
	s := &storage.MemStorage{
		Gauge:   make(map[string]float64),
		Counter: make(map[string]int64),
	}
	s.UpdateCounter("Test", 1)
	if s.Counter["Test"] != int64(1) {
		t.Errorf("Test counter failed, got: %v, want: %v", s.Counter["Test"], int64(1))
	}
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
		{
			name: "check Content-Type, negative scenario.",
			responsedata: responsedata{
				code:        http.StatusUnsupportedMediaType,
				response:    "Content-Type not text/plain.",
				contentType: "",
			},
			requestdata: requestdata{
				method:      http.MethodPost,
				contentType: "application/json",
				url:         "/update/counter/TestMetric/1",
			},
		},
		{
			name: "check Method, negative",
			responsedata: responsedata{
				code:        http.StatusMethodNotAllowed,
				response:    "Method not Allowed.",
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
			s := &storage.MemStorage{
				Gauge:   make(map[string]float64),
				Counter: make(map[string]int64),
			}
			handler.Update(w, request, s)

			res := w.Result()

			assert.Equal(t, tt.responsedata.code, res.StatusCode)
			defer res.Body.Close()

			resBody, err := io.ReadAll(res.Body)

			require.NoError(t, err)
			assert.Equal(t, tt.responsedata.response, string(resBody))
			assert.Equal(t, tt.responsedata.contentType, res.Header.Get("Content-Type"))
		})
	}
}
