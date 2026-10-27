package agent

import (
	"errors"
	"net"
	"net/http"
	"syscall"
	"testing"
)

func TestIsRetriableStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		want   bool
	}{
		{http.StatusOK, false},
		{http.StatusBadRequest, false},
		{http.StatusNotFound, false},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			t.Parallel()
			if got := isRetriableStatus(tt.status); got != tt.want {
				t.Errorf("isRetriableStatus(%d) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestIsRetriableHTTPError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"generic", errors.New("generic"), false},
		{"timeout", &net.DNSError{IsTimeout: true}, true},
		{"conn refused", syscall.ECONNREFUSED, true},
		{"conn reset", syscall.ECONNRESET, true},
		{"timeout syscall", syscall.ETIMEDOUT, true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isRetriableHTTPError(tt.err); got != tt.want {
				t.Errorf("isRetriableHTTPError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
