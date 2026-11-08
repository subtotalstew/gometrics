package grpctls

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGenerateSelfSigned проверяет, что сертификат валиден и покрывает
// адреса, по которым к серверу будут подключаться.
func TestGenerateSelfSigned(t *testing.T) {
	cert, certPEM, err := GenerateSelfSigned()
	if err != nil {
		t.Fatalf("GenerateSelfSigned() error = %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("certificate chain is empty")
	}
	if cert.PrivateKey == nil {
		t.Fatal("private key is nil")
	}

	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("certPEM is not a PEM certificate")
	}

	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	if parsed.Subject.CommonName != "gometrics-grpc" {
		t.Errorf("CommonName = %q, want gometrics-grpc", parsed.Subject.CommonName)
	}
	if parsed.NotAfter.Before(time.Now()) {
		t.Error("certificate is already expired")
	}

	var hasLocalhost, hasLoopback bool
	for _, name := range parsed.DNSNames {
		if name == "localhost" {
			hasLocalhost = true
		}
	}
	for _, ip := range parsed.IPAddresses {
		if ip.String() == "127.0.0.1" {
			hasLoopback = true
		}
	}
	if !hasLocalhost || !hasLoopback {
		t.Errorf("SAN = %v / %v, want localhost and 127.0.0.1", parsed.DNSNames, parsed.IPAddresses)
	}
}

// TestServerCredentials_Generated проверяет fallback: без файлов сервер
// получает самоподписанные credentials.
func TestServerCredentials_Generated(t *testing.T) {
	creds, generated, err := ServerCredentials("", "")
	if err != nil {
		t.Fatalf("ServerCredentials() error = %v", err)
	}
	if !generated {
		t.Error("generated = false, want true for empty cert/key")
	}
	if creds == nil {
		t.Fatal("credentials are nil")
	}
	if creds.Info().SecurityProtocol != "tls" {
		t.Errorf("SecurityProtocol = %q, want tls", creds.Info().SecurityProtocol)
	}
}

// TestServerCredentials_PartialPair проверяет, что неполная пара — ошибка.
func TestServerCredentials_PartialPair(t *testing.T) {
	tests := []struct{ cert, key string }{
		{cert: "/tmp/server.crt"},
		{key: "/tmp/server.key"},
	}
	for _, tt := range tests {
		if _, _, err := ServerCredentials(tt.cert, tt.key); err == nil {
			t.Errorf("ServerCredentials(%q, %q) = nil, want error", tt.cert, tt.key)
		}
	}
}

// TestServerCredentials_MissingFiles проверяет ошибку при несуществующих файлах.
func TestServerCredentials_MissingFiles(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	if _, _, err := ServerCredentials(certFile, keyFile); err == nil {
		t.Fatal("ServerCredentials() = nil, want error for missing files")
	}
}

// TestClientCredentials проверяет оба режима: без CA (fallback) и с CA-файлом.
func TestClientCredentials(t *testing.T) {
	creds, err := ClientCredentials("")
	if err != nil {
		t.Fatalf("ClientCredentials(\"\") error = %v", err)
	}
	if creds == nil || creds.Info().SecurityProtocol != "tls" {
		t.Fatalf("credentials = %v, want tls", creds)
	}

	_, certPEM, err := GenerateSelfSigned()
	if err != nil {
		t.Fatalf("GenerateSelfSigned() error = %v", err)
	}
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, certPEM, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := ClientCredentials(caFile); err != nil {
		t.Errorf("ClientCredentials(caFile) error = %v", err)
	}
	if _, err := ClientCredentials(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Error("ClientCredentials(missing) = nil, want error")
	}
}
