// Package grpctls настраивает TLS для gRPC-транспорта метрик.
//
// Если файлы сертификата и ключа не заданы, сервер получает самоподписанный
// сертификат, сгенерированный в памяти: это позволяет запустить gRPC без
// подготовки сертификатов (учебный сценарий). Агент без CA-сертификата
// подключается по TLS без проверки сервера — трафик шифруется, но личность
// сервера не подтверждается. Для продакшена задавайте cert/key и CA явно.
package grpctls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"

	"google.golang.org/grpc/credentials"
)

// minTLSVersion — минимальная версия TLS для gRPC-канала.
const minTLSVersion = tls.VersionTLS12

// ServerCredentials готовит TLS-credentials для gRPC-сервера.
//
//   - certFile и keyFile заданы — используются эти файлы;
//   - оба пусты — генерируется самоподписанный сертификат (generated = true);
//   - задан только один — ошибка: неполная пара почти всегда опечатка.
func ServerCredentials(certFile, keyFile string) (credentials.TransportCredentials, bool, error) {
	switch {
	case certFile == "" && keyFile == "":
		cert, _, err := GenerateSelfSigned()
		if err != nil {
			return nil, false, err
		}

		creds := credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   minTLSVersion,
		})
		return creds, true, nil

	case certFile == "" || keyFile == "":
		return nil, false, errors.New("grpctls: задайте оба файла — сертификат и ключ")

	default:
		creds, err := credentials.NewServerTLSFromFile(certFile, keyFile)
		if err != nil {
			return nil, false, fmt.Errorf("grpctls: не удалось загрузить сертификат %q: %w", certFile, err)
		}
		return creds, false, nil
	}
}

// ClientCredentials готовит TLS-credentials для gRPC-клиента.
//
// Пустой caFile означает «доверять любому сертификату»: соединение остаётся
// зашифрованным, но сервер не аутентифицируется. Это осознанный fallback для
// запуска без файлов; если caFile задан, сертификат сервера проверяется.
func ClientCredentials(caFile string) (credentials.TransportCredentials, error) {
	if caFile == "" {
		// InsecureSkipVerify здесь не «отключает TLS», а только проверку
		// цепочки: без CA проверить сервер нечем. Канал всё равно шифруется.
		return credentials.NewTLS(&tls.Config{ //nolint:gosec // осознанный fallback без CA
			InsecureSkipVerify: true,
			MinVersion:         minTLSVersion,
		}), nil
	}

	creds, err := credentials.NewClientTLSFromFile(caFile, "")
	if err != nil {
		return nil, fmt.Errorf("grpctls: не удалось загрузить CA-сертификат %q: %w", caFile, err)
	}
	return creds, nil
}

// GenerateSelfSigned создаёт самоподписанный сертификат для gRPC-сервера.
// В SAN попадают localhost, 127.0.0.1, ::1, имя хоста и локальные адреса
// интерфейсов — чтобы к серверу можно было подключиться по любому из них.
//
// Возвращаются готовый tls.Certificate и PEM сертификата (второй пригодится,
// если сертификат нужно показать или сохранить).
func GenerateSelfSigned() (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("grpctls: не удалось сгенерировать ключ: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("grpctls: не удалось сгенерировать серийный номер: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "gometrics-grpc"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	if host, err := os.Hostname(); err == nil && host != "" {
		tmpl.DNSNames = append(tmpl.DNSNames, host)
	}
	for _, addr := range localIPs() {
		tmpl.IPAddresses = append(tmpl.IPAddresses, addr)
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("grpctls: не удалось создать сертификат: %w", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("grpctls: не удалось сериализовать ключ: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("grpctls: не удалось собрать пару сертификат/ключ: %w", err)
	}

	return cert, certPEM, nil
}

// localIPs возвращает IPv4- и IPv6-адреса интерфейсов, кроме loopback:
// сертификат должен подходить при подключении по адресу машины в сети.
func localIPs() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}

	var result []net.IP
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		result = append(result, ip)
	}
	return result
}
