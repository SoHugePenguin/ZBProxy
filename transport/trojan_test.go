package transport

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/layou233/zbproxy/v3/config"
)

func testCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "proxy.test"}, DNSNames: []string{"proxy.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestTLSAuthenticationAndCertificateVerification(t *testing.T) {
	certFile, keyFile := testCertificate(t)
	server, err := NewServer(&config.Encryption{Enabled: true, CertFile: certFile, KeyFile: keyFile, Password: "correct-password"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, password, serverName string
		trustCA, accepted          bool
	}{
		{"valid", "correct-password", "proxy.test", true, true},
		{"wrong password", "wrong-password", "proxy.test", true, false},
		{"wrong hostname", "correct-password", "wrong.test", true, false},
		{"untrusted certificate", "correct-password", "proxy.test", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := config.EncryptionClient{Enabled: true, Password: tc.password, ServerAddress: "127.0.0.1", ServerPort: 443, ServerName: tc.serverName}
			if tc.trustCA {
				settings.CACertFile = certFile
			}
			clientConfig, err := clientTLS(settings)
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				raw, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer raw.Close()
				conn, err := server.Accept(context.Background(), raw)
				if err == nil {
					var payload [4]byte
					_, err = io.ReadFull(conn, payload[:])
					if err == nil {
						_, err = conn.Write(payload[:])
					}
				}
				done <- err
			}()
			dialer := &tls.Dialer{Config: clientConfig, NetDialer: &net.Dialer{Timeout: 3 * time.Second}}
			conn, dialErr := dialer.DialContext(context.Background(), "tcp", listener.Addr().String())
			if dialErr == nil {
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				var request bytes.Buffer
				if err := writeRequest(&request, tc.password); err != nil {
					t.Fatal(err)
				}
				request.WriteString("game")
				_, writeErr := conn.Write(request.Bytes())
				if tc.accepted {
					if writeErr != nil {
						t.Fatal(writeErr)
					}
					var payload [4]byte
					if _, err := io.ReadFull(conn, payload[:]); err != nil || string(payload[:]) != "game" {
						t.Fatalf("payload lost: %q, %v", payload, err)
					}
				}
				conn.Close()
			} else if tc.accepted {
				t.Fatal(dialErr)
			}
			select {
			case err := <-done:
				if (err == nil) != tc.accepted {
					t.Fatalf("accepted=%v, server error=%v", tc.accepted, err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("authentication did not finish")
			}
			if !tc.trustCA || tc.serverName != "proxy.test" {
				if dialErr == nil {
					t.Fatal("certificate verification unexpectedly succeeded")
				}
			}
		})
	}
}

func TestTrojanRequestValidation(t *testing.T) {
	hash := passwordHash("password")
	var valid bytes.Buffer
	if err := writeRequest(&valid, "password"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		edit     func([]byte) []byte
		accepted bool
	}{
		{"IPv4", func(b []byte) []byte { return b }, true},
		{"IPv6", func(b []byte) []byte {
			return append(append(b[:59], append([]byte{4}, make([]byte, 16)...)...), 0, 80, '\r', '\n')
		}, true},
		{"domain", func(b []byte) []byte { return append(append(b[:59], 3, 3, 'a', '.', 'b'), 0, 80, '\r', '\n') }, true},
		{"empty domain", func(b []byte) []byte { return append(b[:59], 3, 0) }, false},
		{"UDP", func(b []byte) []byte { b[58] = 3; return b }, false},
		{"bad address type", func(b []byte) []byte { b[59] = 9; return b }, false},
		{"bad password delimiter", func(b []byte) []byte { b[56] = 0; return b }, false},
		{"bad request terminator", func(b []byte) []byte { b[len(b)-1] = 0; return b }, false},
		{"truncated", func(b []byte) []byte { return b[:len(b)-1] }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.edit(append([]byte{}, valid.Bytes()...))
			r := bytes.NewReader(append(data, []byte("payload")...))
			err := readRequest(r, hash)
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted=%v, err=%v", tc.accepted, err)
			}
			if tc.accepted {
				rest, _ := io.ReadAll(r)
				if string(rest) != "payload" {
					t.Fatalf("payload consumed: %q", rest)
				}
			}
		})
	}
}

func TestEncryptionFailsClosed(t *testing.T) {
	if server, err := NewServer(&config.Encryption{Enabled: false, CertFile: "missing"}); err != nil || server != nil {
		t.Fatal("disabled encryption changed plaintext behavior")
	}
	if _, err := NewServer(&config.Encryption{Enabled: true}); err == nil {
		t.Fatal("empty password accepted")
	}
	if _, err := NewServer(&config.Encryption{Enabled: true, Password: "secret", CertFile: "missing", KeyFile: "missing"}); err == nil {
		t.Fatal("missing certificate accepted")
	}
}
