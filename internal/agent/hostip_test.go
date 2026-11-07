package agent

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/subtotalstew/gometrics.git/internal/models"
)

// TestHostIP проверяет, что HostIP всегда возвращает корректный IPv4-адрес:
// заголовок X-Real-IP должен разбираться сервером через net.ParseIP.
func TestHostIP(t *testing.T) {
	ip := HostIP()
	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Fatalf("HostIP() = %q, want a valid IP", ip)
	}
	if parsed.To4() == nil {
		t.Fatalf("HostIP() = %q, want an IPv4 address", ip)
	}
}

// TestSendMetricsBatch_SetsRealIPHeader проверяет, что агент сообщает серверу
// свой IP в заголовке X-Real-IP — по нему сервер сверяет доверенную подсеть.
func TestSendMetricsBatch_SetsRealIPHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(HeaderRealIP)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := NewAgent(srv.URL, 1, 1, 1, "")
	v := 1.5
	a.sendMetricsBatch(context.Background(), []models.Metrics{
		{ID: "g1", MType: models.Gauge, Value: &v},
	})

	if got == "" {
		t.Fatal("X-Real-IP header is missing")
	}
	if got != a.hostIP {
		t.Errorf("X-Real-IP = %q, want %q", got, a.hostIP)
	}
	if net.ParseIP(got) == nil {
		t.Errorf("X-Real-IP = %q is not a valid IP", got)
	}
}

// TestNewRequest_RealIPFallback проверяет, что заголовок выставляется даже
// для агента, созданного без NewAgent (hostIP не заполнен).
func TestNewRequest_RealIPFallback(t *testing.T) {
	a := &Agent{}
	req, err := a.newRequest(context.Background(), "http://example.com/updates/", nil, http.Header{})
	if err != nil {
		t.Fatalf("newRequest() error = %v", err)
	}
	if ip := req.Header.Get(HeaderRealIP); net.ParseIP(ip) == nil {
		t.Errorf("X-Real-IP = %q, want a valid IP", ip)
	}
}
