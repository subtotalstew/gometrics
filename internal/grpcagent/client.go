// Package grpcagent реализует gRPC-клиент агента: отправляет батчи метрик
// в сервис Metrics и передаёт IP-адрес агента в метаданных x-real-ip.
//
// Пакет не знает про Collector, расписание и воркеры агента: на вход он
// получает готовый срез models.Metrics, поэтому его можно использовать и
// тестировать отдельно от Agent. Симметричен internal/grpcserver, который
// принимает те же метрики на стороне сервера.
package grpcagent

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/subtotalstew/gometrics.git/internal/models"
	pb "github.com/subtotalstew/gometrics.git/internal/proto"
	"github.com/subtotalstew/gometrics.git/internal/retry"
)

// MetadataRealIP — ключ метаданных, в котором агент передаёт свой IP-адрес.
// Имена ключей gRPC-метаданных всегда в нижнем регистре; значение совпадает
// с HTTP-заголовком X-Real-IP и ключом, который проверяет интерсептор сервера
// (grpcserver.MetadataRealIP).
const MetadataRealIP = "x-real-ip"

// Client — gRPC-клиент сервиса Metrics. Создаётся через Dial (продакшн)
// или New (готовое соединение, например bufconn в тестах).
type Client struct {
	conn   *grpc.ClientConn
	client pb.MetricsClient
}

// Dial создаёт клиента для сервера по адресу host:port (без схемы).
// Пустой addr — не ошибка: возвращается nil, nil, и вызывающая сторона
// остаётся на HTTP-транспорте.
//
// grpc.NewClient не устанавливает TCP-соединение сразу: первый вызов
// происходит при отправке метрик. TLS не используется — в учебном проекте
// сервер и агент общаются внутри доверенной сети (доступ ограничивает
// trusted_subnet на сервере).
func Dial(addr string) (*Client, error) {
	if addr == "" {
		return nil, nil
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("grpcagent: не удалось создать клиент: %w", err)
	}

	return &Client{conn: conn, client: pb.NewMetricsClient(conn)}, nil
}

// New оборачивает готовое gRPC-соединение. Нужен, когда соединение создаёт
// вызывающая сторона (в тестах — bufconn).
func New(cc grpc.ClientConnInterface) *Client {
	return &Client{client: pb.NewMetricsClient(cc)}
}

// SendMetrics отправляет батч метрик методом Metrics.UpdateMetrics и
// повторяет вызов при временных ошибках (Unavailable, DeadlineExceeded,
// ResourceExhausted). Ошибки клиента (например, PermissionDenied) не
// повторяются: их причина не исчезнет со временем.
//
// realIP уходит в метаданные x-real-ip: по нему UnaryInterceptor сервера
// проверяет, что агент входит в доверенную подсеть.
func (c *Client) SendMetrics(ctx context.Context, realIP string, metrics []models.Metrics) error {
	req := &pb.UpdateMetricsRequest{Metrics: toProtoMetrics(metrics)}

	sendOnce := func() error {
		callCtx := metadata.AppendToOutgoingContext(ctx, MetadataRealIP, realIP)
		_, err := c.client.UpdateMetrics(callCtx, req)
		return err
	}

	return retry.DoContext(ctx, "send_metrics_batch_grpc", sendOnce, isRetriableGRPCError)
}

// Close закрывает соединение, созданное Dial. Для клиента из New (чужое
// соединение) ничего не делает. Повторный вызов безопасен.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}

	err := c.conn.Close()
	c.conn = nil
	return err
}

// isRetriableGRPCError сообщает, имеет ли смысл повторить gRPC-вызов.
// Unavailable и DeadlineExceeded — типичные сетевые сбои, ResourceExhausted
// (rate limit сервера) тоже проходит со временем.
func isRetriableGRPCError(err error) bool {
	if err == nil {
		return false
	}

	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}

// toProtoMetrics переводит внутреннюю модель метрик в protobuf-сообщения.
// Для gauge заполняется value, для counter — delta. Метрики неизвестного
// типа пропускаются: агентский buildBatch создаёт только известные, а сервер
// всё равно отклонил бы такой батч.
func toProtoMetrics(metrics []models.Metrics) []*pb.Metric {
	result := make([]*pb.Metric, 0, len(metrics))

	for _, m := range metrics {
		switch m.MType {
		case models.Gauge:
			var value float64
			if m.Value != nil {
				value = *m.Value
			}
			result = append(result, &pb.Metric{
				Id:    m.ID,
				Type:  pb.Metric_GAUGE,
				Value: value,
			})

		case models.Counter:
			var delta int64
			if m.Delta != nil {
				delta = *m.Delta
			}
			result = append(result, &pb.Metric{
				Id:    m.ID,
				Type:  pb.Metric_COUNTER,
				Delta: delta,
			})
		}
	}

	return result
}
