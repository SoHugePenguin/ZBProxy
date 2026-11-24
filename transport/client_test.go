package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/layou233/zbproxy/v3/config"
	"github.com/phuslu/log"
)

func freeClientPort(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	listener.Close()
	return port
}

func TestRunClientsIndependentUpstreams(t *testing.T) {
	cert, key := testCertificate(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var settings []config.EncryptionClient
	var addresses []string
	for i := 0; i < 2; i++ {
		password := fmt.Sprintf("password-%d", i)
		server, err := NewServer(&config.Encryption{Enabled: true, CertFile: cert, KeyFile: key, Password: password})
		if err != nil {
			t.Fatal(err)
		}
		upstream, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer upstream.Close()
		response := fmt.Sprintf("server-%d", i)
		go func() {
			for {
				raw, err := upstream.Accept()
				if err != nil {
					return
				}
				go func() {
					defer raw.Close()
					raw.SetDeadline(time.Now().Add(5 * time.Second))
					conn, err := server.Accept(ctx, raw)
					if err == nil {
						var request [4]byte
						if _, err := io.ReadFull(conn, request[:]); err == nil && string(request[:]) == "game" {
							io.WriteString(conn, response)
						}
					}
				}()
			}
		}()
		port := freeClientPort(t)
		for len(settings) > 0 && port == settings[0].Listen {
			port = freeClientPort(t)
		}
		settings = append(settings, config.EncryptionClient{Enabled: true, Listen: port, ServerAddress: "127.0.0.1", ServerPort: uint16(upstream.Addr().(*net.TCPAddr).Port), ServerName: "proxy.test", Password: password, CACertFile: cert})
		addresses = append(addresses, fmt.Sprintf("127.0.0.1:%d", port))
	}
	done := make(chan error, 1)
	go func() { done <- RunClients(ctx, settings, &log.Logger{Level: log.ErrorLevel}) }()
	for i, address := range addresses {
		deadline := time.Now().Add(5 * time.Second)
		var conn net.Conn
		var err error
		for time.Now().Before(deadline) {
			conn, err = net.DialTimeout("tcp", address, 100*time.Millisecond)
			if err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(deadline)
		_, err = io.WriteString(conn, "game")
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		response, err := io.ReadAll(conn)
		conn.Close()
		if err != nil || string(response) != fmt.Sprintf("server-%d", i) {
			t.Fatalf("client %d: response=%q error=%v", i, response, err)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("clients did not stop")
	}
	for _, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("listener not released: %v", err)
		}
		listener.Close()
	}
}

func TestRunClientsStartupRollback(t *testing.T) {
	settings := config.EncryptionClient{Enabled: true, Listen: freeClientPort(t), ServerAddress: "127.0.0.1", ServerPort: 443, ServerName: "proxy.test", Password: "password"}
	err := RunClients(context.Background(), []config.EncryptionClient{settings, settings}, &log.Logger{Level: log.ErrorLevel})
	if err == nil || !strings.Contains(err.Error(), "EncryptionClient[1]") {
		t.Fatalf("expected second listener failure, got %v", err)
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", settings.Listen))
	if err != nil {
		t.Fatalf("startup leaked first listener: %v", err)
	}
	listener.Close()
	if err := RunClients(context.Background(), nil, &log.Logger{Level: log.ErrorLevel}); err == nil {
		t.Fatal("empty configuration accepted")
	}
}
