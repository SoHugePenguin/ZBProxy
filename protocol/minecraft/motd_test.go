package minecraft

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/layou233/zbproxy/v3/config"
)

func testMOTDOutbound() *Outbound {
	c := &config.Outbound{Minecraft: &config.MinecraftService{
		MotdFavicon: defaultMOTD, MotdDescription: "Test <server> §a在线",
	}}
	c.Minecraft.OnlineCount.Online = -1
	c.Minecraft.OnlineCount.Max = 100
	return &Outbound{config: c}
}

func TestMOTDCache(t *testing.T) {
	o := testMOTDOutbound()
	for _, step := range []struct {
		protocol uint
		online   int32
	}{{764, 3}, {764, 3}, {765, 3}, {765, 4}} {
		o.onlineCount.Store(step.online)
		got := o.motdForProtocol(step.protocol)
		want := generateMOTD(step.protocol, o.config, &o.onlineCount)
		if !bytes.Equal(got, want) {
			t.Fatalf("cached MOTD differs for protocol %d, online %d", step.protocol, step.online)
		}
	}
	previous := o.motdForProtocol(765)
	if next := o.motdForProtocol(765); &previous[0] != &next[0] {
		t.Fatal("identical query did not reuse serialized response")
	}
	o.config.Minecraft.MotdDescription = "reloaded"
	if err := o.PostInitialize(nil, nil); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(previous, o.motdForProtocol(765)) {
		t.Fatal("configuration update did not invalidate cached MOTD")
	}
	o.config.Minecraft.OnlineCount.Online = 42
	o.onlineCount.Store(99)
	o.motdCache.Store(nil)
	var decoded motdObject
	if err := json.Unmarshal(o.motdForProtocol(765), &decoded); err != nil || decoded.Players.Online != 42 {
		t.Fatalf("fixed online count: %d, %v", decoded.Players.Online, err)
	}
}

var benchmarkMOTD []byte

func BenchmarkMOTD(b *testing.B) {
	b.Run("MarshalEveryRequest", func(b *testing.B) {
		o := testMOTDOutbound()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkMOTD = generateMOTD(764, o.config, &o.onlineCount)
		}
	})
	b.Run("Cached", func(b *testing.B) {
		o := testMOTDOutbound()
		o.motdForProtocol(764)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			benchmarkMOTD = o.motdForProtocol(764)
		}
	})
}
