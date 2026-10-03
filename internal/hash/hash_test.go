package hash

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestCompute_KnownVector(t *testing.T) {
	// RFC 4231 / Wikipedia test vector для HMAC-SHA256.
	const (
		key  = "key"
		body = "The quick brown fox jumps over the lazy dog"
		want = "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"
	)

	if got := Compute([]byte(body), key); got != want {
		t.Fatalf("Compute() = %q, want %q", got, want)
	}
}

func TestCompute_EmptyBody(t *testing.T) {
	want := hex.EncodeToString(hmacSHA256(nil, "secret"))

	got := Compute(nil, "secret")
	if got != want {
		t.Fatalf("Compute() = %q, want %q", got, want)
	}
	if len(got) != sha256.Size*2 {
		t.Fatalf("длина хеша = %d, want %d", len(got), sha256.Size*2)
	}
}

func TestCompute_Deterministic(t *testing.T) {
	body := []byte(`{"id":"HeapAlloc","type":"gauge","value":1}`)

	first := Compute(body, "key-1")
	if first != Compute(body, "key-1") {
		t.Fatal("Compute() не детерминирован для одинаковых входных данных")
	}
	if first == Compute(body, "key-2") {
		t.Fatal("Compute() не зависит от ключа")
	}
	if first == Compute([]byte(`{"id":"HeapAlloc"}`), "key-1") {
		t.Fatal("Compute() не зависит от тела запроса")
	}
}

func TestCompute_MatchesStdlib(t *testing.T) {
	body := []byte("loadgen-batch-payload")

	if got, want := Compute(body, "bench-key"), hex.EncodeToString(hmacSHA256(body, "bench-key")); got != want {
		t.Fatalf("Compute() = %q, want %q", got, want)
	}
}

func hmacSHA256(body []byte, key string) []byte {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	return mac.Sum(nil)
}
