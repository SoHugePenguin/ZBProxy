package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIAndAccessProtection(t *testing.T) {
	s := openTestStore(t)
	server, err := NewServer("127.0.0.1:8080", "admin", "test-password", s)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string, auth, header bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		if auth {
			r.SetBasicAuth("admin", "test-password")
		}
		if header {
			r.Header.Set("X-ZBProxy-Admin", "1")
		}
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		server.Handler.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/whitelist", "", false, false, ""); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	if w := request("POST", "/api/players", `{"names":["Steve"]}`, true, false, ""); w.Code != 403 {
		t.Fatalf("missing CSRF header: %d", w.Code)
	}
	if w := request("POST", "/api/players", `{"names":["Steve"]}`, true, true, "https://other.example"); w.Code != 403 {
		t.Fatalf("cross-origin: %d", w.Code)
	}
	if w := request("POST", "/api/players", `{"names":["Steve","Alex"]}`, true, true, ""); w.Code != 200 {
		t.Fatalf("add: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", "/api/whitelist", `{"names":["Steve","Visitor"]}`, true, true, ""); w.Code != 400 {
		t.Fatalf("unregistered whitelist add: %d", w.Code)
	}
	if s.List("", 1).Total != 0 {
		t.Fatal("registration automatically added whitelist members")
	}
	if w := request("POST", "/api/whitelist", `{"names":["Steve","Alex"]}`, true, true, ""); w.Code != 200 {
		t.Fatalf("whitelist add: %d", w.Code)
	}
	if w := request("PUT", "/api/whitelist", `{"enabled":true}`, true, true, ""); w.Code != 200 {
		t.Fatalf("toggle: %d", w.Code)
	}
	if s.AllowPlayer("Visitor") || !s.AllowPlayer("STEVE") {
		t.Fatal("HTTP update did not affect login policy")
	}
	w := request("GET", "/api/whitelist?q=ste", "", true, false, "")
	var page Page
	if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.Matched != 1 || page.Players[0] != "Steve" {
		t.Fatalf("search: %s %v", w.Body.String(), err)
	}
	if w := request("DELETE", "/api/players", `{"names":["Steve"]}`, true, true, ""); w.Code != 200 {
		t.Fatalf("delete: %d", w.Code)
	}
	if s.AllowPlayer("Steve") {
		t.Fatal("HTTP deletion not applied")
	}
	if w := request("POST", "/api/players", `{"names":["<script>"]}`, true, true, ""); w.Code != 400 {
		t.Fatalf("invalid name: %d", w.Code)
	}
	if w := request("POST", "/api/players", `{"names":["Alex"]}{}`, true, true, ""); w.Code != 400 {
		t.Fatalf("trailing JSON: %d", w.Code)
	}
	for _, path := range []string{"/", "/app.css", "/app.js"} {
		w := request("GET", path, "", true, false, "")
		if w.Code != 200 || w.Header().Get("ETag") == "" {
			t.Fatalf("asset %s: %d", path, w.Code)
		}
	}
	if _, err = NewServer("0.0.0.0:8080", "admin", "", s); err == nil {
		t.Fatal("public listener accepted empty password")
	}
	local, _ := NewServer("127.0.0.1:8080", "admin", "", s)
	r := httptest.NewRequest("GET", "http://attacker.example:8080/api/whitelist", nil)
	w = httptest.NewRecorder()
	local.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("loopback listener accepted untrusted Host")
	}
}

func TestCustomUsername(t *testing.T) {
	s := openTestStore(t)
	server, err := NewServer("127.0.0.1:8080", "penguin", "test-password", s)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		user, password string
		status         int
	}{
		{"penguin", "test-password", http.StatusOK},
		{"admin", "test-password", http.StatusUnauthorized},
		{"penguin", "wrong-password", http.StatusUnauthorized},
	} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/whitelist", nil)
		r.SetBasicAuth(test.user, test.password)
		w := httptest.NewRecorder()
		server.Handler.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("username %q: got %d, want %d", test.user, w.Code, test.status)
		}
	}
	if _, err := NewServer("127.0.0.1:8080", "invalid:user", "password", s); err == nil {
		t.Fatal("accepted an invalid Basic Auth username")
	}
}
