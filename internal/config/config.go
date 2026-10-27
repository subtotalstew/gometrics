// Package config загружает настройки приложения из JSON-файла.
//
// Файл конфигурации — самый слабый источник настроек: его значения
// перекрываются флагами и переменными окружения. Разбор приоритетов
// живёт в cmd/server и cmd/agent, здесь только чтение и разбор значений.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
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

// ParseSeconds переводит значение интервала из файла в целое число секунд.
// Приложение хранит интервалы целыми секундами, поэтому длительность
// округляется до ближайшей секунды ("500ms" -> 1).
// Кроме длительностей ("1s", "2m") принимается и простое число секунд ("300"),
// чтобы файл вёл себя так же, как флаги и переменные окружения.
func ParseSeconds(s string) (int, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return int(d.Round(time.Second) / time.Second), nil
	}

	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("неверный формат интервала %q: ожидается длительность вида 1s", s)
	}
	return n, nil
}
