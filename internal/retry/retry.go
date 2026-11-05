// Package retry выполняет операцию повторно при временных ошибках,
// выдерживая паузы между попытками.
package retry

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
)

// Intervals задаёт паузы перед повторными попытками. Количество элементов
// определяет число повторов после первой попытки, поэтому всего выполняется
// len(Intervals)+1 попыток. Переменную можно переопределить (например,
// в тестах, чтобы не ждать реальные секунды).
var Intervals = []time.Duration{1 * time.Second, 3 * time.Second, 5 * time.Second}

// IsRetriable решает, имеет ли смысл повторять операцию при данной ошибке.
// Для невосстанавливаемых ошибок возвращается false, и retry.Do
// прекращает попытки.
type IsRetriable func(err error) bool

// Do вызывает fn, пока она не выполнится успешно или пока isRetriable
// не вернёт false, либо пока не закончатся паузы из Intervals.
//
// Возвращается ошибка последней попытки. Паузы между попытками и итоговая
// ошибка логируются, operation используется как имя операции в логах.
//
// Do — это DoContext с фоновым контекстом: прервать повторы нельзя.
func Do(operation string, fn func() error, isRetriable IsRetriable) error {
	return DoContext(context.Background(), operation, fn, isRetriable)
}

// DoContext — как Do, но прекращает попытки при отмене ctx. Нужна там, где
// время выполнения ограничено: например, при остановке приложения, когда
// ретраи не должны затягивать завершение.
//
// Отмена проверяется перед каждой попыткой и во время паузы между ними,
// поэтому уже начатый сетевой вызов прерывается только своим контекстом
// (если fn его использует).
func DoContext(ctx context.Context, operation string, fn func() error, isRetriable IsRetriable) error {
	var err error

	for attempt := 0; ; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%s: попытки прекращены: %w", operation, ctxErr)
		}

		err = fn()
		if err == nil {
			return nil
		}

		if !isRetriable(err) {
			return err
		}

		if attempt >= len(Intervals) {
			log.Error().
				Err(err).
				Str("operation", operation).
				Int("attempts", attempt+1).
				Msg("retriable operation failed after all attempts")
			return err
		}

		wait := Intervals[attempt]
		log.Warn().
			Err(err).
			Str("operation", operation).
			Int("attempt", attempt+1).
			Dur("retry_in", wait).
			Msg("retriable error, will retry")

		select {
		case <-time.After(wait):
		case <-ctx.Done():
			log.Warn().
				Err(ctx.Err()).
				Str("operation", operation).
				Int("attempt", attempt+1).
				Msg("retries stopped by context")
			return fmt.Errorf("%s: попытки прекращены: %w", operation, ctx.Err())
		}
	}
}
