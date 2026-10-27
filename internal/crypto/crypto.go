// Package crypto реализует асимметричное шифрование сообщений, которые агент
// отправляет серверу.
//
// Агент шифрует тело HTTP-запроса публичным ключом, сервер расшифровывает его
// приватным. Шифровать тело целиком одним RSA-блоком нельзя: блок RSA-2048
// вмещает не больше 245 байт открытого текста, а сериализованный батч метрик
// заметно больше. Поэтому используется гибридная схема (envelope encryption):
//
//  1. для каждого сообщения генерируется одноразовый ключ AES-256;
//  2. тело шифруется AES-256-GCM (аутентифицированное шифрование);
//  3. ключ AES-256 упаковывается ключом RSA через OAEP (SHA-256);
//  4. ключ, nonce и шифртекст складываются в конверт одного формата.
//
// Формат конверта (n — длина упакованного ключа, все длины в байтах):
//
//	[0:4]          магия "GMK1"
//	[4:6]          n, uint16 big endian
//	[6:6+n]        ключ AES-256, упакованный RSA-OAEP/SHA-256
//	[6+n:6+n+12]   nonce AES-GCM
//	[6+n+12:]      шифртекст AES-256-GCM вместе с тегом аутентификации
//
// Заголовок конверта (магия, длина, упакованный ключ, nonce) передаётся в GCM
// как associated data, поэтому подмена заголовка приводит к ошибке
// аутентификации при расшифровке.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	// envelopeMagic — префикс конверта, по нему распознаётся шифртекст.
	envelopeMagic = "GMK1"
	// aesKeySize — длина сессионного ключа AES-256.
	aesKeySize = 32
	// gcmNonceSize — стандартная длина nonce для GCM.
	gcmNonceSize = 12
	// gcmTagSize — длина тега аутентификации GCM.
	gcmTagSize = 16
	// headerFixed — магия и uint16 с длиной упакованного ключа.
	headerFixed = len(envelopeMagic) + 2
	// minEnvelopeSize — минимальный корректный конверт (без упакованного ключа).
	minEnvelopeSize = headerFixed + gcmNonceSize + gcmTagSize
	// maxWrappedKey — предел длины упакованного ключа, заданный форматом.
	maxWrappedKey = 1<<16 - 1
)

// ErrNotEnvelope возвращается, если сообщение не является конвертом: оно
// слишком короткое или начинается не с магии. По этой ошибке вызывающий код
// отличает «пришёл открытый текст» от «пришёл повреждённый шифртекст».
var ErrNotEnvelope = errors.New("crypto: сообщение не зашифровано")

// Encrypt шифрует plaintext публичным ключом pub и возвращает конверт.
//
// Размер plaintext не ограничен длиной ключа RSA: тело шифруется AES-256-GCM,
// а RSA-OAEP переносит только одноразовый ключ AES.
func Encrypt(pub *rsa.PublicKey, plaintext []byte) ([]byte, error) {
	if pub == nil {
		return nil, errors.New("crypto: публичный ключ не задан")
	}

	// 1. Одноразовый ключ AES-256: повторное использование ключа в GCM
	// недопустимо, поэтому на каждое сообщение генерируется свой.
	cek := make([]byte, aesKeySize)
	if _, err := rand.Read(cek); err != nil {
		return nil, fmt.Errorf("crypto: не удалось сгенерировать сессионный ключ: %w", err)
	}

	// 2. Ключ AES упаковывается публичным ключом RSA.
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, cek, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: не удалось упаковать сессионный ключ: %w", err)
	}
	if len(wrapped) > maxWrappedKey {
		return nil, fmt.Errorf("crypto: упакованный ключ слишком велик: %d байт", len(wrapped))
	}

	aead, err := newAEAD(cek)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcmNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: не удалось сгенерировать nonce: %w", err)
	}

	header := make([]byte, 0, headerFixed+len(wrapped)+gcmNonceSize)
	header = append(header, envelopeMagic...)
	header = binary.BigEndian.AppendUint16(header, uint16(len(wrapped)))
	header = append(header, wrapped...)
	header = append(header, nonce...)

	// 3. Заголовок идёт как associated data — GCM проверит его целостность.
	ciphertext := aead.Seal(nil, nonce, plaintext, header)

	return append(header, ciphertext...), nil
}

