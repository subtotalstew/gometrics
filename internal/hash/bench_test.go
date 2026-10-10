package hash

import (
	"bytes"
	"testing"
)

var benchKey = "bench-secret-key-for-hmac-sha256"

func benchPayload(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte('a' + i%26)
	}
	return data
}

func BenchmarkCompute64B(b *testing.B) {
	body := benchPayload(64)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Compute(body, benchKey)
	}
}

func BenchmarkCompute4KB(b *testing.B) {
	body := benchPayload(4 << 10)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Compute(body, benchKey)
	}
}

func BenchmarkCompute64KB(b *testing.B) {
	body := benchPayload(64 << 10)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Compute(body, benchKey)
	}
}

func BenchmarkComputeGzipBatch(b *testing.B) {
	// Типичный размер gzip-батча агента: ~2 КБ.
	body := bytes.Repeat([]byte(`{"id":"HeapAlloc","type":"gauge","value":1234567.89},`), 40)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Compute(body, benchKey)
	}
}
