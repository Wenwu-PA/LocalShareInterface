package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func EnsureTLS(directory string) (string, string, error) {
	certPath, keyPath := filepath.Join(directory, "server.crt"), filepath.Join(directory, "server.key")
	if _, err := os.Stat(certPath); err == nil {
		if _, err = os.Stat(keyPath); err != nil {
			return "", "", fmt.Errorf("сертификат существует, но ключ недоступен: %w", err)
		}
		return certPath, keyPath, nil
	}
	if _, err := os.Stat(keyPath); err == nil {
		return "", "", fmt.Errorf("ключ существует без сертификата; сохраните его и настройте пару явно")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	now := time.Now()
	t := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "LANBridge"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(1, 0, 0), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	if addrs, e := net.InterfaceAddrs(); e == nil {
		for _, addr := range addrs {
			if ip, _, e := net.ParseCIDR(addr.String()); e == nil && ip.IsPrivate() {
				t.IPAddresses = append(t.IPAddresses, ip)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &t, &t, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}), 0600); err != nil {
		return "", "", err
	}
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}
