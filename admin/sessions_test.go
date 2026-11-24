package admin

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/layou233/zbproxy/v3/adapter"
	"github.com/layou233/zbproxy/v3/monitoring"
)

type monitoredRuntime struct {
	fakeRuntime
	tracker *monitoring.Tracker
}

func (r *monitoredRuntime) ConnectionMonitor() *monitoring.Tracker { return r.tracker }

func TestSessionsAuthenticationPaginationAndFiltering(t *testing.T) {
	m, _, _ := networkFixture(t)
	tracker, _ := monitoring.Open("")
	m.runtime = &monitoredRuntime{tracker: tracker}
	for i := 0; i < 55; i++ {
		session := tracker.Begin(&adapter.Metadata{ServiceName: "game", Minecraft: &adapter.MinecraftMetadata{NextState: 2, PlayerName: fmt.Sprintf("Player%d", i)}}, "private-outbound", "mc.example:25565")
		session.AddUpload(10)
		if i == 54 {
			session.End()
		}
	}
	store, _ := OpenStore(filepath.Join(t.TempDir(), "whitelist.json"))
	server, err := NewManagedServer("127.0.0.1:0", "admin", "password", store, m)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, path, method string
		auth               bool
		status             int
		count, matched     int
	}{
		{"auth", "/api/sessions", "GET", false, 401, 0, 0},
		{"first page", "/api/sessions", "GET", true, 200, 50, 54},
		{"large page", "/api/sessions?page=999999999", "GET", true, 200, 4, 54},
		{"name filter", "/api/sessions?q=player53", "GET", true, 200, 1, 1},
		{"outbound filter", "/api/sessions?q=PRIVATE-outbound", "GET", true, 200, 50, 54},
		{"empty filter", "/api/sessions?q=absent", "GET", true, 200, 0, 0},
		{"history", "/api/sessions?mode=history", "GET", true, 200, 50, 55},
		{"offline player", "/api/sessions?mode=history&q=Player54", "GET", true, 200, 1, 1},
		{"bad mode", "/api/sessions?mode=bad", "GET", true, 400, 0, 0},
		{"method", "/api/sessions", "PUT", true, 405, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "http://127.0.0.1"+tt.path, nil)
			if tt.auth {
				r.SetBasicAuth("admin", "password")
			}
			r.Header.Set("X-ZBProxy-Admin", "1")
			w := httptest.NewRecorder()
			server.Handler.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if tt.status != 200 {
				return
			}
			var page sessionPage
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if len(page.Sessions)+len(page.Players) != tt.count || page.Matched != tt.matched || page.Summary.UploadBytes != 550 || page.Summary.ActiveSessions != 54 {
				t.Fatalf("wrong page or filtered summary: %+v", page)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private session response was cacheable")
			}
		})
	}
}
