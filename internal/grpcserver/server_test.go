package grpcserver

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/subtotalstew/gometrics.git/internal/models"
	pb "github.com/subtotalstew/gometrics.git/internal/proto"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

// failingStorage — хранилище, у которого UpdateBatch всегда падает.
// Остальные методы не вызываются, поэтому достаточно встроенного интерфейса.
type failingStorage struct {
	storage.Storage
}

func (failingStorage) UpdateBatch([]models.Metrics) error {
	return errors.New("storage is broken")
}

// TestTrustedSubnetInterceptor проверяет проверку IP из метаданных.
func TestTrustedSubnetInterceptor(t *testing.T) {
	tests := []struct {
		name     string
		cidr     string
		realIP   string
		noMD     bool
		wantCode codes.Code
	}{
		{name: "empty subnet passes without metadata", noMD: true, wantCode: codes.OK},
		{name: "inside subnet", cidr: "10.0.0.0/8", realIP: "10.1.2.3", wantCode: codes.OK},
		{name: "outside subnet", cidr: "10.0.0.0/8", realIP: "192.168.1.1", wantCode: codes.PermissionDenied},
		{name: "missing metadata", cidr: "10.0.0.0/8", noMD: true, wantCode: codes.PermissionDenied},
		{name: "invalid IP", cidr: "10.0.0.0/8", realIP: "not-an-ip", wantCode: codes.PermissionDenied},
		{name: "IPv6 inside subnet", cidr: "fd00::/8", realIP: "fd00::1", wantCode: codes.OK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interceptor, err := TrustedSubnetInterceptor(tt.cidr)
			if err != nil {
				t.Fatalf("TrustedSubnetInterceptor(%q) error = %v", tt.cidr, err)
			}

			ctx := context.Background()
			if !tt.noMD {
				ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(MetadataRealIP, tt.realIP))
			}

			called := false
			_, err = interceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(context.Context, any) (any, error) {
				called = true
				return nil, nil
			})

			if got := status.Code(err); got != tt.wantCode {
				t.Errorf("code = %v, want %v (err = %v)", got, tt.wantCode, err)
			}
			if wantCalled := tt.wantCode == codes.OK; called != wantCalled {
				t.Errorf("handler called = %v, want %v", called, wantCalled)
			}
		})
	}
}

// TestTrustedSubnetInterceptor_InvalidCIDR проверяет ошибку запуска
// при неверной доверенной подсети.
func TestTrustedSubnetInterceptor_InvalidCIDR(t *testing.T) {
	if _, err := TrustedSubnetInterceptor("192.168.1.1"); err == nil {
		t.Fatal("TrustedSubnetInterceptor() = nil, want error for bare IP")
	}
	if _, err := NewGRPCServer(storage.NewMemStorage(), nil, "garbage"); err == nil {
		t.Fatal("NewGRPCServer() = nil, want error for invalid CIDR")
	}
}

// TestServer_UpdateMetrics проверяет сохранение батча и вызов syncSave.
func TestServer_UpdateMetrics(t *testing.T) {
	mem := storage.NewMemStorage()
	saved := 0
	s := New(mem, func() { saved++ })

	_, err := s.UpdateMetrics(context.Background(), &pb.UpdateMetricsRequest{Metrics: []*pb.Metric{
		{Id: "g", Type: pb.Metric_GAUGE, Value: 1.5},
		{Id: "c", Type: pb.Metric_COUNTER, Delta: 5},
	}})
	if err != nil {
		t.Fatalf("UpdateMetrics() error = %v", err)
	}

	if value, ok := mem.GetGauge("g"); !ok || value != 1.5 {
		t.Errorf("gauge g = %v, %v; want 1.5, true", value, ok)
	}
	if value, ok := mem.GetCounter("c"); !ok || value != 5 {
		t.Errorf("counter c = %v, %v; want 5, true", value, ok)
	}
	if saved != 1 {
		t.Errorf("syncSave called %d times, want 1", saved)
	}
}

// TestServer_UpdateMetrics_EmptyBatch проверяет, что пустой батч — успех
// без обращения к хранилищу.
func TestServer_UpdateMetrics_EmptyBatch(t *testing.T) {
	mem := storage.NewMemStorage()
	saved := false
	s := New(mem, func() { saved = true })

	resp, err := s.UpdateMetrics(context.Background(), &pb.UpdateMetricsRequest{})
	if err != nil {
		t.Fatalf("UpdateMetrics() error = %v", err)
	}
	if resp == nil {
		t.Fatal("UpdateMetrics() = nil response")
	}
	if saved {
		t.Error("syncSave called for empty batch")
	}
}

