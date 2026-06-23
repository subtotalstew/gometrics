package main

import (
	"testing"

	"github.com/subtotalstew/gometrics.git/internal/storage"
)

func TestMemStorage(t *testing.T) {
	s := &storage.MemStorage{
		Gauge:   make(map[string]float64),
		Counter: make(map[string]int64),
	}
	if s == nil {
		t.Errorf("s is nil, expected not nil value")
	}
}
