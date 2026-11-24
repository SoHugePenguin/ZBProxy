package route

import (
	"github.com/layou233/zbproxy/v3/adapter"
	"github.com/layou233/zbproxy/v3/config"
	"testing"
)

func TestPlayerRoutePriorityAndStatusFallback(t *testing.T) {
	uuid := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	rules := []config.PlayerRoute{
		{Enabled: true, Kind: "name", Value: "Steve", Outbound: "global"},
		{Enabled: true, Kind: "name", Value: "steve", Service: "game", Outbound: "specific"},
		{Enabled: true, Kind: "uuid", Value: "01020304-0506-0708-090a-0b0c0d0e0f10", Service: "game", Outbound: "uuid"},
		{Enabled: false, Kind: "name", Value: "Alex", Service: "game", Outbound: "disabled"},
	}
	for _, tt := range []struct {
		name, service, player string
		uuid                  [16]byte
		want                  string
	}{
		{"old client", "game", "STEVE", [16]byte{}, "specific"},
		{"uuid priority", "game", "Steve", uuid, "uuid"},
		{"other entry", "other", "Steve", uuid, "global"},
		{"disabled", "game", "Alex", [16]byte{}, "default"},
		{"server status", "game", "", [16]byte{}, "default"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := selectPlayerOutbound("default", &adapter.Metadata{ServiceName: tt.service, Minecraft: &adapter.MinecraftMetadata{PlayerName: tt.player, UUID: tt.uuid}}, rules)
			if got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}
