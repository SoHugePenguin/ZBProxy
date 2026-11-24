package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/layou233/zbproxy/v3/monitoring"
)

type sessionPage struct {
	monitoring.Snapshot
	Mode    string `json:"mode"`
	Total   int    `json:"total"`
	Matched int    `json:"matched"`
	Page    int    `json:"page"`
	Pages   int    `json:"pages"`
}

func (m *NetworkManager) serveSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "仅支持读取连接统计"})
		return
	}
	provider, ok := m.runtime.(interface{ ConnectionMonitor() *monitoring.Tracker })
	if !ok || provider.ConnectionMonitor() == nil {
		jsonResponse(w, http.StatusServiceUnavailable, map[string]string{"error": "连接监控未启用"})
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "online"
	}
	if mode != "online" && mode != "history" {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "统计视图无效"})
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	snapshot := provider.ConnectionMonitor().Snapshot()
	result := sessionPage{Snapshot: snapshot, Mode: mode}
	match := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(strings.ToLower(value), query) {
				return true
			}
		}
		return false
	}
	if mode == "online" {
		result.Total = len(snapshot.Sessions)
		result.Players = []monitoring.Player{}
		result.Sessions = []monitoring.Connection{}
		for _, c := range snapshot.Sessions {
			if match(c.Name, c.UUID, c.Service, c.Outbound) {
				result.Sessions = append(result.Sessions, c)
			}
		}
		result.Matched = len(result.Sessions)
	} else {
		result.Total = len(snapshot.Players)
		result.Sessions = []monitoring.Connection{}
		result.Players = []monitoring.Player{}
		for _, p := range snapshot.Players {
			if match(p.Name, p.UUID, p.LastService, p.LastOutbound) {
				result.Players = append(result.Players, p)
			}
		}
		result.Matched = len(result.Players)
	}
	const size = 50
	result.Pages = (result.Matched + size - 1) / size
	if result.Pages < 1 {
		result.Pages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > result.Pages {
		page = result.Pages
	}
	result.Page = page
	start, end := (page-1)*size, page*size
	if end > result.Matched {
		end = result.Matched
	}
	if mode == "online" {
		result.Sessions = result.Sessions[start:end]
	} else {
		result.Players = result.Players[start:end]
	}
	jsonResponse(w, http.StatusOK, result)
}
