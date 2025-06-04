package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256" // For fingerprint, if used here
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex" // For fingerprint
	"encoding/pem"
	"fmt"
	"math/big"
	// "net" // Removed as it's not used for now
	"os"
	"path/filepath"
	"time"
)

const (
	certFileName = "cert.pem"
	keyFileName  = "key.pem"
)

// EnsureTLSConfig checks for existing TLS certificate and key. If they don't exist,
// it generates them. It then loads them and returns a tls.Config.
// configDir is the directory like ".filesync" within the sync folder.
// deviceID is used as the CommonName for the self-signed certificate.
func EnsureTLSConfig(configDir string, deviceID string) (*tls.Config, error) {
	if deviceID == "" {
		return nil, fmt.Errorf("deviceID cannot be empty for TLS certificate generation")
	}

	certPath := filepath.Join(configDir, certFileName)
	keyPath := filepath.Join(configDir, keyFileName)

	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)

	if os.IsNotExist(certErr) || os.IsNotExist(keyErr) {
		fmt.Printf("TLS certificate or key not found in %s. Generating new ones...\n", configDir)

		// Generate private key (ECDSA P256)
		privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("failed to generate private key: %w", err)
		}

		// Create certificate template
		serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128) // 128-bit serial number
		serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
		if err != nil {
			return nil, fmt.Errorf("failed to generate serial number: %w", err)
		}

		notBefore := time.Now()
		notAfter := notBefore.Add(10 * 365 * 24 * time.Hour) // 10 years validity

		template := x509.Certificate{
			SerialNumber: serialNumber,
			Subject: pkix.Name{
				CommonName:   deviceID, // Use deviceID as CN
				Organization: []string{"FileSync Peer"},
			},
			NotBefore: notBefore,
			NotAfter:  notAfter,

			KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
			BasicConstraintsValid: true,
			IsCA:                  false, // This is a leaf certificate
			// IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")}, // Optional: Add SANs
			// DNSNames:              []string{deviceID, "localhost"},    // Optional: Add SANs
		}

		// Create certificate
		derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privKey.PublicKey, privKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create certificate: %w", err)
		}

		// Save private key to key.pem
		keyFile, err := os.Create(keyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create key file %s: %w", keyPath, err)
		}
		defer keyFile.Close()
		privKeyBytes, err := x509.MarshalECPrivateKey(privKey)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal private key: %w", err)
		}
		if err := pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privKeyBytes}); err != nil {
			return nil, fmt.Errorf("failed to write private key to %s: %w", keyPath, err)
		}
		fmt.Printf("Saved private key to %s\n", keyPath)

		// Save certificate to cert.pem
		certFile, err := os.Create(certPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create certificate file %s: %w", certPath, err)
		}
		defer certFile.Close()
		if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
			return nil, fmt.Errorf("failed to write certificate to %s: %w", certPath, err)
		}
		fmt.Printf("Saved certificate to %s\n", certPath)
		fmt.Println("Generated new TLS certificate and key.")
	} else if certErr != nil || keyErr != nil {
		// One exists but not the other, or other errors
		return nil, fmt.Errorf("certificate/key state inconsistent or error: certErr=%v, keyErr=%v", certErr, keyErr)
	} else {
		fmt.Printf("TLS certificate and key found in %s.\n", configDir)
	}

	// Load the key pair to create tls.Config
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load key pair from %s and %s: %w", certPath, keyPath, err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12, // Enforce modern TLS
	}, nil
}

// GetCertFingerprint computes the SHA256 fingerprint of a certificate.
func GetCertFingerprint(cert *x509.Certificate) string {
	derBytes := cert.Raw // The DER-encoded form of the certificate
	hash := sha256.Sum256(derBytes)
	return hex.EncodeToString(hash[:])
}
