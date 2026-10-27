package crypto

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testKeyOnce sync.Once
	testKey     *rsa.PrivateKey
	testKeyErr  error
)

// testPrivateKey возвращает один RSA-ключ на весь пакет: генерация ключа
// заметно дороже самих тестов.
func testPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	testKeyOnce.Do(func() {
		testKey, testKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	require.NoError(t, testKeyErr)
	return testKey
}

// writePEMFile сохраняет DER в PEM-файл с указанным типом блока и возвращает
// путь к нему.
func writePEMFile(t *testing.T, blockType string, der []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

// TestEncryptDecrypt_RoundTrip проверяет, что сообщение любого размера
// переживает шифрование и расшифровку. Размеры 245 и 246 байт — граница
// одного блока RSA-2048 (size-11), за которую прямое RSA-шифрование уже
// не работает: гибридная схема должна проходить её без ограничений.
func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	priv := testPrivateKey(t)

	for _, size := range []int{0, 1, 100, 245, 246, 1024, 64 * 1024, 1024 * 1024} {
		plaintext := make([]byte, size)
		_, err := rand.Read(plaintext)
		require.NoError(t, err)

		envelope, err := Encrypt(&priv.PublicKey, plaintext)
		require.NoError(t, err, "размер %d", size)
		require.Greater(t, len(envelope), len(plaintext), "размер %d: конверт должен быть больше открытого текста", size)

		decrypted, err := Decrypt(priv, envelope)
		require.NoError(t, err, "размер %d", size)
		assert.True(t, bytes.Equal(plaintext, decrypted), "размер %d: открытый текст не совпал", size)
	}
}

// TestEncrypt_UniqueEnvelopes проверяет, что одно и то же сообщение шифруется
// в разные конверты: сессионный ключ и nonce генерируются заново.
func TestEncrypt_UniqueEnvelopes(t *testing.T) {
	priv := testPrivateKey(t)
	plaintext := []byte("same payload")

	first, err := Encrypt(&priv.PublicKey, plaintext)
	require.NoError(t, err)
	second, err := Encrypt(&priv.PublicKey, plaintext)
	require.NoError(t, err)

	assert.False(t, bytes.Equal(first, second), "конверты одинаковы: ключ или nonce переиспользованы")

	for _, envelope := range [][]byte{first, second} {
		decrypted, err := Decrypt(priv, envelope)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted)
	}
}

// TestDecrypt_NotEnvelope проверяет, что открытые и слишком короткие
// сообщения распознаются отдельной ошибкой ErrNotEnvelope.
func TestDecrypt_NotEnvelope(t *testing.T) {
	priv := testPrivateKey(t)

	cases := map[string][]byte{
		"nil":               nil,
		"пустое":            {},
		"открытый JSON":     []byte(`[{"id":"g1","type":"gauge","value":1.5}]`),
		"чужая магия":       append([]byte("XXXX"), make([]byte, 64)...),
		"только магия":      []byte(envelopeMagic),
		"короче минимума":   []byte("GMK1\x00\x01"),
		"мусорные 34 байта": bytes.Repeat([]byte{1}, minEnvelopeSize),
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Decrypt(priv, data)
			require.ErrorIs(t, err, ErrNotEnvelope)
		})
	}
}

// TestDecrypt_Tampered проверяет, что любое искажение конверта (кроме магии,
// которая отсекается как ErrNotEnvelope) приводит к ошибке аутентификации.
func TestDecrypt_Tampered(t *testing.T) {
	priv := testPrivateKey(t)
	plaintext := []byte("payload that must stay intact")

	envelope, err := Encrypt(&priv.PublicKey, plaintext)
	require.NoError(t, err)

	wrappedLen := int(binary.BigEndian.Uint16(envelope[len(envelopeMagic):headerFixed]))
	nonceStart := headerFixed + wrappedLen
	require.Greater(t, len(envelope), nonceStart+gcmNonceSize)

	mutated := func(at int) []byte {
		out := bytes.Clone(envelope)
		out[at] ^= 0xff
		return out
	}

	badLength := bytes.Clone(envelope)
	badLength[4], badLength[5] = 0xff, 0xff

	zeroLength := bytes.Clone(envelope)
	zeroLength[4], zeroLength[5] = 0, 0

	cases := map[string][]byte{
		"упакованный ключ": mutated(headerFixed + 10),
		"nonce":            mutated(nonceStart + 3),
		"тег":              mutated(len(envelope) - gcmTagSize),
		"шифртекст":        mutated(len(envelope) - 1),
		"обрезанный":       envelope[:len(envelope)-1],
		"длина ключа 0":    zeroLength,
		"длина ключа больше данных": badLength,
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			decrypted, err := Decrypt(priv, data)
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrNotEnvelope, "повреждённый конверт не должен считаться открытым текстом")
			assert.Nil(t, decrypted)
		})
	}
}

