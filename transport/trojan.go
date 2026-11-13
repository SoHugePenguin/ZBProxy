// Package transport provides authenticated, TCP-only Trojan over TLS.
// Destination fields are consumed for interoperability, but never used for
// routing: the existing ZBProxy rules always determine the upstream server.
package transport

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/layou233/zbproxy/v3/config"
)

const handshakeTimeout = 10 * time.Second

type Server struct {
	tlsConfig *tls.Config
	password  [56]byte
}

func passwordHash(password string) (out [56]byte) {
	hash := sha256.Sum224([]byte(password))
	hex.Encode(out[:], hash[:])
	return
}

func NewServer(settings *config.Encryption) (*Server, error) {
	if settings == nil || !settings.Enabled {
		return nil, nil
	}
	if settings.Password == "" {
		return nil, errors.New("Encryption.Password is required when encryption is enabled")
	}
	cert, err := tls.LoadX509KeyPair(settings.CertFile, settings.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load Encryption certificate/key: %w", err)
	}
	return &Server{
		tlsConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}},
		password:  passwordHash(settings.Password),
	}, nil
}

// Accept authenticates before exposing any bytes to Minecraft sniffing.
func (s *Server) Accept(ctx context.Context, raw net.Conn) (net.Conn, error) {
	conn := tls.Server(raw, s.tlsConfig)
	if err := conn.SetDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return nil, err
	}
	if err := conn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("TLS handshake: %w", err)
	}
	if err := readRequest(conn, s.password); err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	return conn, nil
}

func readRequest(r io.Reader, password [56]byte) error {
	var header [60]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(header[:56], password[:]) != 1 ||
		header[56] != '\r' || header[57] != '\n' || header[58] != 1 {
		return errors.New("invalid Trojan authentication or TCP command")
	}
	var addressLength int
	switch header[59] {
	case 1:
		addressLength = 4
	case 4:
		addressLength = 16
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(r, length[:]); err != nil {
			return err
		}
		addressLength = int(length[0])
		if addressLength == 0 {
			return errors.New("empty Trojan destination")
		}
	default:
		return errors.New("invalid Trojan address type")
	}
	// Address + port + CRLF. Read exactly the header, preserving game payload.
	var rest [259]byte
	end := addressLength + 4
	if _, err := io.ReadFull(r, rest[:end]); err != nil {
		return err
	}
	if rest[end-2] != '\r' || rest[end-1] != '\n' {
		return errors.New("invalid Trojan request terminator")
	}
	return nil
}

func writeRequest(w io.Writer, password string) error {
	hash := passwordHash(password)
	var header [68]byte
	copy(header[:], hash[:])
	// CONNECT 127.0.0.1:25565; the server ignores this destination.
	copy(header[56:], []byte{'\r', '\n', 1, 1, 127, 0, 0, 1, 0x63, 0xdd, '\r', '\n'})
	n, err := w.Write(header[:])
	if err == nil && n != len(header) {
		err = io.ErrShortWrite
	}
	return err
}
