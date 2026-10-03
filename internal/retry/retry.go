// Package retry выполняет операцию повторно при временных ошибках,
// выдерживая паузы между попытками.
package retry

import (
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
func Do(operation string, fn func() error, isRetriable IsRetriable) error {
	var err error

	for attempt := 0; ; attempt++ {
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

		time.Sleep(wait)
	}
}
