package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/layou233/zbproxy/v3/common/mcprotocol"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/layou233/zbproxy/v3/config"
)

type fakeRuntime struct {
	fail  bool
	calls int
}

func (f *fakeRuntime) ApplyConfig(_ *config.Root, commit func() error) error {
	f.calls++
	if f.fail {
		return errors.New("bind failed")
	}
	if commit != nil {
		return commit()
	}
	return nil
}
func networkFixture(t *testing.T) (*NetworkManager, *fakeRuntime, string) {
	t.Helper()
	root := &config.Root{Services: []*config.Service{{Name: "game", Bind: "127.0.0.1", Listen: 25565, DefaultOutbound: "us"}}, Router: config.Router{DefaultOutbound: "RESET"}, Outbounds: []*config.Outbound{{Name: "us", TargetAddress: "mc.example", TargetPort: 25565, Minecraft: &config.MinecraftService{}, ProxyOptions: config.ProxyOptions{Type: "socks5", Address: "127.0.0.1:1080", Username: "user", Password: "secret"}}}}
	data, _ := json.Marshal(root)
	var raw map[string]json.RawMessage
	json.Unmarshal(data, &raw)
	raw["EncryptionClient"] = json.RawMessage(`[{"ServerAddress":"keep.example","Password":"keep"}]`)
	data, _ = json.Marshal(raw)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	m, err := OpenNetworkManager(path, root, runtime)
	if err != nil {
		t.Fatal(err)
	}
	return m, runtime, path
}

func TestNetworkSavePreservesSecretsUnknownFieldsAndRevision(t *testing.T) {
	m, _, path := networkFixture(t)
	state := m.Snapshot()
	if state.Outbounds[0].ProxyOptions.Password != "" || !state.ProxyPasswords["us"] {
		t.Fatal("secret was exposed or missing password indicator")
	}
	state.Outbounds[0].TargetPort = 25566
	if err := m.Save(state); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var raw map[string]json.RawMessage
	json.Unmarshal(data, &raw)
	if !bytes.Contains(raw["Outbounds"], []byte(`"Password": "secret"`)) || !bytes.Contains(raw["EncryptionClient"], []byte("keep.example")) {
		t.Fatal("password or unknown top-level configuration was lost")
	}
	if err := m.Save(state); err == nil {
		t.Fatal("stale revision accepted")
	}
	fresh := m.Snapshot()
	fresh.Outbounds[0].ProxyOptions.Type = "direct"
	if err := m.Save(fresh); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().ProxyPasswords["us"] {
		t.Fatal("switching to direct kept the password")
	}
}

func TestNetworkRejectsDanglingRoutesAndFailedApply(t *testing.T) {
	m, runtime, path := networkFixture(t)
	before, _ := os.ReadFile(path)
	state := m.Snapshot()
	state.Outbounds = nil
	if err := m.Save(state); err == nil {
		t.Fatal("dangling default outbound accepted")
	}
	state = m.Snapshot()
	state.PlayerRoutes = []config.PlayerRoute{{Enabled: true, Service: "missing", Kind: "name", Value: "Steve", Outbound: "us"}}
	if err := m.Save(state); err == nil {
		t.Fatal("missing service accepted")
	}
	state = m.Snapshot()
	runtime.fail = true
	state.Services[0].Listen = 25566
	if err := m.Save(state); err == nil {
		t.Fatal("runtime error hidden")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || m.Snapshot().Revision != 0 {
		t.Fatal("failed update changed active state")
	}
}

func TestNetworkSaveWithLegacyRouting(t *testing.T) {
	for _, mode := range []string{"global", "service-list", "conditional"} {
		t.Run(mode, func(t *testing.T) {
			m, _, path := networkFixture(t)
			root := cloneRoot(m.current)
			root.Services[0].DefaultOutbound = ""
			switch mode {
			case "global":
				root.Router.DefaultOutbound = "us"
			case "service-list":
				root.Router.Rules = []*config.Rule{{Type: config.RuleTypeServiceName, Parameter: json.RawMessage(`["game"]`), Outbound: "us"}}
			case "conditional":
				root.Router.Rules = []*config.Rule{{Type: config.RuleTypeServiceName, Parameter: json.RawMessage(`"other"`), Invert: true, Outbound: "us"}}
			}
			data, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			m, err = OpenNetworkManager(path, root, &fakeRuntime{})
			if err != nil {
				t.Fatal(err)
			}
			state := m.Snapshot()
			if state.Services[0].DefaultOutbound != "" {
				t.Fatal("legacy routing unexpectedly replaced")
			}
			state.Outbounds = append(state.Outbounds, &config.Outbound{Name: "new", TargetAddress: "mc.example", TargetPort: 25565, Minecraft: &config.MinecraftService{}})
			if err := m.Save(state); err != nil {
				t.Fatalf("adding an outbound with legacy routing: %v", err)
			}
			data, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Services []*config.Service
				Router   config.Router
			}
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(root.Router)
			got, _ := json.Marshal(saved.Router)
			if !bytes.Equal(got, want) || saved.Services[0].DefaultOutbound != "" {
				t.Fatalf("legacy routing changed: %s -> %s", want, got)
			}
			state = m.Snapshot()
			state.Outbounds = state.Outbounds[1:]
			if err := m.Save(state); err == nil {
				t.Fatal("removed an outbound still referenced by legacy routing")
			}
		})
	}
}

