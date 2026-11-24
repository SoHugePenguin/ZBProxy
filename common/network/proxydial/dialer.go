// Package proxydial constructs the same proxy dialer for game traffic and probes.
package proxydial

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/layou233/zbproxy/v3/common/network"
	"github.com/layou233/zbproxy/v3/common/network/socks"
)

func New(base network.Dialer, kind, proxyNetwork, address, username, password string) (network.Dialer, error) {
	if proxyNetwork == "" {
		proxyNetwork = "tcp"
	}
	switch kind {
	case "", "direct":
		return base, nil
	case "socks", "socks5", "socks4", "socks4a", "http":
	default:
		return nil, fmt.Errorf("unsupported proxy type %q", kind)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || strings.ContainsAny(host, "\r\n /@") {
		return nil, errors.New("invalid proxy host:port")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return nil, errors.New("invalid proxy port")
	}
	if proxyNetwork != "tcp" && proxyNetwork != "tcp4" && proxyNetwork != "tcp6" {
		return nil, errors.New("proxy network must be tcp, tcp4 or tcp6")
	}
	if kind == "http" {
		if strings.ContainsAny(username, ":\r\n") {
			return nil, errors.New("invalid HTTP proxy username")
		}
		return &httpDialer{base: base, network: proxyNetwork, address: address, username: username, password: password}, nil
	}
	if kind == "socks" || kind == "socks5" {
		if len(username) > 255 || len(password) > 255 || (username == "") != (password == "") {
			return nil, errors.New("SOCKS5 requires both username and password (maximum 255 bytes each)")
		}
	} else if password != "" {
		return nil, errors.New("SOCKS4 does not support password authentication")
	}
	return &socks.Client{Dialer: base, Version: kind, Network: proxyNetwork, Address: address, Username: username, Password: password}, nil
}

type httpDialer struct {
	base                                 network.Dialer
	network, address, username, password string
}
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (d *httpDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := d.base.DialContext(ctx, d.network, d.address)
	if err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: address}, Host: address, Header: make(http.Header)}
	if d.username != "" || d.password != "" {
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(d.username+":"+d.password)))
	}
	reader := bufio.NewReader(io.LimitReader(conn, 32768))
	var response *http.Response
	if err = req.Write(conn); err == nil {
		response, err = http.ReadResponse(reader, req)
	}
	close(done)
	<-stopped
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && response.StatusCode != http.StatusOK {
		err = fmt.Errorf("HTTP proxy CONNECT failed (status %d)", response.StatusCode)
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	// Keep target bytes buffered alongside the headers, without retaining the
	// header size limit on the established game tunnel.
	prefix := make([]byte, reader.Buffered())
	io.ReadFull(reader, prefix)
	conn.SetDeadline(time.Time{})
	return &bufferedConn{Conn: conn, reader: bufio.NewReader(io.MultiReader(strings.NewReader(string(prefix)), conn))}, nil
}
