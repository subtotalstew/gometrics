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
	if fc.Restore != nil && !set("r") {
		cfg.restore = *fc.Restore
	}
	if fc.StoreInterval != nil && !set("i") {
		n, err := fileconfig.ParseSeconds(*fc.StoreInterval)
		if err != nil {
			return err
		}
		cfg.storeInterval = n
	}
	if fc.StoreFile != nil && !set("f") {
		cfg.filePath = *fc.StoreFile
	}
	if fc.DatabaseDSN != nil && !set("d") {
		cfg.databaseDSN = *fc.DatabaseDSN
	}
	if fc.CryptoKey != nil && !set("crypto-key") {
		cfg.cryptoKey = *fc.CryptoKey
	}
	if fc.Key != nil && !set("k") {
		cfg.key = *fc.Key
	}
	if fc.AuditFile != nil && !set("audit-file") {
		cfg.auditFile = *fc.AuditFile
	}
	if fc.AuditURL != nil && !set("audit-url") {
		cfg.auditURL = *fc.AuditURL
	}
	if fc.PprofAddr != nil && !set("pprof-addr") {
		cfg.pprofAddr = *fc.PprofAddr
	}
	return nil
}
