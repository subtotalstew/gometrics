package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"

	models "github.com/subtotalstew/gometrics.git/internal/model"
)

// SaveToFile сохраняет все метрики из хранилища s в файл path в формате
// JSON ([]models.Metrics). Запись идёт через временный файл с последующим
// переименованием, поэтому прерывание процесса не портит существующий файл.
// При необходимости создаёт каталоги назначения.
func SaveToFile(s Storage, path string) error {
	gauges, counters := s.GetAllMetrics()

	metrics := make([]models.Metrics, 0, len(gauges)+len(counters))

	for name, value := range gauges {
		v := value
		metrics = append(metrics, models.Metrics{
			ID:    name,
			MType: models.Gauge,
			Value: &v,
		})
	}

	for name, delta := range counters {
		d := delta
		metrics = append(metrics, models.Metrics{
			ID:    name,
			MType: models.Counter,
			Delta: &d,
		})
	}

	data, err := json.MarshalIndent(metrics, "", "  ")
	if err != nil {
		return err
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadFromFile восстанавливает метрики из файла path в хранилище s:
// gauge-метрики записываются, counter-метрики прибавляются. Отсутствие файла
// не считается ошибкой — это обычная ситуация при первом запуске.
func LoadFromFile(s Storage, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Info().Str("path", path).Msg("файл со значениями метрик не найден, пропускаем восстановление")
			return nil
		}
		return err
	}

	var metrics []models.Metrics
	if err := json.Unmarshal(data, &metrics); err != nil {
		return err
	}

	for _, m := range metrics {
		switch m.MType {
		case models.Gauge:
			if m.Value != nil {
				if err := s.SetGauge(m.ID, *m.Value); err != nil {
					log.Warn().Err(err).Str("metric", m.ID).Msg("не удалось восстановить gauge")
				}
			}
		case models.Counter:
			if m.Delta != nil {
				if err := s.UpdateCounter(m.ID, *m.Delta); err != nil {
					log.Warn().Err(err).Str("metric", m.ID).Msg("не удалось восстановить counter")
				}
			}
		}
	}

	log.Info().Str("path", path).Int("count", len(metrics)).Msg("метрики восстановлены из файла")
	return nil
}

// RunPeriodicSave запускает фоновое сохранение метрик в файл каждые interval
// секунд и блокирует горутину до закрытия канала stop. При interval <= 0
// функция сразу возвращает управление (сохранение выключено).
func RunPeriodicSave(s Storage, path string, interval int, stop <-chan struct{}) {
	if interval <= 0 {
		return
	}

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := SaveToFile(s, path); err != nil {
				log.Error().Err(err).Str("path", path).Msg("не удалось сохранить метрики на диск")
			} else {
				log.Debug().Str("path", path).Msg("метрики сохранены на диск")
			}
		case <-stop:
			return
		}
	}
}
