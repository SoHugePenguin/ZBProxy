package admin

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/phuslu/log"
)

//go:embed static/*
var assets embed.FS

type asset struct {
	body              []byte
	etag, contentType string
}

// NewServer serves a buildless, embedded UI.
// An empty password is only allowed on a loopback listener.
func NewServer(address, username, password string, store *Store, accessLogger ...*log.Logger) (*http.Server, error) {
	return NewManagedServer(address, username, password, store, nil, accessLogger...)
}

func NewManagedServer(address, username, password string, store *Store, manager *NetworkManager, accessLogger ...*log.Logger) (*http.Server, error) {
	if username == "" {
		username = "admin"
	}
	if strings.ContainsAny(username, ":\r\n") {
		return nil, errors.New("Web.Username 不能包含冒号或换行")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	local := ip != nil && ip.IsLoopback()
	if !local && password == "" {
		return nil, errors.New("非本机管理地址必须设置 ZBProxy.json 的 Web.Password 或环境变量 ZBPROXY_ADMIN_PASSWORD")
	}
	static := make(map[string]asset)
	for _, name := range []string{"index.html", "app.css", "app.js", "network.js", "monitor.js", "icons.svg"} {
		body, err := assets.ReadFile("static/" + name)
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256(body)
		static["/"+name] = asset{body, `"` + hex.EncodeToString(hash[:8]) + `"`, mime.TypeByExtension("." + strings.Split(name, ".")[1])}
	}
	passwordHash := sha256.Sum256([]byte(password))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if local {
			requestHost := r.Host
			if h, _, err := net.SplitHostPort(requestHost); err == nil {
				requestHost = h
			}
			hostIP := net.ParseIP(requestHost)
			if requestHost != "localhost" && (hostIP == nil || !hostIP.IsLoopback()) {
				http.Error(w, "Invalid host", http.StatusForbidden)
				return
			}
		}
		if password != "" {
			user, pass, ok := r.BasicAuth()
			passHash := sha256.Sum256([]byte(pass))
			if !ok || user != username || subtle.ConstantTimeCompare(passHash[:], passwordHash[:]) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="ZBProxy", charset="UTF-8"`)
				http.Error(w, "请使用配置的管理用户名和密码登录", http.StatusUnauthorized)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodGet {
				origin := r.Header.Get("Origin")
				if origin != "" {
					u, err := url.Parse(origin)
					if err != nil || u.Host != r.Host {
						http.Error(w, "Invalid origin", http.StatusForbidden)
						return
					}
				}
				if r.Header.Get("X-ZBProxy-Admin") != "1" {
					http.Error(w, "Invalid request", http.StatusForbidden)
					return
				}
			}
			if manager != nil && r.URL.Path == "/api/sessions" {
				manager.serveSessions(w, r)
			} else if manager != nil && (r.URL.Path == "/api/network" || r.URL.Path == "/api/probe") {
				manager.ServeHTTP(w, r)
			} else {
				serveAPI(w, r, store)
			}
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		if path == "/favicon.ico" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		a, ok := static[path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", a.contentType)
		w.Header().Set("Cache-Control", "private, no-cache")
		w.Header().Set("ETag", a.etag)
		http.ServeContent(w, r, path, time.Time{}, bytes.NewReader(a.body))
	})
	var finalHandler http.Handler = handler
	if len(accessLogger) > 0 && accessLogger[0] != nil {
		finalHandler = logRequests(handler, accessLogger[0])
	}
	return &http.Server{Addr: address, Handler: finalHandler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}, nil
}

type responseStatus struct {
	http.ResponseWriter
	status int
}

func (w *responseStatus) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseStatus) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseStatus) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func logRequests(next http.Handler, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		response := &responseStatus{ResponseWriter: w}
		next.ServeHTTP(response, r)
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		var entry *log.Entry
		switch {
		case status >= 500:
			entry = logger.Error()
		case status >= 400:
			entry = logger.Warn()
		default:
			entry = logger.Info()
		}
		entry.Str("method", r.Method).Str("path", r.URL.Path).
			Int("status", status).Str("remote", r.RemoteAddr).
			Int64("duration_us", time.Since(start).Microseconds()).Msg("Web request")
	})
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func decode(w http.ResponseWriter, r *http.Request, value any) error {
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if contentType != "application/json" {
		return errors.New("请求必须是 JSON")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return errors.New("请求内容无效或超过单次 1 MB 限制，请分批添加")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("请求内容无效")
	}
	return nil
}

func serveAPI(w http.ResponseWriter, r *http.Request, s *Store) {
	var enabled *bool
	var add, remove []string
	switch {
	case r.URL.Path == "/api/whitelist" && r.Method == http.MethodGet:
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		jsonResponse(w, http.StatusOK, s.List(r.URL.Query().Get("q"), page))
		return
	case r.URL.Path == "/api/whitelist" && r.Method == http.MethodPut:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decode(w, r, &body); err != nil || body.Enabled == nil {
			jsonResponse(w, 400, map[string]string{"error": "请提供有效的白名单开关"})
			return
		}
		enabled = body.Enabled
	case r.URL.Path == "/api/players" && (r.Method == http.MethodPost || r.Method == http.MethodDelete):
		var body struct {
			Names []string `json:"names"`
		}
		if err := decode(w, r, &body); err != nil {
			jsonResponse(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if len(body.Names) == 0 {
			jsonResponse(w, 400, map[string]string{"error": "请输入至少一个玩家名"})
			return
		}
		if r.Method == http.MethodPost {
			add = body.Names
		} else {
			remove = body.Names
		}
	default:
		jsonResponse(w, http.StatusNotFound, map[string]string{"error": "接口不存在或请求方法不支持"})
		return
	}
	changed, err := s.Update(enabled, add, remove)
	if err != nil {
		status := http.StatusInternalServerError
		for _, name := range append(add, remove...) {
			if !validName(name) {
				status = http.StatusBadRequest
				break
			}
		}
		message := "保存失败，请检查白名单文件目录和写入权限"
		if status == http.StatusBadRequest {
			message = err.Error()
		}
		jsonResponse(w, status, map[string]string{"error": message})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]int{"changed": changed})
}