func TestNetworkAPIRequiresAuthenticationAndMutationHeader(t *testing.T) {
	m, _, _ := networkFixture(t)
	store, _ := OpenStore(filepath.Join(t.TempDir(), "whitelist.json"))
	server, err := NewManagedServer("127.0.0.1:0", "admin", "password", store, m)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "http://127.0.0.1/api/network", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("network API bypassed auth")
	}
	request = httptest.NewRequest("PUT", "http://127.0.0.1/api/network", bytes.NewBufferString(`{}`))
	request.SetBasicAuth("admin", "password")
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("network API bypassed mutation header")
	}
}

func TestMinecraftProbeChecksStatusAndEcho(t *testing.T) {
	m, _, _ := networkFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for i := 0; i < 3; i++ {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer c.Close()
				if _, err = readPacket(c); err != nil {
					return
				}
				if _, err = readPacket(c); err != nil {
					return
				}
				body := []byte(`{"version":{"name":"test","protocol":47},"players":{"max":20,"online":0}}`)
				var response bytes.Buffer
				response.WriteByte(0)
				writeVarString(&response, body)
				writePacket(c, response.Bytes())
				p, err := readPacket(c)
				if err == nil {
					writePacket(c, p)
				}
			}()
		}
	}()
	m.current.Outbounds[0].ProxyOptions = config.ProxyOptions{Type: "direct"}
	m.current.Outbounds[0].TargetAddress = "127.0.0.1"
	m.current.Outbounds[0].TargetPort = uint16(listener.Addr().(*net.TCPAddr).Port)
	result, err := m.Probe(context.Background(), "us", "minecraft")
	if err != nil || result.Successful != 3 || result.AverageMS == nil {
		t.Fatalf("probe: %+v %v", result, err)
	}
	for _, sample := range result.Samples {
		if sample.ConnectMS == nil || sample.PingMS == nil || sample.Error != "" {
			t.Fatalf("sample: %+v", sample)
		}
	}
}

func writeVarString(w *bytes.Buffer, value []byte) {
	mcprotocol.VarInt(len(value)).WriteTo(w)
	w.Write(value)
}

func TestNetworkWatchSurvivesAtomicReplacements(t *testing.T) {
	m, _, path := networkFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errors := make(chan error, 4)
	if err := m.Watch(ctx, func(err error) { errors <- err }); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]json.RawMessage
		json.Unmarshal(data, &raw)
		services, _ := json.Marshal([]*config.Service{{Name: "game", Bind: "127.0.0.1", Listen: uint16(25565 + i), DefaultOutbound: "us"}})
		raw["Services"], raw["Log"] = services, json.RawMessage(`{"Level":"warn"}`)
		data, _ = json.Marshal(raw)
		if err = saveAtomic(path, data); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for m.Snapshot().Revision < uint64(i) && time.Now().Before(deadline) {
			select {
			case err := <-errors:
				t.Fatal(err)
			default:
			}
			time.Sleep(10 * time.Millisecond)
		}
		if got := m.Snapshot(); got.Revision != uint64(i) || got.Services[0].Listen != uint16(25565+i) {
			t.Fatalf("replacement %d not applied: %+v", i, got)
		}
	}
	state := m.Snapshot()
	if err := m.Save(state); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if m.Snapshot().Revision != state.Revision+1 {
		t.Fatal("watcher reapplied an update already saved through the website")
	}
}
