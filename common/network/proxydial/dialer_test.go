package proxydial

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/layou233/zbproxy/v3/common/network"
)

func proxyServer(t *testing.T, handler func(net.Conn)) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		handler(conn)
	}()
	return l.Addr().String()
}

func TestSOCKS5AuthenticationAndFragmentedReply(t *testing.T) {
	address := proxyServer(t, func(c net.Conn) {
		var header [3]byte
		if _, err := io.ReadFull(c, header[:]); err != nil || header != [3]byte{5, 1, 2} {
			return
		}
		c.Write([]byte{5})
		c.Write([]byte{2})
		auth := make([]byte, 11)
		io.ReadFull(c, auth)
		if !bytes.Equal(auth, []byte{1, 4, 'u', 's', 'e', 'r', 4, 'p', 'a', 's', 's'}) {
			c.Write([]byte{1, 1})
			return
		}
		c.Write([]byte{1, 0})
		request := make([]byte, 4+1+len("mc.example")+2)
		io.ReadFull(c, request)
		if request[3] != 3 || !bytes.Equal(request[5:len(request)-2], []byte("mc.example")) {
			return
		}
		for _, b := range []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0} {
			c.Write([]byte{b})
		}
		c.Write([]byte("ok"))
	})
	d, err := New(network.SystemDialer, "socks5", "tcp", address, "user", "pass")
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.DialContext(context.Background(), "tcp", "mc.example:25565")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var result [2]byte
	if _, err = io.ReadFull(c, result[:]); err != nil || string(result[:]) != "ok" {
		t.Fatalf("tunnel payload: %q %v", result, err)
	}
}

func TestHTTPConnectPreservesBufferedPayload(t *testing.T) {
	payload := bytes.Repeat([]byte("p"), 40000)
	address := proxyServer(t, func(c net.Conn) {
		req, err := http.ReadRequest(bufio.NewReader(c))
		if err != nil || req.Method != "CONNECT" || req.Host != "mc.example:25565" || req.Header.Get("Proxy-Authorization") != "Basic dXNlcjpwYXNz" {
			return
		}
		c.Write(append([]byte("HTTP/1.1 200 Connection established\r\n\r\n"), payload...))
	})
	d, err := New(network.SystemDialer, "http", "tcp", address, "user", "pass")
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.DialContext(context.Background(), "tcp", "mc.example:25565")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got, err := io.ReadAll(c)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("payload length %d, error %v", len(got), err)
	}
}

func TestProxyAuthenticationFailsClosedAndCancellation(t *testing.T) {
	for _, kind := range []string{"socks5", "http"} {
		t.Run(kind, func(t *testing.T) {
			address := proxyServer(t, func(c net.Conn) {
				if kind == "socks5" {
					greeting := make([]byte, 3)
					io.ReadFull(c, greeting)
					c.Write([]byte{5, 255})
				} else {
					http.ReadRequest(bufio.NewReader(c))
					c.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n"))
				}
			})
			d, _ := New(network.SystemDialer, kind, "tcp", address, "user", "bad")
			if c, err := d.DialContext(context.Background(), "tcp", "mc.example:25565"); err == nil {
				c.Close()
				t.Fatal("failed authentication returned a tunnel")
			}
		})
	}
	address := proxyServer(t, func(c net.Conn) { io.Copy(io.Discard, c) })
	d, _ := New(network.SystemDialer, "socks5", "tcp", address, "user", "pass")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	start := time.Now()
	if c, err := d.DialContext(ctx, "tcp", "mc.example:25565"); err == nil {
		c.Close()
		t.Fatal("cancelled handshake succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation did not interrupt the handshake")
	}
}
