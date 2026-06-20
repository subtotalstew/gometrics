package main

import (
	"net/http"
	"strconv"
	"strings"
)

type MemStorage struct {
	Gauge   map[string]float64
	Counter map[string]int64
}

func (m *MemStorage) SetGauge(name string, value float64) error {
	m.Gauge[name] = value
	return nil
}

func (m *MemStorage) UpdateCounter(name string, value int64) error {
	m.Counter[name] += value
	return nil
}

type Storage interface {
	SetGauge(name string, value float64) error
	UpdateCounter(name string, value int64) error
}

func update(w http.ResponseWriter, r *http.Request, storage Storage) {

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

func main() {
	storage := &MemStorage{
		Gauge:   make(map[string]float64),
		Counter: make(map[string]int64),
	}

	http.HandleFunc("/update/", func(w http.ResponseWriter, r *http.Request) {
		update(w, r, storage)
	})
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		panic(err)
	}
}
