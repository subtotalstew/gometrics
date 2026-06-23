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

func TestUpdate(t *testing.T) {
	type requestdata struct {
		method      string
		contentType string
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
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.requestdata.method, "/update/counter/TestMetric/1", nil)
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
