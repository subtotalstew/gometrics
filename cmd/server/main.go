package main

import (
	"net/http"

	"github.com/subtotalstew/gometrics.git/internal/handler"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

func main() {
	storage := &storage.MemStorage{
		Gauge:   make(map[string]float64),
		Counter: make(map[string]int64),
	}

	http.HandleFunc("/update/", func(w http.ResponseWriter, r *http.Request) {
		handler.Update(w, r, storage)
	})
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		panic(err)
	}
}
