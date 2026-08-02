package retry

import (
	"time"

	"github.com/rs/zerolog/log"
)

var Intervals = []time.Duration{1 * time.Second, 3 * time.Second, 5 * time.Second}

type IsRetriable func(err error) bool

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
