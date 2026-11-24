package zbproxy_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/layou233/zbproxy/v3"
	"github.com/layou233/zbproxy/v3/admin"
	"github.com/layou233/zbproxy/v3/common/mcprotocol"
	"github.com/layou233/zbproxy/v3/config"
	"github.com/phuslu/log"
)

func packet(payload []byte) []byte {
	var b bytes.Buffer
	mcprotocol.VarInt(len(payload)).WriteTo(&b)
	b.Write(payload)
	return b.Bytes()
}
func readPacket(r io.Reader) ([]byte, error) {
	size, _, err := mcprotocol.ReadVarIntFrom(r)
	if err != nil {
		return nil, err
	}
	if size < 0 || size > 2048 {
		return nil, errors.New("bad size")
	}
	b := make([]byte, int(size))
	_, err = io.ReadFull(r, b)
	return b, err
}
func target(t *testing.T, marker byte) (uint16, <-chan uint16) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	ports := make(chan uint16, 10)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				data, err := readPacket(c)
				if err != nil {
					return
				}
				b := bytes.NewReader(data)
				b.ReadByte()
				mcprotocol.ReadVarIntFrom(b)
				size, _, _ := mcprotocol.ReadVarIntFrom(b)
				io.CopyN(io.Discard, b, int64(size))
				var port uint16
				binary.Read(b, binary.BigEndian, &port)
				ports <- port
				if _, err = readPacket(c); err != nil {
					return
				}
				c.Write([]byte{marker})
				io.Copy(c, c)
			}()
		}
	}()
	return uint16(l.Addr().(*net.TCPAddr).Port), ports
}
func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	return port
}
func login(t *testing.T, port uint16, name string, uuid []byte) net.Conn {
	t.Helper()
	var h bytes.Buffer
	h.WriteByte(0)
	version := 47
	if uuid != nil {
		version = 764
	}
	mcprotocol.VarInt(version).WriteTo(&h)
	host := "local.invalid"
	mcprotocol.VarInt(len(host)).WriteTo(&h)
	h.WriteString(host)
	binary.Write(&h, binary.BigEndian, uint16(25565))
	h.WriteByte(2)
	var p bytes.Buffer
	p.WriteByte(0)
	mcprotocol.VarInt(len(name)).WriteTo(&p)
	p.WriteString(name)
	p.Write(uuid)
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", fmtPort(port)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write(append(packet(h.Bytes()), packet(p.Bytes())...))
	return c
}
func fmtPort(port uint16) string { return strconv.Itoa(int(port)) }
func expectByte(t *testing.T, c net.Conn, value byte) {
	t.Helper()
	var b [1]byte
	if _, err := io.ReadFull(c, b[:]); err != nil || b[0] != value {
		t.Fatalf("got %q, want %q, error %v", b[0], value, err)
	}
}

func TestManagedRoutesHotUpdateAndRollback(t *testing.T) {
	a, portsA := target(t, 'A')
	b, portsB := target(t, 'B')
	inPort := freePort(t)
	root := &config.Root{Log: config.Log{Level: log.PanicLevel}, Services: []*config.Service{{Name: "game", Bind: "127.0.0.1", Listen: inPort, DefaultOutbound: "a"}}, Router: config.Router{DefaultOutbound: "RESET", PlayerRoutes: []config.PlayerRoute{{Enabled: true, Kind: "name", Value: "Alex", Service: "game", Outbound: "b"}}}, Outbounds: []*config.Outbound{{Name: "a", TargetAddress: "127.0.0.1", TargetPort: a, Minecraft: &config.MinecraftService{}}, {Name: "b", TargetAddress: "127.0.0.1", TargetPort: b, Minecraft: &config.MinecraftService{}}}}
	instance, err := zbproxy.NewInstance(context.Background(), zbproxy.Options{Config: root})
	if err != nil {
		t.Fatal(err)
	}
	if err = instance.Start(); err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	steve := login(t, inPort, "Steve", nil)
	expectByte(t, steve, 'A')
	if p := <-portsA; p != a {
		t.Fatalf("wrong rewritten target port %d", p)
	}
	alex := login(t, inPort, "aLeX", nil)
	expectByte(t, alex, 'B')
	if p := <-portsB; p != b {
		t.Fatalf("wrong rewritten target port %d", p)
	}
	data, _ := json.Marshal(root)
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, data, 0600)
	manager, err := admin.OpenNetworkManager(path, root, instance)
	if err != nil {
		t.Fatal(err)
	}
	state := manager.Snapshot()
	state.PlayerRoutes[0].Outbound = "a"
	if err = manager.Save(state); err != nil {
		t.Fatal(err)
	}
	updated := login(t, inPort, "Alex", nil)
	expectByte(t, updated, 'A')
	<-portsA
	alex.Write([]byte{'z'})
	expectByte(t, alex, 'z')
	state = manager.Snapshot()
	state.Services[0].DefaultOutbound = "b"
	if err = manager.Save(state); err != nil {
		t.Fatal(err)
	}
	newDefault := login(t, inPort, "Steve", nil)
	expectByte(t, newDefault, 'B')
	<-portsB
	state = manager.Snapshot()
	state.PlayerRoutes = append(state.PlayerRoutes, config.PlayerRoute{Enabled: true, Kind: "uuid", Service: "game", Value: "01020304-0506-0708-090a-0b0c0d0e0f10", Outbound: "a"})
	if err = manager.Save(state); err != nil {
		t.Fatal(err)
	}
	byUUID := login(t, inPort, "Steve", []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	expectByte(t, byUUID, 'A')
	<-portsA
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	before, _ := os.ReadFile(path)
	state = manager.Snapshot()
	state.Services[0].Listen = uint16(busy.Addr().(*net.TCPAddr).Port)
	if err = manager.Save(state); err == nil {
		t.Fatal("occupied port was accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed update changed persisted config")
	}
	stillWorks := login(t, inPort, "Steve", nil)
	expectByte(t, stillWorks, 'B')
	<-portsB
}
