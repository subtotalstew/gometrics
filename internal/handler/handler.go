package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/subtotalstew/gometrics.git/internal/storage"
)

func Update(w http.ResponseWriter, r *http.Request, storage storage.Storage) {

	contentType := r.Header.Get("Content-Type")
	if contentType != "text/plain" {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		w.Write([]byte("Content-Type not text/plain."))
		return
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Method not Allowed."))
		return
	}

	path := r.URL.Path
	parts := strings.Split(path, "/")

	if len(parts) != 5 {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	metricType := parts[2]
	metricName := parts[3]
	metricValue := parts[4]

	fmt.Printf("recieved metric: %s, value: %s, type: %s", metricName, metricValue, metricType)
	fmt.Println()

	if metricType != "gauge" && metricType != "counter" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if metricName == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	switch metricType {
	case "gauge":
		value, err := strconv.ParseFloat(metricValue, 64)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		storage.SetGauge(metricName, value)
		w.WriteHeader(http.StatusOK)
	case "counter":
		value, err := strconv.ParseInt(metricValue, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		storage.UpdateCounter(metricName, value)
		w.WriteHeader(http.StatusOK)
	}
}
