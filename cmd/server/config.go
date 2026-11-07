package main

import (
	fileconfig "github.com/subtotalstew/gometrics.git/internal/config"
)

// fileConfig — содержимое JSON-файла конфигурации сервера.
// Путь к файлу задаётся флагом -c/-config или переменной окружения CONFIG.
//
// Поля — указатели, чтобы отличить отсутствующее в файле поле от нулевого
// значения: "restore": false и полное отсутствие "restore" — разные вещи.
type fileConfig struct {
	Address       *string `json:"address"`
	Restore       *bool   `json:"restore"`
	StoreInterval *string `json:"store_interval"`
	StoreFile     *string `json:"store_file"`
	DatabaseDSN   *string `json:"database_dsn"`
	CryptoKey     *string `json:"crypto_key"`
	Key           *string `json:"key"`
	AuditFile     *string `json:"audit_file"`
	AuditURL      *string `json:"audit_url"`
	PprofAddr     *string `json:"pprof_addr"`
	TrustedSubnet *string `json:"trusted_subnet"`
}

// applyFileConfig накладывает значения из файла на cfg.
//
// Механизм наложения живёт в internal/config: здесь остаётся только
// описание полей сервера и имён соответствующих флагов. set сообщает,
// задан ли флаг явно: такие поля файл не перекрывает, а переменные
// окружения применяются вызывающей стороной уже после файла.
func applyFileConfig(cfg *config, path string, set func(name string) bool) error {
	// Пустой path просто не читает файл.
	var fc fileConfig
	if err := fileconfig.LoadFile(path, &fc); err != nil {
		return err
	}

	fileconfig.ApplyIf(&cfg.addr, fc.Address, set, "a")
	fileconfig.ApplyIf(&cfg.restore, fc.Restore, set, "r")
	fileconfig.ApplyIf(&cfg.filePath, fc.StoreFile, set, "f")
	fileconfig.ApplyIf(&cfg.databaseDSN, fc.DatabaseDSN, set, "d")
	fileconfig.ApplyIf(&cfg.cryptoKey, fc.CryptoKey, set, "crypto-key")
	fileconfig.ApplyIf(&cfg.key, fc.Key, set, "k")
	fileconfig.ApplyIf(&cfg.auditFile, fc.AuditFile, set, "audit-file")
	fileconfig.ApplyIf(&cfg.auditURL, fc.AuditURL, set, "audit-url")
	fileconfig.ApplyIf(&cfg.pprofAddr, fc.PprofAddr, set, "pprof-addr")
	fileconfig.ApplyIf(&cfg.trustedSubnet, fc.TrustedSubnet, set, "t")

	// store_interval: 0 — особое значение (синхронное сохранение),
	// поэтому разбирается отдельным парсером, который не пропускает
	// субсекундные длительности.
	return fileconfig.ApplyValue(&cfg.storeInterval, fc.StoreInterval, set, "i", fileconfig.ParseStoreInterval)
}
