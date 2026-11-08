// Package grpcserver реализует gRPC-сервис Metrics: принимает батчи метрик
// от агента и сохраняет их в хранилище.
//
// Адрес агента передаётся в метаданных запроса (x-real-ip), а UnaryInterceptor
// сверяет его с доверенной подсетью — так же, как HTTP-сервер проверяет
// заголовок X-Real-IP. При несовпадении запрос отклоняется с кодом
// codes.PermissionDenied.
package grpcserver

import (
	"context"
	"fmt"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/subtotalstew/gometrics.git/internal/models"
	pb "github.com/subtotalstew/gometrics.git/internal/proto"
	"github.com/subtotalstew/gometrics.git/internal/storage"
)

// MetadataRealIP — ключ метаданных, в котором агент передаёт свой IP-адрес.
// В gRPC имена ключей всегда в нижнем регистре.
const MetadataRealIP = "x-real-ip"

// Server реализует pb.MetricsServer поверх хранилища метрик.
type Server struct {
	pb.UnimplementedMetricsServer

	storage  storage.Storage
	syncSave func()
}

// New создаёт gRPC-реализацию сервиса метрик. syncSave может быть nil — тогда
// после успешного обновления метрики не сохраняются синхронно на диск.
func New(st storage.Storage, syncSave func()) *Server {
	return &Server{storage: st, syncSave: syncSave}
}

// UpdateMetrics принимает батч метрик и сохраняет его целиком или никак:
// сначала конвертируются и проверяются все метрики, и только потом батч
// уходит в хранилище. Пустой батч — успешный ответ без изменений.
func (s *Server) UpdateMetrics(_ context.Context, req *pb.UpdateMetricsRequest) (*pb.UpdateMetricsResponse, error) {
	metrics := make([]models.Metrics, 0, len(req.GetMetrics()))

	for _, m := range req.GetMetrics() {
		converted, err := fromProto(m)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, converted)
	}

	if len(metrics) > 0 {
		if err := s.storage.UpdateBatch(metrics); err != nil {
			return nil, status.Errorf(codes.Internal, "не удалось сохранить метрики: %v", err)
		}
		if s.syncSave != nil {
			s.syncSave()
		}
	}

	return pb.UpdateMetricsResponse_builder{}.Build(), nil
}

// fromProto переводит метрику из protobuf в модель приложения.
// Неизвестный тип и пустое имя — ошибка клиента (InvalidArgument).
func fromProto(m *pb.Metric) (models.Metrics, error) {
	if m.GetId() == "" {
		return models.Metrics{}, status.Error(codes.InvalidArgument, "имя метрики пустое")
	}

	switch m.GetType() {
	case pb.Metric_GAUGE:
		value := m.GetValue()
		return models.Metrics{ID: m.GetId(), MType: models.Gauge, Value: &value}, nil

	case pb.Metric_COUNTER:
		delta := m.GetDelta()
		return models.Metrics{ID: m.GetId(), MType: models.Counter, Delta: &delta}, nil

	default:
		return models.Metrics{}, status.Errorf(codes.InvalidArgument, "неизвестный тип метрики: %v", m.GetType())
	}
}

// TrustedSubnetInterceptor возвращает UnaryInterceptor, который проверяет
// IP-адрес агента из метаданных x-real-ip.
//
// Пустой cidr отключает проверку: как и у HTTP-сервера, пустой trusted_subnet
// означает «принимать метрики без ограничений». Неверный CIDR — ошибка
// запуска, чтобы опечатка в конфигурации не отключала проверку молча.
func TrustedSubnetInterceptor(cidr string) (grpc.UnaryServerInterceptor, error) {
	var trusted *net.IPNet
	if cidr != "" {
		_, subnet, err := net.ParseCIDR(strings.TrimSpace(cidr))
		if err != nil {
			return nil, fmt.Errorf("grpcserver: неверная доверенная подсеть %q: %w", cidr, err)
		}
		trusted = subnet
	}

	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if trusted != nil {
			if err := checkRealIP(ctx, trusted); err != nil {
				return nil, err
			}
		}
		return handler(ctx, req)
	}, nil
}

// checkRealIP достаёт IP агента из входящих метаданных и проверяет,
// что он входит в подсеть. Отсутствие или неразбираемое значение
// трактуются как недоверенный адрес.
func checkRealIP(ctx context.Context, trusted *net.IPNet) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.PermissionDenied, "метаданные запроса отсутствуют")
	}

	values := md.Get(MetadataRealIP)
	if len(values) == 0 {
		return status.Errorf(codes.PermissionDenied, "метаданные %s отсутствуют", MetadataRealIP)
	}

	ip := net.ParseIP(strings.TrimSpace(values[0]))
	if ip == nil || !trusted.Contains(ip) {
		return status.Errorf(codes.PermissionDenied, "адрес %q вне доверенной подсети %s", values[0], trusted)
	}

	return nil
}

// NewGRPCServer собирает gRPC-сервер с реализацией Metrics и интерсептором
// проверки подсети. Возвращённый сервер остаётся за вызывающей стороной:
// она вызывает Serve и останавливает его при завершении работы.
//
// opts позволяют вызвающей стороне добавить настройки транспорта — в
// частности, TLS-credentials (grpc.Creds). Сервер не включает шифрование сам:
// сертификаты читает cmd/server на старте, чтобы ошибка в путях была видна
// сразу.
func NewGRPCServer(st storage.Storage, syncSave func(), trustedSubnet string, opts ...grpc.ServerOption) (*grpc.Server, error) {
	interceptor, err := TrustedSubnetInterceptor(trustedSubnet)
	if err != nil {
		return nil, err
	}

	opts = append(opts, grpc.UnaryInterceptor(interceptor))
	gs := grpc.NewServer(opts...)
	pb.RegisterMetricsServer(gs, New(st, syncSave))
	return gs, nil
}
