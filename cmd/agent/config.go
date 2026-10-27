package main

import (
	fileconfig "github.com/subtotalstew/gometrics.git/internal/config"
)

// fileConfig — содержимое JSON-файла конфигурации агента.
// Путь к файлу задаётся флагом -c/-config или переменной окружения CONFIG.
//
// Поля — указатели, чтобы отличить отсутствующее в файле поле от нулевого
// значения.
type fileConfig struct {
	Address        *string `json:"address"`
	ReportInterval *string `json:"report_interval"`
	PollInterval   *string `json:"poll_interval"`
	CryptoKey      *string `json:"crypto_key"`
	Key            *string `json:"key"`
	RateLimit      *int    `json:"rate_limit"`
}

// applyFileConfig накладывает значения из файла на cfg.
// set сообщает, задан ли флаг с таким именем явно: такие поля файл
// не перекрывает. Переменные окружения применяются вызывающей стороной
// уже после файла, поэтому env тоже побеждает.
func applyFileConfig(cfg *config, path string, set func(name string) bool) error {
	// Пустой path просто не читает файл.
	var fc fileConfig
	if err := fileconfig.LoadFile(path, &fc); err != nil {
		return err
	}

	if fc.Address != nil && !set("a") {
		cfg.addr = *fc.Address
	}
	if fc.ReportInterval != nil && !set("r") {
		n, err := fileconfig.ParseSeconds(*fc.ReportInterval)
		if err != nil {
			return err
		}
		cfg.reportInterval = n
	}
	if fc.PollInterval != nil && !set("p") {
		n, err := fileconfig.ParseSeconds(*fc.PollInterval)
		if err != nil {
			return err
		}
		cfg.pollInterval = n
	}
	if fc.CryptoKey != nil && !set("crypto-key") {
		cfg.cryptoKey = *fc.CryptoKey
	}
	if fc.Key != nil && !set("k") {
		cfg.key = *fc.Key
	}
	if fc.RateLimit != nil && !set("l") {
		cfg.rateLimit = *fc.RateLimit
	}
	return nil
}
