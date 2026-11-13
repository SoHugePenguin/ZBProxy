package bufio

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/layou233/zbproxy/v3/common/buf"
)

type readerConn struct {
	net.Conn
	reader io.Reader
}

func (c readerConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

type boundedWriter struct {
	bytes.Buffer
	remaining int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, errors.New("duplicate cache data")
	}
	w.remaining -= len(p)
	return w.Buffer.Write(p)
}

func TestCopyConsumesCache(t *testing.T) {
	c := NewCachedConn(readerConn{reader: bytes.NewBufferString("tail")})
	c.cache = buf.NewSize(64)
	c.cache.WriteString("head")
	defer c.Release()
	w := &boundedWriter{remaining: 8}
	n, err := Copy(w, c)
	if err != nil || n != 8 || w.String() != "headtail" {
		t.Fatalf("copy = %d, %v, %q; want 8, nil, headtail", n, err, w.String())
	}
	if c.cache != nil {
		t.Fatal("consumed cache was not released")
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestCopyCacheShortWrite(t *testing.T) {
	c := NewCachedConn(readerConn{reader: bytes.NewReader(nil)})
	c.cache = buf.NewSize(64)
	c.cache.WriteString("head")
	defer c.Release()
	n, err := Copy(shortWriter{}, c)
	if n != 2 || !errors.Is(err, io.ErrShortWrite) || string(c.cache.Bytes()) != "ad" {
		t.Fatalf("copy = %d, %v; remaining %q", n, err, c.cache.Bytes())
	}
}

func tcpPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	a, err := net.DialTCP("tcp4", nil, l.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := l.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	a.SetDeadline(time.Now().Add(5 * time.Second))
	b.SetDeadline(time.Now().Add(5 * time.Second))
	return a, b
}

func TestCopyCacheThenTCP(t *testing.T) {
	sender, source := tcpPair(t)
	destination, receiver := tcpPair(t)
	payload := bytes.Repeat([]byte("tcp-data"), 8192)
	c := NewCachedConn(source)
	c.cache = buf.NewSize(64)
	c.cache.WriteString("head")
	defer c.Release()
	writeDone := make(chan error, 1)
	go func() {
		_, err := sender.Write(payload)
		sender.CloseWrite()
		writeDone <- err
	}()
	type result struct {
		n   int64
		err error
	}
	copyDone := make(chan result, 1)
	go func() {
		n, err := Copy(destination, c)
		destination.CloseWrite()
		copyDone <- result{n, err}
	}()
	got, err := io.ReadAll(receiver)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte("head"), payload...)
	if !bytes.Equal(got, want) {
		t.Fatalf("received %d bytes; want %d in order", len(got), len(want))
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	r := <-copyDone
	if r.err != nil || r.n != int64(len(want)) {
		t.Fatalf("copy = %d, %v", r.n, r.err)
	}
	if c.cache != nil {
		t.Fatal("cache retained during TCP relay")
	}
}
