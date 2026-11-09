package agent

import (
	"fmt"

	"github.com/subtotalstew/gometrics.git/internal/grpcagent"
)

// SetGRPC переключает отправку метрик на gRPC: addr — адрес сервера в форме
// host:port (без схемы и пути), caFile — PEM-сертификат CA для проверки
// TLS-сертификата сервера. Пустой addr оставляет HTTP-транспорт.
//
// Вся работа с протоколом (TLS-соединение, метаданные x-real-ip, конвертация
// метрик и ретраи) живёт в internal/grpcagent; агент лишь выбирает транспорт
// и передаёт свой адрес хоста.
func (a *Agent) SetGRPC(addr, caFile string) error {
	client, err := grpcagent.Dial(addr, caFile)
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}

	a.grpc = client
	return nil
}

// Close закрывает gRPC-соединение. Для HTTP-транспорта ничего не делает.
// Вызывать после Run, когда отправка уже остановлена.
func (a *Agent) Close() error {
	if a.grpc == nil {
		return nil
	}

	err := a.grpc.Close()
	a.grpc = nil
	return err
}
