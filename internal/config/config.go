// Package config загружает настройки приложения из JSON-файла и
// накладывает их на конфигурацию запуска.
//
// Файл конфигурации — самый слабый источник настроек: его значения
// перекрываются флагами и переменными окружения. Механизм наложения
// (ExplicitFlags, ApplyIf, ApplyValue) живёт здесь, а cmd-пакеты
// описывают только собственный набор полей — так приоритеты источников
// не разъезжаются между командами.
package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// LoadFile читает JSON-файл конфигурации по пути path в dst.
// Пустой path — не ошибка: конфигурационный файл просто не задан.
func LoadFile(path string, dst any) error {
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("не удалось прочитать файл конфигурации %q: %w", path, err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("не удалось разобрать файл конфигурации %q: %w", path, err)
	}
	return nil
}

// ExplicitFlags возвращает функцию, которая сообщает, был ли флаг с таким
// именем задан явно. Нужна, чтобы значения из файла не перекрывали флаги.
func ExplicitFlags(fs *flag.FlagSet) func(name string) bool {
	explicit := make(map[string]bool, fs.NFlag())
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	return func(name string) bool { return explicit[name] }
}

// ApplyIf записывает значение из файла в dst, если поле в файле есть и
// соответствующий флаг не был задан явно.
func ApplyIf[T any](dst *T, src *T, set func(name string) bool, flagName string) {
	if src != nil && !set(flagName) {
		*dst = *src
	}
}

// ApplyValue записывает значение из файла в dst, преобразуя его через parse.
// Если поля в файле нет или флаг задан явно, dst не меняется. Ошибка разбора
// возвращается вызывающему: неверное значение в файле должно останавливать
// запуск, а не подменяться значением по умолчанию.
func ApplyValue[T, S any](dst *T, src *S, set func(name string) bool, flagName string, parse func(S) (T, error)) error {
	if src == nil || set(flagName) {
		return nil
	}

	v, err := parse(*src)
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

// ParseInterval переводит значение интервала из файла в time.Duration.
// Принимаются два формата: простое число секунд ("300") и длительность
// ("1s", "2m", "500ms") — так файл ведёт себя так же, как флаги и env.
//
// Формат определяется однозначно (сначала число, затем длительность),
// поэтому ошибка разбора объясняет оба ожидаемых варианта и сохраняет
// исходную ошибку time.ParseDuration.
func ParseInterval(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)

	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second, nil
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf(
			`неверный интервал %q: ожидается число секунд ("300") или длительность ("1s", "2m"): %w`,
			s, err,
		)
	}
	return d, nil
}

// ParseSeconds переводит интервал из файла в целое число секунд.
// Приложение хранит интервалы целыми секундами, поэтому длительность
// округляется до ближайшей секунды ("500ms" -> 1).
func ParseSeconds(s string) (int, error) {
	d, err := ParseInterval(s)
	if err != nil {
		return 0, err
	}
	return int(d.Round(time.Second) / time.Second), nil
}

// ParseStoreInterval разбирает store_interval сервера, для которого ноль
// означает синхронное сохранение. Положительные значения меньше секунды
// отклоняются: округление "300ms" до нуля молча переключило бы режим
// работы хранилища.
func ParseStoreInterval(s string) (int, error) {
	d, err := ParseInterval(s)
	if err != nil {
		return 0, err
	}
	if d > 0 && d < time.Second {
		return 0, fmt.Errorf(
			"store_interval = %q: субсекундные значения не поддерживаются, укажите целое число секунд (0 — синхронное сохранение)",
			s,
		)
	}
	return int(d.Round(time.Second) / time.Second), nil
}
