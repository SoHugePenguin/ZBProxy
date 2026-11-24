package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/layou233/zbproxy/v3/common/bufio"
	"github.com/layou233/zbproxy/v3/config"
	"github.com/phuslu/log"
)

func clientTLS(settings config.EncryptionClient) (*tls.Config, error) {
	if !settings.Enabled || settings.Password == "" || settings.ServerAddress == "" || settings.ServerName == "" || settings.ServerPort == 0 {
		return nil, errors.New("EncryptionClient requires Enabled, Password, ServerAddress, ServerName and ServerPort")
	}
	tlsConfig := &tls.Config{
		ServerName: settings.ServerName, MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"}, ClientSessionCache: tls.NewLRUClientSessionCache(32),
	}
	if settings.CACertFile != "" {
		pem, err := os.ReadFile(settings.CACertFile)
		if err != nil {
			return nil, err
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("CACertFile contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	return tlsConfig, nil
}

// RunClient exposes a game port on the configured IP, defaulting to loopback.
// One game connection uses one independent TLS stream.
func RunClient(ctx context.Context, settings config.EncryptionClient, logger *log.Logger) error {
	return RunClients(ctx, []config.EncryptionClient{settings}, logger)
}

type encryptedClient struct {
	settings  config.EncryptionClient
	tlsConfig *tls.Config
	listener  net.Listener
}

// RunClients starts all configured listeners and stops them together on failure
// or cancellation. Startup failure releases every listener already opened.
func RunClients(ctx context.Context, settings []config.EncryptionClient, logger *log.Logger) error {
	if len(settings) == 0 {
		return errors.New("EncryptionClient requires at least one client")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	clients := make([]*encryptedClient, 0, len(settings))
	defer func() {
		for _, client := range clients {
			client.listener.Close()
		}
	}()
	for i, entry := range settings {
		client, err := newEncryptedClient(entry)
		if err != nil {
			return fmt.Errorf("EncryptionClient[%d]: %w", i, err)
		}
		clients = append(clients, client)
	}
	results := make(chan error, len(clients))
	for i, client := range clients {
		go func(index int, client *encryptedClient) {
			err := client.run(ctx, logger)
			if err != nil {
				err = fmt.Errorf("EncryptionClient[%d]: %w", index, err)
			}
			results <- err
		}(i, client)
	}
	var firstErr error
	for range clients {
		if err := <-results; err != nil && firstErr == nil {
			firstErr = err
		}
		cancel()
	}
	return firstErr
}

func newEncryptedClient(settings config.EncryptionClient) (*encryptedClient, error) {
	tlsConfig, err := clientTLS(settings)
	if err != nil {
		return nil, err
	}
	if settings.Bind == "" {
		settings.Bind = "127.0.0.1"
	}
	if ip := net.ParseIP(settings.Bind); ip == nil {
		return nil, errors.New("EncryptionClient.Bind must be an IP address")
	}
	if settings.Listen == 0 {
		settings.Listen = 25565
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(settings.Bind, strconv.Itoa(int(settings.Listen))))
	if err != nil {
		return nil, err
	}
	return &encryptedClient{settings: settings, tlsConfig: tlsConfig, listener: listener}, nil
}

func (client *encryptedClient) run(ctx context.Context, logger *log.Logger) error {
	settings, tlsConfig, listener := client.settings, client.tlsConfig, client.listener
	defer listener.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	connections := make(map[net.Conn]struct{})
	go func() {
		<-ctx.Done()
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for conn := range connections {
			conn.Close()
		}
	}()
	dialer := &tls.Dialer{Config: tlsConfig, NetDialer: &net.Dialer{Timeout: handshakeTimeout, KeepAlive: 30 * time.Second}}
	address := net.JoinHostPort(settings.ServerAddress, strconv.Itoa(int(settings.ServerPort)))
	logger.Info().Str("listen", listener.Addr().String()).Str("server", address).Msg("Encrypted Minecraft client started")
	for {
		local, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		mu.Lock()
		if ctx.Err() != nil {
			mu.Unlock()
			local.Close()
			return nil
		}
		connections[local] = struct{}{}
		mu.Unlock()
		go func() {
			defer local.Close()
			defer func() { mu.Lock(); delete(connections, local); mu.Unlock() }()
			remote, err := dialer.DialContext(ctx, "tcp", address)
			if err != nil {
				logger.Warn().Err(err).Msg("Encrypted server connection failed")
				return
			}
			defer remote.Close()
			remote.SetWriteDeadline(time.Now().Add(handshakeTimeout))
			if err = writeRequest(remote, settings.Password); err != nil {
				logger.Warn().Err(fmt.Errorf("Trojan request: %w", err)).Msg("Encrypted server authentication failed")
				return
			}
			remote.SetWriteDeadline(time.Time{})
			if err := bufio.CopyConn(remote, local); err != nil && ctx.Err() == nil {
				logger.Debug().Err(err).Msg("Encrypted Minecraft connection closed")
			}
		}()
	}
}
