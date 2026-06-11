package serve

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// tlsCertFileName is the persisted self-signed certificate + private
// key, PEM-encoded, under the XDG state dir. Persisting (rather than
// minting per process) keeps the cert hash — which IS the device
// fingerprint in LocalSend's HTTPS mode (protocol §2) — stable across
// restarts, so sender apps recognize the receiver as the same device.
const tlsCertFileName = "localsend-tls.pem"

// certValidity mirrors the LocalSend app's own 10-year self-signed
// certificate. Senders pin the cert hash instead of validating chains,
// so expiry is cosmetic; long validity avoids fingerprint churn.
const certValidity = 10 * 365 * 24 * time.Hour

// loadOrCreateTLSCert returns the persisted serve certificate, minting
// and persisting a fresh self-signed one on first use.
func loadOrCreateTLSCert(path string) (tls.Certificate, error) {
	pemBytes, err := os.ReadFile(path)
	switch {
	case err == nil:
		cert, parseErr := parseCertPEM(pemBytes)
		if parseErr != nil {
			return tls.Certificate{}, errors.Wrapf(parseErr, "parsing %s", path)
		}
		return cert, nil
	case os.IsNotExist(err):
		return mintAndPersistCert(path)
	default:
		return tls.Certificate{}, errors.Wrap(err)
	}
}

// parseCertPEM builds the keypair from the combined cert+key PEM file.
// tls.X509KeyPair tolerates the combined layout: it collects
// CERTIFICATE blocks from its first argument and skips ahead to the
// private-key block in its second, so the same bytes serve as both.
func parseCertPEM(pemBytes []byte) (tls.Certificate, error) {
	cert, err := tls.X509KeyPair(pemBytes, pemBytes)
	if err != nil {
		return tls.Certificate{}, errors.Wrap(err)
	}
	return cert, nil
}

func mintAndPersistCert(path string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, errors.Wrap(err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, errors.Wrap(err)
	}

	now := time.Now()
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "cutting-garden"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(
		rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, errors.Wrap(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, errors.Wrap(err)
	}

	var buf []byte
	buf = append(buf, pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	buf = append(buf, pem.EncodeToMemory(
		&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})...)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return tls.Certificate{}, errors.Wrap(err)
	}
	// 0600: the file carries the private key.
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return tls.Certificate{}, errors.Wrap(err)
	}

	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}, nil
}

// certFingerprint is the LocalSend HTTPS-mode device fingerprint:
// uppercase hex SHA-256 of the leaf certificate DER (protocol §2;
// matches the app's calculateHashOfCertificate).
func certFingerprint(cert tls.Certificate) string {
	sum := sha256.Sum256(cert.Certificate[0])
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// tlsServerConfig is the listener config for HTTPS mode. The cert is
// self-signed; LocalSend clients pin its hash rather than validate a
// chain, so no client auth and no chain building.
func tlsServerConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
}
