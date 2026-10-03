package storage

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgerrcode"
	"github.com/rs/zerolog/log"

	models "github.com/subtotalstew/gometrics.git/internal/model"
	"github.com/subtotalstew/gometrics.git/internal/retry"
)

type DBStorage struct {
	db *sql.DB
}

func NewDBStorage(dsn string, migrationsPath string) (*DBStorage, error) {
	log.Info().Msg("подключение к базе данных PostgreSQL")

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Error().Err(err).Msg("не удалось открыть соединение с БД")
		return nil, fmt.Errorf("не удалось открыть соединение с БД: %w", err)
	}

	if err := db.Ping(); err != nil {
		log.Error().Err(err).Msg("не удалось подключиться к БД")
		return nil, fmt.Errorf("не удалось подключиться к БД: %w", err)
	}

	log.Info().Msg("соединение с БД установлено, применяем миграции")

	if err := runMigrations(db, migrationsPath); err != nil {
		log.Error().Err(err).Msg("не удалось выполнить миграции")
		return nil, fmt.Errorf("не удалось выполнить миграции: %w", err)
	}

	log.Info().Msg("миграции применены успешно")

	return &DBStorage{db: db}, nil
}

func runMigrations(db *sql.DB, migrationsPath string) error {
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return err
	}

	m, err := migrate.NewWithDatabaseInstance(
		fmt.Sprintf("file://%s", migrationsPath),
		"postgres",
		driver,
	)
	if err != nil {
		return err
	}

	if err := m.Up(); err != nil {
		if err == migrate.ErrNoChange {
			log.Info().Msg("миграции не требуются, схема уже актуальна")
			return nil
		}
		return err
	}

	return nil
}

// DB возвращает нижележащий *sql.DB — используется хендлером /ping.
func (s *DBStorage) DB() *sql.DB {
	return s.db
}

func (s *DBStorage) Close() error {
	log.Info().Msg("закрываем соединение с базой данных")
	return s.db.Close()
}

func (s *DBStorage) SetGauge(name string, value float64) error {
	return retry.Do("db_set_gauge", func() error {
		_, err := s.db.Exec(`
			INSERT INTO gauges (id, value)
			VALUES ($1, $2)
			ON CONFLICT (id) DO UPDATE SET value = EXCLUDED.value
		`, name, value)
		return err
	}, isRetriablePgError)
}

func (s *DBStorage) UpdateCounter(name string, value int64) error {
	return retry.Do("db_update_counter", func() error {
		_, err := s.db.Exec(`
			INSERT INTO counters (id, delta)
			VALUES ($1, $2)
			ON CONFLICT (id) DO UPDATE SET delta = counters.delta + EXCLUDED.delta
		`, name, value)
		return err
	}, isRetriablePgError)
}

func (s *DBStorage) GetGauge(name string) (float64, bool) {
	var value float64

	err := retry.Do("db_get_gauge", func() error {
		return s.db.QueryRow(`SELECT value FROM gauges WHERE id = $1`, name).Scan(&value)
	}, isRetriablePgError)

	if err != nil {
		return 0, false
	}
	return value, true
}

func (s *DBStorage) GetCounter(name string) (int64, bool) {
	var delta int64

	err := retry.Do("db_get_counter", func() error {
		return s.db.QueryRow(`SELECT delta FROM counters WHERE id = $1`, name).Scan(&delta)
	}, isRetriablePgError)

	if err != nil {
		return 0, false
	}
	return delta, true
}

func (s *DBStorage) GetAllMetrics() (map[string]float64, map[string]int64) {
	gauges := make(map[string]float64)
	counters := make(map[string]int64)

	rows, err := s.db.Query(`SELECT id, value FROM gauges`)
	if err != nil {
		log.Error().Err(err).Msg("не удалось получить список gauge из БД")
	} else {
		defer rows.Close()
		for rows.Next() {
			var id string
			var value float64
			if err := rows.Scan(&id, &value); err != nil {
				log.Error().Err(err).Msg("не удалось прочитать строку gauge")
				continue
			}
			gauges[id] = value
		}
		if err := rows.Err(); err != nil {
			log.Error().Err(err).Msg("ошибка при чтении gauges")
		}
	}

	rows2, err := s.db.Query(`SELECT id, delta FROM counters`)
	if err != nil {
		log.Error().Err(err).Msg("не удалось получить список counter из БД")
	} else {
		defer rows2.Close()
		for rows2.Next() {
			var id string
			var delta int64
			if err := rows2.Scan(&id, &delta); err != nil {
				log.Error().Err(err).Msg("не удалось прочитать строку counter")
				continue
			}
			counters[id] = delta
		}
		if err := rows2.Err(); err != nil {
			log.Error().Err(err).Msg("ошибка при чтении counters")
		}
	}

	return gauges, counters
}

func (s *DBStorage) UpdateBatch(metrics []models.Metrics) error {
	if len(metrics) == 0 {
		return nil
	}

	return retry.Do("db_update_batch", func() error {
		tx, err := s.db.Begin()
		if err != nil {
			log.Error().Err(err).Msg("не удалось начать транзакцию для batch-обновления")
			return err
		}
		defer tx.Rollback() //nolint:errcheck

		gaugeStmt, err := tx.Prepare(`
			INSERT INTO gauges (id, value)
			VALUES ($1, $2)
			ON CONFLICT (id) DO UPDATE SET value = EXCLUDED.value
		`)
		if err != nil {
			return err
		}
		defer gaugeStmt.Close()

		counterStmt, err := tx.Prepare(`
			INSERT INTO counters (id, delta)
			VALUES ($1, $2)
			ON CONFLICT (id) DO UPDATE SET delta = counters.delta + EXCLUDED.delta
		`)
		if err != nil {
			return err
		}
		defer counterStmt.Close()

		for _, mt := range metrics {
			switch mt.MType {
			case models.Gauge:
				if mt.Value == nil {
					continue
				}
				if _, err := gaugeStmt.Exec(mt.ID, *mt.Value); err != nil {
					return err
				}
			case models.Counter:
				if mt.Delta == nil {
					continue
				}
				if _, err := counterStmt.Exec(mt.ID, *mt.Delta); err != nil {
					return err
				}
			}
		}

		if err := tx.Commit(); err != nil {
			return err
		}

		log.Debug().Int("count", len(metrics)).Msg("batch метрик записан в БД")
		return nil
	}, isRetriablePgError)
}

func isRetriablePgError(err error) bool {
	if err == nil {
		return false
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return len(pgErr.Code) >= 2 && pgErr.Code[:2] == pgerrcode.ConnectionException[:2]
	}

	return false
}
