package minecraft_test

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/layou233/zbproxy/v3/adapter"
	"github.com/layou233/zbproxy/v3/admin"
	"github.com/layou233/zbproxy/v3/common/bufio"
	"github.com/layou233/zbproxy/v3/common/mcprotocol"
	"github.com/layou233/zbproxy/v3/config"
	"github.com/layou233/zbproxy/v3/protocol/minecraft"
	"github.com/layou233/zbproxy/v3/route"
	"github.com/phuslu/log"
)

type countedDialer struct{ calls atomic.Int32 }

func (*countedDialer) Name() string                                                       { return "counted" }
func (*countedDialer) PostInitialize(adapter.Router, adapter.RouteResourceProvider) error { return nil }
func (*countedDialer) Reload(adapter.OutboundReloadOptions) error                         { return nil }
func (d *countedDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.calls.Add(1)
	return nil, errors.New("test dial reached")
}

func TestManagedWhitelistRejectsBeforeDial(t *testing.T) {
	s, err := admin.OpenStore(filepath.Join(t.TempDir(), "players.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdatePlayers([]string{"Steve", "Visitor"}, nil); err != nil {
		t.Fatal(err)
	}
	on := true
	if _, err = s.Update(&on, []string{"Steve"}, nil); err != nil {
		t.Fatal(err)
	}
	dialer := &countedDialer{}
	router := &route.Router{}
	logger := &log.Logger{Level: log.PanicLevel}
	if err = router.Initialize(context.Background(), logger, route.RouterOptions{
		Config:      &config.Router{DefaultOutbound: "RESET"},
		OutboundMap: map[string]adapter.Outbound{"counted": dialer}, PlayerAccess: s.AllowPlayer,
	}); err != nil {
		t.Fatal(err)
	}
	out, err := minecraft.NewOutbound(logger, &config.Outbound{Name: "mc", Dialer: "counted", TargetAddress: "localhost", TargetPort: 25565, Minecraft: &config.MinecraftService{IgnoreSRVRedirect: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err = out.PostInitialize(router, router); err != nil {
		t.Fatal(err)
	}
	check := func(name string) string {
		t.Helper()
		client, server := net.Pipe()
		defer client.Close()
		client.SetDeadline(time.Now().Add(3 * time.Second))
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer server.Close()
			out.InjectConnection(context.Background(), bufio.NewCachedConn(server), &adapter.Metadata{Minecraft: &adapter.MinecraftMetadata{NextState: mcprotocol.IntentLogin, PlayerName: name, SniffPosition: -1}})
		}()
		data, err := io.ReadAll(client)
		if err != nil {
			t.Fatal(err)
		}
		<-done
		return string(data)
	}
	if response := check("Visitor"); !strings.Contains(response, "not on the proxy whitelist") {
		t.Fatalf("missing rejection packet: %q", response)
	}
	if dialer.calls.Load() != 0 {
		t.Fatal("unauthorized client reached upstream")
	}
	check("STEVE")
	if dialer.calls.Load() != 1 {
		t.Fatal("authorized player did not reach dialer")
	}
	if _, err = s.Update(nil, []string{"Visitor"}, nil); err != nil {
		t.Fatal(err)
	}
	check("Visitor")
	if dialer.calls.Load() != 2 {
		t.Fatal("live whitelist update was not applied")
	}
}