// TestServer_UpdateMetrics_Errors проверяет ошибки клиента и хранилища.
func TestServer_UpdateMetrics_Errors(t *testing.T) {
	tests := []struct {
		name string
		srv  *Server
		req  *pb.UpdateMetricsRequest
		code codes.Code
	}{
		{
			name: "empty id",
			srv:  New(storage.NewMemStorage(), nil),
			req:  &pb.UpdateMetricsRequest{Metrics: []*pb.Metric{{Type: pb.Metric_GAUGE}}},
			code: codes.InvalidArgument,
		},
		{
			name: "unknown type",
			srv:  New(storage.NewMemStorage(), nil),
			req:  &pb.UpdateMetricsRequest{Metrics: []*pb.Metric{{Id: "x", Type: pb.Metric_MType(99)}}},
			code: codes.InvalidArgument,
		},
		{
			name: "storage error",
			srv:  New(failingStorage{}, nil),
			req:  &pb.UpdateMetricsRequest{Metrics: []*pb.Metric{{Id: "x", Type: pb.Metric_GAUGE}}},
			code: codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.srv.UpdateMetrics(context.Background(), tt.req); status.Code(err) != tt.code {
				t.Errorf("code = %v, want %v (err = %v)", status.Code(err), tt.code, err)
			}
		})
	}
}

// startTestServer поднимает gRPC-сервер на bufconn и возвращает клиент.
func startTestServer(t *testing.T, st storage.Storage, cidr string) (pb.MetricsClient, func()) {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	gs, err := NewGRPCServer(st, nil, cidr)
	if err != nil {
		t.Fatalf("NewGRPCServer() error = %v", err)
	}
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
	return pb.NewMetricsClient(conn), cleanup
}

// TestGRPC_EndToEnd проверяет полный путь по gRPC: метаданные x-real-ip
// проверяются интерсептором, метрики сохраняются в хранилище.
func TestGRPC_EndToEnd(t *testing.T) {
	t.Run("trusted agent", func(t *testing.T) {
		mem := storage.NewMemStorage()
		client, cleanup := startTestServer(t, mem, "10.0.0.0/8")
		defer cleanup()

		ctx := metadata.AppendToOutgoingContext(context.Background(), MetadataRealIP, "10.1.2.3")
		_, err := client.UpdateMetrics(ctx, &pb.UpdateMetricsRequest{Metrics: []*pb.Metric{
			{Id: "g", Type: pb.Metric_GAUGE, Value: 2.5},
		}})
		if err != nil {
			t.Fatalf("UpdateMetrics() error = %v", err)
		}
		if value, ok := mem.GetGauge("g"); !ok || value != 2.5 {
			t.Errorf("gauge g = %v, %v; want 2.5, true", value, ok)
		}
	})

	t.Run("foreign agent is rejected", func(t *testing.T) {
		mem := storage.NewMemStorage()
		client, cleanup := startTestServer(t, mem, "10.0.0.0/8")
		defer cleanup()

		ctx := metadata.AppendToOutgoingContext(context.Background(), MetadataRealIP, "192.168.1.1")
		_, err := client.UpdateMetrics(ctx, &pb.UpdateMetricsRequest{Metrics: []*pb.Metric{
			{Id: "g", Type: pb.Metric_GAUGE, Value: 2.5},
		}})
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("code = %v, want PermissionDenied (err = %v)", status.Code(err), err)
		}
		if _, ok := mem.GetGauge("g"); ok {
			t.Error("metric saved for foreign agent")
		}
	})

	t.Run("empty subnet accepts everything", func(t *testing.T) {
		mem := storage.NewMemStorage()
		client, cleanup := startTestServer(t, mem, "")
		defer cleanup()

		_, err := client.UpdateMetrics(context.Background(), &pb.UpdateMetricsRequest{Metrics: []*pb.Metric{
			{Id: "g", Type: pb.Metric_GAUGE, Value: 1},
		}})
		if err != nil {
			t.Fatalf("UpdateMetrics() error = %v", err)
		}
		if _, ok := mem.GetGauge("g"); !ok {
			t.Error("metric not saved")
		}
	})
}
