package route

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/layou233/zbproxy/v3/adapter"
	"github.com/layou233/zbproxy/v3/common/bufio"
	"github.com/layou233/zbproxy/v3/common/jsonx"
	"github.com/layou233/zbproxy/v3/config"
	"github.com/layou233/zbproxy/v3/protocol"
	"github.com/phuslu/log"
)

func TestSlowSniffDoesNotBlockReload(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	logger := &log.Logger{Level: log.PanicLevel}
	r := &Router{}
	options := RouterOptions{
		Config: &config.Router{DefaultOutbound: "REJECT", Rules: []*config.Rule{{
			Type: config.RuleTypeAlways, Sniff: jsonx.Listable[string]{"slow"},
		}}},
		SnifferRegistry: map[string]protocol.SnifferFunc{
			"slow": func(*log.Logger, bufio.PeekConn, *adapter.Metadata) error {
				close(entered)
				<-release
				return nil
			},
		},
	}
	if err := r.Initialize(context.Background(), logger, options); err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	done := make(chan struct{})
	go func() {
		r.HandleConnection(server, &adapter.Metadata{})
		close(done)
	}()
	defer func() { close(release); <-done }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("sniffer did not start")
	}
	updated := make(chan error, 1)
	go func() { updated <- r.UpdateConfig(options) }()
	select {
	case err := <-updated:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow handshake blocked configuration update")
	}
}

// Deliberately implement only the plain outbound interface.
type dialFailure struct{}

func (dialFailure) Name() string                                                       { return "failure" }
func (dialFailure) PostInitialize(adapter.Router, adapter.RouteResourceProvider) error { return nil }
func (dialFailure) Reload(adapter.OutboundReloadOptions) error                         { return nil }
func (dialFailure) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("dial failed")
}

func TestDialFailureClosesClient(t *testing.T) {
	r := &Router{}
	if err := r.Initialize(context.Background(), &log.Logger{Level: log.PanicLevel}, RouterOptions{
		Config:      &config.Router{DefaultOutbound: "failure"},
		OutboundMap: map[string]adapter.Outbound{"failure": dialFailure{}},
	}); err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	r.HandleConnection(server, &adapter.Metadata{DestinationHostname: "localhost", DestinationPort: 1})
	client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("client was not closed after dial failure")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("client connection leaked")
	}
}