// TestDecrypt_WrongKey проверяет, что чужим приватным ключом конверт
// не расшифровывается.
func TestDecrypt_WrongKey(t *testing.T) {
	priv := testPrivateKey(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	envelope, err := Encrypt(&priv.PublicKey, []byte("payload"))
	require.NoError(t, err)

	decrypted, err := Decrypt(other, envelope)
	require.Error(t, err)
	assert.Nil(t, decrypted)
}

// TestEncrypt_NilKey проверяет ошибку при незаданном публичном ключе.
func TestEncrypt_NilKey(t *testing.T) {
	_, err := Encrypt(nil, []byte("payload"))
	require.Error(t, err)
}

// TestDecrypt_NilKey проверяет ошибку при незаданном приватном ключе.
func TestDecrypt_NilKey(t *testing.T) {
	_, err := Decrypt(nil, []byte("payload"))
	require.Error(t, err)
}

// TestLoadPublicKey проверяет оба поддерживаемых формата публичного ключа.
func TestLoadPublicKey(t *testing.T) {
	priv := testPrivateKey(t)

	pkix, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	require.NoError(t, err)

	tests := map[string]struct {
		blockType string
		der       []byte
	}{
		"PKIX":  {blockType: "PUBLIC KEY", der: pkix},
		"PKCS1": {blockType: "RSA PUBLIC KEY", der: x509.MarshalPKCS1PublicKey(&priv.PublicKey)},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			pub, err := LoadPublicKey(writePEMFile(t, tc.blockType, tc.der))
			require.NoError(t, err)
			assert.True(t, pub.Equal(&priv.PublicKey))
		})
	}
}

// TestLoadPrivateKey проверяет оба поддерживаемых формата приватного ключа:
// PKCS#1 и PKCS#8 (последний пишут OpenSSL 3.x и openssl genpkey).
func TestLoadPrivateKey(t *testing.T) {
	priv := testPrivateKey(t)

	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)

	tests := map[string]struct {
		blockType string
		der       []byte
	}{
		"PKCS1": {blockType: "RSA PRIVATE KEY", der: x509.MarshalPKCS1PrivateKey(priv)},
		"PKCS8": {blockType: "PRIVATE KEY", der: pkcs8},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			loaded, err := LoadPrivateKey(writePEMFile(t, tc.blockType, tc.der))
			require.NoError(t, err)
			assert.True(t, loaded.Equal(priv))
		})
	}
}

// TestLoadKey_Errors проверяет понятные ошибки на типичных проблемах
// с файлами ключей.
func TestLoadKey_Errors(t *testing.T) {
	priv := testPrivateKey(t)

	notPEM := filepath.Join(t.TempDir(), "bad.pem")
	require.NoError(t, os.WriteFile(notPEM, []byte("not a pem"), 0o600))

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecPublic, err := x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	require.NoError(t, err)
	ecPrivate, err := x509.MarshalPKCS8PrivateKey(ecKey)
	require.NoError(t, err)

	publicPEM := writePEMFile(t, "PUBLIC KEY", mustPKIX(t, &priv.PublicKey))
	privatePEM := writePEMFile(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(priv))

	t.Run("пустой путь", func(t *testing.T) {
		_, err := LoadPublicKey("")
		require.Error(t, err)
		_, err = LoadPrivateKey("   ")
		require.Error(t, err)
	})

	t.Run("файла нет", func(t *testing.T) {
		_, err := LoadPublicKey(filepath.Join(t.TempDir(), "absent.pem"))
		require.Error(t, err)
		_, err = LoadPrivateKey(filepath.Join(t.TempDir(), "absent.pem"))
		require.Error(t, err)
	})

	t.Run("не PEM", func(t *testing.T) {
		_, err := LoadPublicKey(notPEM)
		require.ErrorContains(t, err, "PEM")
		_, err = LoadPrivateKey(notPEM)
		require.ErrorContains(t, err, "PEM")
	})

	t.Run("публичный ключ не RSA", func(t *testing.T) {
		_, err := LoadPublicKey(writePEMFile(t, "PUBLIC KEY", ecPublic))
		require.ErrorContains(t, err, "ожидался RSA-ключ")
	})

	t.Run("приватный ключ не RSA", func(t *testing.T) {
		_, err := LoadPrivateKey(writePEMFile(t, "PRIVATE KEY", ecPrivate))
		require.ErrorContains(t, err, "ожидался RSA-ключ")
	})

	t.Run("перепутаны файлы", func(t *testing.T) {
		// Приватный ключ вместо публичного и наоборот.
		_, err := LoadPublicKey(privatePEM)
		require.Error(t, err)
		_, err = LoadPrivateKey(publicPEM)
		require.Error(t, err)
	})
}

// mustPKIX кодирует публичный ключ в PKIX DER.
func mustPKIX(t *testing.T, pub *rsa.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	return der
}
