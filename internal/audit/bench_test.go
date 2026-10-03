package audit

import (
	"path/filepath"
	"testing"
)

type discardObserver struct{}

func (discardObserver) Notify(Event) error { return nil }

func benchEvent() Event {
	metrics := []string{"HeapAlloc", "HeapInuse", "PollCount", "RandomValue", "TotalMemory"}
	return NewEvent(metrics, "192.168.0.42")
}

func BenchmarkNewEvent(b *testing.B) {
	metrics := []string{"HeapAlloc", "HeapInuse", "PollCount", "RandomValue", "TotalMemory"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewEvent(metrics, "192.168.0.42")
	}
}

func BenchmarkSubjectNotify_TwoObservers(b *testing.B) {
	s := NewSubject()
	s.Register(discardObserver{})
	s.Register(discardObserver{})
	event := benchEvent()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Notify(event)
	}
}

func BenchmarkFileObserverNotify(b *testing.B) {
	path := filepath.Join(b.TempDir(), "audit.log")
	obs := NewFileObserver(path)
	event := benchEvent()

	// Прогреваем файл, чтобы измерять именно append, а не создание.
	if err := obs.Notify(event); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := obs.Notify(event); err != nil {
			b.Fatal(err)
		}
	}
}
