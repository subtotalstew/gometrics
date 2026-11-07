package grpcagent

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/subtotalstew/gometrics.git/internal/models"
	pb "github.com/subtotalstew/gometrics.git/internal/proto"
)

// captureServer запоминает последний запрос и метаданные.
type captureServer struct {
	pb.UnimplementedMetricsServer

	mu   sync.Mutex
	got  []*pb.Metric
	meta metadata.MD
}

func (s *captureServer) UpdateMetrics(ctx context.Context, req *pb.UpdateMetricsRequest) (*pb.UpdateMetricsResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta = md
	s.got = req.GetMetrics()

	return &pb.UpdateMetricsResponse{}, nil
}

// dialTestServer поднимает gRPC-сервер на bufconn и возвращает соединение.
func dialTestServer(t *testing.T, srv pb.MetricsServer, opts ...grpc.ServerOption) *grpc.ClientConn {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer(opts...)
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

	t.Cleanup(func() {
		_ = conn.Close()
		gs.Stop()
		_ = lis.Close()
	})
	return conn
}

// TestSendMetrics проверяет отправку батча и метаданные x-real-ip.
func TestSendMetrics(t *testing.T) {
	srv := &captureServer{}
	c := New(dialTestServer(t, srv))

	value := 1.5
	delta := int64(3)
	err := c.SendMetrics(context.Background(), "10.9.8.7", []models.Metrics{
		{ID: "g", MType: models.Gauge, Value: &value},
		{ID: "c", MType: models.Counter, Delta: &delta},
	})
	if err != nil {
		t.Fatalf("SendMetrics() error = %v", err)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()

	if len(srv.got) != 2 {
		t.Fatalf("received %d metrics, want 2", len(srv.got))
	}
	if srv.got[0].GetId() != "g" || srv.got[0].GetValue() != value {
		t.Errorf("gauge = %v, want id=g value=%v", srv.got[0], value)
	}
	if srv.got[1].GetId() != "c" || srv.got[1].GetDelta() != delta {
		t.Errorf("counter = %v, want id=c delta=%d", srv.got[1], delta)
	}
	if got := srv.meta.Get(MetadataRealIP); len(got) != 1 || got[0] != "10.9.8.7" {
		t.Errorf("metadata %s = %v, want [10.9.8.7]", MetadataRealIP, got)
	}
}

// TestSendMetrics_NonRetriableError проверяет, что отказ сервера возвращается
// как ошибка и не повторяется (иначе вызов завис бы на ретраях).
func TestSendMetrics_NonRetriableError(t *testing.T) {
	conn := dialTestServer(t, &captureServer{}, grpc.UnaryInterceptor(
		func(context.Context, any, *grpc.UnaryServerInfo, grpc.UnaryHandler) (any, error) {
			return nil, status.Error(codes.PermissionDenied, "forbidden")
		},
	))

	c := New(conn)
	value := 1.0
	err := c.SendMetrics(context.Background(), "10.0.0.1", []models.Metrics{
		{ID: "g", MType: models.Gauge, Value: &value},
	})

	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("code = %v, want PermissionDenied (err = %v)", status.Code(err), err)
	}
}

// TestDial проверяет создание клиента по адресу и закрытие соединения.
func TestDial(t *testing.T) {
	empty, err := Dial("")
	if err != nil || empty != nil {
		t.Fatalf("Dial(\"\") = %v, %v; want nil, nil", empty, err)
	}

	c, err := Dial("localhost:18080")
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	if c == nil || c.conn == nil {
		t.Fatal("Dial() did not create connection")
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close() error = %v", err)
	}
}

// TestNew_CloseNoop проверяет, что клиент на чужом соединении не закрывает его.
func TestNew_CloseNoop(t *testing.T) {
	srv := &captureServer{}
	conn := dialTestServer(t, srv)

	c := New(conn)
	if err := c.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Соединение, созданное не клиентом, осталось рабочим.
	value := 1.0
	if err := c.SendMetrics(context.Background(), "10.0.0.1", []models.Metrics{
		{ID: "g", MType: models.Gauge, Value: &value},
	}); err != nil {
		t.Errorf("SendMetrics() after Close() error = %v", err)
	}
}

// TestToProtoMetrics проверяет конвертацию модели в protobuf.
func TestToProtoMetrics(t *testing.T) {
	value := 2.5
	delta := int64(7)

	got := toProtoMetrics([]models.Metrics{
		{ID: "g", MType: models.Gauge, Value: &value},
		{ID: "c", MType: models.Counter, Delta: &delta},
		{ID: "nil-gauge", MType: models.Gauge},
		{ID: "unknown", MType: "histogram"},
	})

	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (unknown type skipped)", len(got))
	}
	if got[0].GetValue() != value || got[0].GetType() != pb.Metric_GAUGE {
		t.Errorf("gauge = %v", got[0])
	}
	if got[1].GetDelta() != delta || got[1].GetType() != pb.Metric_COUNTER {
		t.Errorf("counter = %v", got[1])
	}
	if got[2].GetValue() != 0 {
		t.Errorf("nil gauge value = %v, want 0", got[2].GetValue())
	}
}

// TestIsRetriableGRPCError проверяет выбор повторяемых кодов.
func TestIsRetriableGRPCError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "unavailable", err: status.Error(codes.Unavailable, "down"), want: true},
		{name: "deadline", err: status.Error(codes.DeadlineExceeded, "timeout"), want: true},
		{name: "resource exhausted", err: status.Error(codes.ResourceExhausted, "limit"), want: true},
		{name: "permission denied", err: status.Error(codes.PermissionDenied, "forbidden"), want: false},
		{name: "plain error", err: errors.New("boom"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetriableGRPCError(tt.err); got != tt.want {
				t.Errorf("isRetriableGRPCError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
