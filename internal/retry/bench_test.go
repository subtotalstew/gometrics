package retry

import (
	"errors"
	"testing"
)

func BenchmarkDoSuccess(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := Do("bench_op", func() error { return nil }, func(error) bool { return false })
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDoNonRetriableError(b *testing.B) {
	fatal := errors.New("fatal")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := Do("bench_op", func() error { return fatal }, func(error) bool { return false })
		if !errors.Is(err, fatal) {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}