// Decrypt расшифровывает конверт приватным ключом priv.
//
// Если сообщение не является конвертом, возвращается ErrNotEnvelope; любая
// другая ошибка означает повреждённый конверт или неподходящий ключ.
func Decrypt(priv *rsa.PrivateKey, envelope []byte) ([]byte, error) {
	if priv == nil {
		return nil, errors.New("crypto: приватный ключ не задан")
	}
	if !isEnvelope(envelope) {
		return nil, ErrNotEnvelope
	}

	wrappedLen := int(binary.BigEndian.Uint16(envelope[len(envelopeMagic):headerFixed]))
	headerLen := headerFixed + wrappedLen + gcmNonceSize
	if wrappedLen == 0 || len(envelope) < headerLen+gcmTagSize {
		return nil, fmt.Errorf("crypto: повреждён конверт: %d байт не соответствуют заголовку", len(envelope))
	}

	header := envelope[:headerLen]
	wrapped := envelope[headerFixed : headerFixed+wrappedLen]
	nonce := envelope[headerFixed+wrappedLen : headerLen]
	ciphertext := envelope[headerLen:]

	cek, err := rsa.DecryptOAEP(sha256.New(), nil, priv, wrapped, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: не удалось распаковать сессионный ключ: %w", err)
	}

	aead, err := newAEAD(cek)
	if err != nil {
		return nil, err
	}

	plaintext, err := aead.Open(nil, nonce, ciphertext, header)
	if err != nil {
		return nil, fmt.Errorf("crypto: не удалось расшифровать сообщение: %w", err)
	}
	return plaintext, nil
}

// isEnvelope проверяет, что данные начинаются с магии конверта и имеют
// минимально возможную длину.
func isEnvelope(data []byte) bool {
	return len(data) >= minEnvelopeSize && string(data[:len(envelopeMagic)]) == envelopeMagic
}

// newAEAD создаёт AES-256-GCM для сессионного ключа cek.
func newAEAD(cek []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, fmt.Errorf("crypto: не удалось создать AES-шифр: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: не удалось создать GCM: %w", err)
	}
	return aead, nil
}

// LoadPublicKey читает публичный ключ RSA из PEM-файла по пути path.
//
// Поддерживаются оба распространённых формата: PKIX («PUBLIC KEY», его пишет
// `openssl rsa -pubout`) и PKCS#1 («RSA PUBLIC KEY»).
func LoadPublicKey(path string) (*rsa.PublicKey, error) {
	block, err := readPEMBlock(path, "публичным")
	if err != nil {
		return nil, err
	}

	if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		pub, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("crypto: файл %q: ожидался RSA-ключ, получен %T", path, parsed)
		}
		return pub, nil
	}

	pub, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf(
			"crypto: файл %q: не удалось разобрать публичный ключ (нужен PEM-блок «PUBLIC KEY» или «RSA PUBLIC KEY»): %w",
			path, err,
		)
	}
	return pub, nil
}

// LoadPrivateKey читает приватный ключ RSA из PEM-файла по пути path.
//
// Поддерживаются PKCS#1 («RSA PRIVATE KEY») и PKCS#8 («PRIVATE KEY») — второй
// формат по умолчанию пишут OpenSSL 3.x и `openssl genpkey`.
func LoadPrivateKey(path string) (*rsa.PrivateKey, error) {
	block, err := readPEMBlock(path, "приватным")
	if err != nil {
		return nil, err
	}

	if priv, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return priv, nil
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf(
			"crypto: файл %q: не удалось разобрать приватный ключ (нужен PEM-блок «RSA PRIVATE KEY» или «PRIVATE KEY»): %w",
			path, err,
		)
	}

	priv, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("crypto: файл %q: ожидался RSA-ключ, получен %T", path, parsed)
	}
	return priv, nil
}

// readPEMBlock читает файл по пути path и возвращает первый PEM-блок.
// forKey подставляется в сообщения об ошибках («с публичным ключом» и т. п.).
func readPEMBlock(path string, forKey string) (*pem.Block, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("crypto: путь к файлу с " + forKey + " ключом не задан")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("crypto: не удалось прочитать файл с %s ключом %q: %w", forKey, path, err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("crypto: файл %q не содержит PEM-блока", path)
	}
	return block, nil
}
