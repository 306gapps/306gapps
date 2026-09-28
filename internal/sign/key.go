// Package sign applies the JAR signature recoveries check on a flashable zip.
package sign

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Key is a signing identity: a private key and its certificate.
type Key struct {
	Private *rsa.PrivateKey
	Cert    *x509.Certificate
	// DER is the certificate as encoded, which the signature embeds.
	DER []byte
}

// Fixed so a generated certificate, and the signatures made with it, are reproducible.
var notBefore = time.Date(2009, time.January, 1, 0, 0, 0, 0, time.UTC)

// Generate creates a self-signed signing identity, which is all a recovery zip needs.
func Generate(name string) (*Key, error) {
	return generateSized(name, 4096)
}

func generateSized(name string, bits int) (*Key, error) {
	priv, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   name,
			Organization: []string{name},
		},
		NotBefore:             notBefore,
		NotAfter:              notBefore.AddDate(100, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Key{Private: priv, Cert: cert, DER: der}, nil
}

// Save writes the key and certificate as PEM.
func (k *Key) Save(keyPath, certPath string) error {
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(k.Private)
	if err != nil {
		return err
	}
	// The private key is readable only by its owner.
	if err := writePEM(keyPath, "PRIVATE KEY", keyDER, 0o600); err != nil {
		return err
	}
	return writePEM(certPath, "CERTIFICATE", k.DER, 0o644)
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		return err
	}
	return f.Close()
}

// Load reads a key and certificate from PEM files.
func Load(keyPath, certPath string) (*Key, error) {
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}

	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, fmt.Errorf("%s is not PEM", keyPath)
	}
	cb, _ := pem.Decode(certPEM)
	if cb == nil {
		return nil, fmt.Errorf("%s is not PEM", certPath)
	}

	var priv *rsa.PrivateKey
	switch kb.Type {
	case "RSA PRIVATE KEY":
		priv, err = x509.ParsePKCS1PrivateKey(kb.Bytes)
	default:
		var any any
		any, err = x509.ParsePKCS8PrivateKey(kb.Bytes)
		if err == nil {
			var ok bool
			if priv, ok = any.(*rsa.PrivateKey); !ok {
				return nil, fmt.Errorf("%s is not an RSA key", keyPath)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", keyPath, err)
	}

	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", certPath, err)
	}
	return &Key{Private: priv, Cert: cert, DER: cb.Bytes}, nil
}

// LoadOrGenerate returns the identity in dir, creating one on first use.
func LoadOrGenerate(dir, name string) (*Key, bool, error) {
	keyPath := filepath.Join(dir, "signing.key")
	certPath := filepath.Join(dir, "signing.crt")

	k, err := Load(keyPath, certPath)
	if err == nil {
		return k, false, nil
	}
	if !os.IsNotExist(err) {
		// An unreadable existing key is worth reporting, not replacing.
		return nil, false, err
	}

	k, err = Generate(name)
	if err != nil {
		return nil, false, err
	}
	if err := k.Save(keyPath, certPath); err != nil {
		return nil, false, err
	}
	return k, true, nil
}
