// Package testcerts выпускает в тестах одноразовый CA и подписанные им
// серверный и клиентский сертификаты — для проверки mTLS без файлов в репо.
package testcerts

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Set — выпущенные сертификаты: пути к PEM-файлам плюс готовый конфиг сервера,
// требующего клиентский сертификат от этого же CA.
type Set struct {
	CAFile, ClientCert, ClientKey string
	// Server требует и проверяет клиентский сертификат.
	Server *tls.Config
}

// New выпускает CA, серверный сертификат на 127.0.0.1/localhost и клиентский,
// кладёт PEM-файлы во временный каталог теста.
func New(t testing.TB) Set {
	t.Helper()
	dir := t.TempDir()
	caKey, caCert, caDER := issue(t, nil, nil, &x509.Certificate{
		Subject:               pkix.Name{CommonName: "test CA"},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	})
	srvKey, _, srvDER := issue(t, caCert, caKey, &x509.Certificate{
		Subject:     pkix.Name{CommonName: "localhost"},
		DNSNames:    []string{"localhost"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	cliKey, _, cliDER := issue(t, caCert, caKey, &x509.Certificate{
		Subject:     pkix.Name{CommonName: "orchestrator"},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})

	s := Set{
		CAFile:     write(t, dir, "ca.crt", "CERTIFICATE", caDER),
		ClientCert: write(t, dir, "client.crt", "CERTIFICATE", cliDER),
		ClientKey:  write(t, dir, "client.key", "PRIVATE KEY", pkcs8(t, cliKey)),
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	s.Server = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{srvDER}, PrivateKey: srvKey}},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	return s
}

var serial int64

func issue(t testing.TB, parent *x509.Certificate, parentKey *ecdsa.PrivateKey,
	tmpl *x509.Certificate) (*ecdsa.PrivateKey, *x509.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial++
	tmpl.SerialNumber = big.NewInt(serial)
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = time.Now().Add(time.Hour)
	if parent == nil {
		parent, parentKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return key, cert, der
}

func pkcs8(t testing.TB, k *ecdsa.PrivateKey) []byte {
	t.Helper()
	b, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func write(t testing.TB, dir, name, typ string, der []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
