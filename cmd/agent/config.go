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
//
// Механизм наложения живёт в internal/config: здесь остаётся только
// описание полей агента и имён соответствующих флагов. set сообщает,
// задан ли флаг явно: такие поля файл не перекрывает, а переменные
// окружения применяются вызывающей стороной уже после файла.
func applyFileConfig(cfg *config, path string, set func(name string) bool) error {
	// Пустой path просто не читает файл.
	var fc fileConfig
	if err := fileconfig.LoadFile(path, &fc); err != nil {
		return err
	}

	fileconfig.ApplyIf(&cfg.addr, fc.Address, set, "a")
	fileconfig.ApplyIf(&cfg.cryptoKey, fc.CryptoKey, set, "crypto-key")
	fileconfig.ApplyIf(&cfg.key, fc.Key, set, "k")
	fileconfig.ApplyIf(&cfg.rateLimit, fc.RateLimit, set, "l")

	if err := fileconfig.ApplyValue(&cfg.reportInterval, fc.ReportInterval, set, "r", fileconfig.ParseSeconds); err != nil {
		return err
	}
	return fileconfig.ApplyValue(&cfg.pollInterval, fc.PollInterval, set, "p", fileconfig.ParseSeconds)
}
