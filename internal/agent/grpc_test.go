package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/subtotalstew/gometrics.git/internal/grpcagent"
	"github.com/subtotalstew/gometrics.git/internal/models"
	pb "github.com/subtotalstew/gometrics.git/internal/proto"
)

// captureMetricsServer запоминает последний запрос и метаданные.
type captureMetricsServer struct {
	pb.UnimplementedMetricsServer

	mu   sync.Mutex
	got  []*pb.Metric
	meta metadata.MD
}

func (s *captureMetricsServer) UpdateMetrics(ctx context.Context, req *pb.UpdateMetricsRequest) (*pb.UpdateMetricsResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta = md
	s.got = req.GetMetrics()

	return pb.UpdateMetricsResponse_builder{}.Build(), nil
}

// startAgentTestServer поднимает gRPC-сервер на bufconn и возвращает клиент.
func startAgentTestServer(t *testing.T) (*captureMetricsServer, *grpc.ClientConn, func()) {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := &captureMetricsServer{}
	gs := grpc.NewServer()
	pb.RegisterMetricsServer(gs, srv)
	go func() {
		_ = gs.Serve(lis)
	}()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}

	cleanup := func() {
		_ = conn.Close()
		gs.Stop()
		_ = lis.Close()
	}
	return srv, conn, cleanup
}

// TestSendMetricsBatch_GRPC проверяет, что при настроенном gRPC-клиенте
// sendMetricsBatch делегирует отправку в grpcagent и передаёт x-real-ip.
func TestSendMetricsBatch_GRPC(t *testing.T) {
	srv, conn, cleanup := startAgentTestServer(t)
	defer cleanup()

	a := NewAgent("http://unused:8080", 1, 1, 1, "")
	a.grpc = grpcagent.New(conn)
	a.hostIP = "10.9.8.7"

	value := 1.5
	delta := int64(3)
	a.sendMetricsBatch(context.Background(), []models.Metrics{
		{ID: "g", MType: models.Gauge, Value: &value},
		{ID: "c", MType: models.Counter, Delta: &delta},
	})

	srv.mu.Lock()
	defer srv.mu.Unlock()

	if len(srv.got) != 2 {
		t.Fatalf("received %d metrics, want 2", len(srv.got))
	}
	if srv.got[0].GetId() != "g" || srv.got[0].GetType() != pb.Metric_GAUGE || srv.got[0].GetValue() != value {
		t.Errorf("gauge metric = %v, want id=g type=GAUGE value=%v", srv.got[0], value)
	}
	if srv.got[1].GetId() != "c" || srv.got[1].GetType() != pb.Metric_COUNTER || srv.got[1].GetDelta() != delta {
		t.Errorf("counter metric = %v, want id=c type=COUNTER delta=%d", srv.got[1], delta)
	}
	if got := srv.meta.Get(grpcagent.MetadataRealIP); len(got) != 1 || got[0] != "10.9.8.7" {
		t.Errorf("metadata %s = %v, want [10.9.8.7]", grpcagent.MetadataRealIP, got)
	}
}

// TestSetGRPC и TestClose проверяют настройку и закрытие клиента.
func TestSetGRPC(t *testing.T) {
	a := NewAgent("http://unused", 1, 1, 1, "")
	ca := writeTestCACert(t)

	if err := a.SetGRPC("", ""); err != nil {
		t.Fatalf("SetGRPC(\"\") error = %v", err)
	}
	if a.grpc != nil {
		t.Fatal("SetGRPC(\"\") must not configure gRPC client")
	}

	if err := a.SetGRPC("localhost:18080", ""); err != nil {
		t.Fatalf("SetGRPC() without CA error = %v", err)
	}
	if a.grpc == nil {
		t.Fatal("SetGRPC() without CA did not configure gRPC client")
	}
	if err := a.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	missing := filepath.Join(t.TempDir(), "missing.pem")
	if err := a.SetGRPC("localhost:18080", missing); err == nil {
		t.Fatal("SetGRPC() with missing CA = nil, want error")
	}

	if err := a.SetGRPC("localhost:18080", ca); err != nil {
		t.Fatalf("SetGRPC() error = %v", err)
	}
	if a.grpc == nil {
		t.Fatal("SetGRPC() did not configure gRPC client")
	}

	if err := a.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	if a.grpc != nil {
		t.Error("Close() did not reset gRPC client")
	}
	if err := a.Close(); err != nil {
		t.Errorf("second Close() error = %v", err)
	}
}

// writeTestCACert создаёт минимальный самоподписанный сертификат во временном
// каталоге: Dial проверяет, что CA-файл существует и разбирается.
func writeTestCACert(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "grpc test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
