// Package hash считает HMAC-SHA256 подпись тела HTTP-запроса.
// Подпись передаётся в заголовке HashSHA256 и позволяет серверу
// убедиться, что тело запроса не было изменено по пути.
package hash

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Compute возвращает hex-представление HMAC-SHA256 от body с ключом key.
// Пустой key допустим, но не даёт защиты: обе стороны должны
// использовать один и тот же ключ.
func Compute(body []byte, key string) string {
	h := hmac.New(sha256.New, []byte(key))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}
