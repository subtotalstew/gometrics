package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.json")

	s := NewMemStorage()
	s.SetGauge("Alloc", 123.45)
	s.UpdateCounter("PollCount", 3)

	if err := SaveToFile(s, path); err != nil {
		t.Fatalf("SaveToFile() error = %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}

	s2 := NewMemStorage()
	if err := LoadFromFile(s2, path); err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}

	gaugeVal, ok := s2.GetGauge("Alloc")
	if !ok || gaugeVal != 123.45 {
		t.Errorf("GetGauge() = %v, %v, want 123.45, true", gaugeVal, ok)
	}

	counterVal, ok := s2.GetCounter("PollCount")
	if !ok || counterVal != 3 {
		t.Errorf("GetCounter() = %v, %v, want 3, true", counterVal, ok)
	}
}

func TestLoadFromFile_MissingFile(t *testing.T) {
	s := NewMemStorage()
	err := LoadFromFile(s, "definitely-does-not-exist-12345.json")
	if err != nil {
		t.Errorf("LoadFromFile() error = %v, want nil for missing file", err)
	}
}

func TestSaveToFile_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "metrics.json")

	s := NewMemStorage()
	s.SetGauge("Test", 1)

	if err := SaveToFile(s, path); err != nil {
		t.Fatalf("SaveToFile() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
}
