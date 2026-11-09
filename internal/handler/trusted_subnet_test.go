package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/subtotalstew/gometrics.git/internal/storage"
)

// TestSetTrustedSubnet проверяет разбор CIDR: пустая строка отключает
// ограничение, неверное значение — ошибка запуска.
func TestSetTrustedSubnet(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		wantErr bool
	}{
		{name: "empty disables restriction", cidr: ""},
		{name: "valid IPv4 subnet", cidr: "192.168.1.0/24"},
		{name: "valid IPv6 subnet", cidr: "fd00::/8"},
		{name: "spaces are trimmed", cidr: " 10.0.0.0/8 "},
		{name: "invalid: bare IP", cidr: "192.168.1.1", wantErr: true},
		{name: "invalid: garbage", cidr: "not-a-subnet", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler(storage.NewMemStorage())

			err := h.SetTrustedSubnet(tt.cidr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("SetTrustedSubnet(%q) = nil, want error", tt.cidr)
				}
				if h.trustedSubnet != nil {
					t.Error("trustedSubnet is set after error")
				}
				return
			}

			if err != nil {
				t.Fatalf("SetTrustedSubnet(%q) error = %v", tt.cidr, err)
			}
			if tt.cidr == "" && h.trustedSubnet != nil {
				t.Error("trustedSubnet != nil for empty CIDR")
			}
			if tt.cidr != "" && h.trustedSubnet == nil {
				t.Error("trustedSubnet == nil for valid CIDR")
			}
		})
	}
}

// TestTrustedSubnetMiddleware проверяет, что метрики принимаются только
// от адресов из доверенной подсети и что пустая подсеть ничего не ограничивает.
func TestTrustedSubnetMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		subnet     string
		realIP     string
		wantStatus int
	}{
		{name: "no subnet, no header", wantStatus: http.StatusOK},
		{name: "no subnet, any header", realIP: "10.0.0.1", wantStatus: http.StatusOK},
		{name: "inside subnet", subnet: "192.168.0.0/16", realIP: "192.168.1.10", wantStatus: http.StatusOK},
		{name: "outside subnet", subnet: "192.168.0.0/16", realIP: "10.0.0.1", wantStatus: http.StatusForbidden},
		{name: "missing header", subnet: "192.168.0.0/16", wantStatus: http.StatusForbidden},
		{name: "malformed header", subnet: "192.168.0.0/16", realIP: "not-an-ip", wantStatus: http.StatusForbidden},
		{name: "IPv6 inside subnet", subnet: "fd00::/8", realIP: "fd00::1", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler(storage.NewMemStorage())
			if err := h.SetTrustedSubnet(tt.subnet); err != nil {
				t.Fatalf("SetTrustedSubnet(%q) error = %v", tt.subnet, err)
			}

			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodPost, "/updates", nil)
			if tt.realIP != "" {
				req.Header.Set("X-Real-IP", tt.realIP)
			}
			w := httptest.NewRecorder()

			h.TrustedSubnetMiddleware(next).ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}
