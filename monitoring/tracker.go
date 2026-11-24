package monitoring

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/layou233/zbproxy/v3/adapter"
)

type Usage struct {
	UploadBytes   int64 `json:"upload_bytes"`
	DownloadBytes int64 `json:"download_bytes"`
	DurationMS    int64 `json:"duration_ms"`
	Connections   int64 `json:"connections"`
}

type Player struct {
	Usage
	Name           string    `json:"name"`
	UUID           string    `json:"uuid,omitempty"`
	LastService    string    `json:"last_service"`
	LastOutbound   string    `json:"last_outbound"`
	FirstSeen      time.Time `json:"first_seen"`
	LastSeen       time.Time `json:"last_seen"`
	LastConnected  time.Time `json:"last_connected"`
	ActiveSessions int       `json:"active_sessions,omitempty"`
}

type Connection struct {
	ID            uint64    `json:"id"`
	Name          string    `json:"name"`
	UUID          string    `json:"uuid,omitempty"`
	Service       string    `json:"service"`
	Outbound      string    `json:"outbound"`
	Target        string    `json:"target"`
	StartedAt     time.Time `json:"started_at"`
	DurationMS    int64     `json:"duration_ms"`
	UploadBytes   int64     `json:"upload_bytes"`
	DownloadBytes int64     `json:"download_bytes"`
}

type Summary struct {
	Usage
	OnlinePlayers  int `json:"online_players"`
	ActiveSessions int `json:"active_sessions"`
	KnownPlayers   int `json:"known_players"`
}

type Snapshot struct {
	GeneratedAt      time.Time    `json:"generated_at"`
	Summary          Summary      `json:"summary"`
	Sessions         []Connection `json:"sessions"`
	Players          []Player     `json:"players"`
	PersistenceError string       `json:"persistence_error,omitempty"`
}

type diskState struct {
	Version int      `json:"version"`
	Players []Player `json:"players"`
}

type Tracker struct {
	mu       sync.Mutex
	saveMu   sync.Mutex
	path     string
	nextID   uint64
	players  map[string]Player
	sessions map[uint64]*Session
	saveErr  string
}

type Session struct {
	tracker  *Tracker
	info     Connection
	upload   atomic.Int64
	download atomic.Int64
	finish   sync.Once
}

// An empty path keeps statistics in memory, useful for embedders and tests.
func Open(path string) (*Tracker, error) {
	t := &Tracker{path: path, players: map[string]Player{}, sessions: map[uint64]*Session{}}
	if path == "" {
		return t, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return nil, err
	}
	var state diskState
	if err = json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("read player statistics: %w", err)
	}
	if state.Version != 1 {
		return nil, errors.New("unsupported player statistics version")
	}
	for _, p := range state.Players {
		p.ActiveSessions = 0
		t.players[strings.ToLower(p.Name)] = p
	}
	return t, nil
}

func (t *Tracker) Begin(m *adapter.Metadata, outbound, target string) *Session {
	if t == nil || m == nil || m.Minecraft == nil || m.Minecraft.PlayerName == "" || m.Minecraft.NextState != 2 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	info := Connection{ID: t.nextID, Name: m.Minecraft.PlayerName, Service: m.ServiceName, Outbound: outbound, Target: target, StartedAt: time.Now()}
	if m.Minecraft.UUID != [16]byte{} {
		info.UUID = hex.EncodeToString(m.Minecraft.UUID[:])
	}
	s := &Session{tracker: t, info: info}
	t.sessions[info.ID] = s
	return s
}

func (s *Session) AddUpload(n int64) {
	if n > 0 {
		s.upload.Add(n)
	}
}

// Wrap deliberately does not expose unwrapping or io.Copy fast paths: both
// directions must pass through Read/Write for live byte accounting.
func (s *Session) Wrap(conn net.Conn) net.Conn { return &countedConn{Conn: conn, session: s} }

type countedConn struct {
	net.Conn
	session *Session
}

func (c *countedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.session.download.Add(int64(n))
	return n, err
}

func (c *countedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.session.upload.Add(int64(n))
	return n, err
}

func (s *Session) snapshot(now time.Time) Connection {
	c := s.info
	c.DurationMS = now.Sub(c.StartedAt).Milliseconds()
	c.UploadBytes, c.DownloadBytes = s.upload.Load(), s.download.Load()
	return c
}

func merge(p Player, c Connection, now time.Time) Player {
	if c.UUID != "" {
		p.UUID = c.UUID
	}
	if p.FirstSeen.IsZero() || c.StartedAt.Before(p.FirstSeen) {
		p.FirstSeen = c.StartedAt
	}
	if !c.StartedAt.Before(p.LastConnected) {
		p.Name, p.LastConnected, p.LastService, p.LastOutbound = c.Name, c.StartedAt, c.Service, c.Outbound
	}
	if now.After(p.LastSeen) {
		p.LastSeen = now
	}
	p.UploadBytes += c.UploadBytes
	p.DownloadBytes += c.DownloadBytes
	p.DurationMS += c.DurationMS
	p.Connections++
	return p
}

// End is called only after both relay directions have stopped.
func (s *Session) End() {
	s.finish.Do(func() {
		t := s.tracker
		t.mu.Lock()
		defer t.mu.Unlock()
		now := time.Now()
		key := strings.ToLower(s.info.Name)
		t.players[key] = merge(t.players[key], s.snapshot(now), now)
		delete(t.sessions, s.info.ID)
	})
}

func (t *Tracker) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	r := Snapshot{GeneratedAt: now, Sessions: []Connection{}, Players: []Player{}, PersistenceError: t.saveErr}
	players := make(map[string]Player, len(t.players))
	for k, p := range t.players {
		players[k] = p
	}
	for _, s := range t.sessions {
		c := s.snapshot(now)
		r.Sessions = append(r.Sessions, c)
		key := strings.ToLower(c.Name)
		p := merge(players[key], c, now)
		p.ActiveSessions++
		players[key] = p
	}
	for _, p := range players {
		r.Players = append(r.Players, p)
		r.Summary.UploadBytes += p.UploadBytes
		r.Summary.DownloadBytes += p.DownloadBytes
		r.Summary.DurationMS += p.DurationMS
		r.Summary.Connections += p.Connections
		if p.ActiveSessions > 0 {
			r.Summary.OnlinePlayers++
		}
	}
	r.Summary.ActiveSessions, r.Summary.KnownPlayers = len(r.Sessions), len(r.Players)
	sort.Slice(r.Sessions, func(i, j int) bool { return r.Sessions[i].ID > r.Sessions[j].ID })
	sort.Slice(r.Players, func(i, j int) bool {
		if r.Players[i].LastSeen.Equal(r.Players[j].LastSeen) {
			return r.Players[i].Name < r.Players[j].Name
		}
		return r.Players[i].LastSeen.After(r.Players[j].LastSeen)
	})
	return r
}

func (t *Tracker) Save() error {
	t.saveMu.Lock()
	defer t.saveMu.Unlock()
	if t.path == "" {
		return nil
	}
	state := t.Snapshot()
	for i := range state.Players {
		state.Players[i].ActiveSessions = 0
	}
	data, err := json.Marshal(diskState{Version: 1, Players: state.Players})
	if err == nil {
		err = saveAtomic(t.path, data)
	}
	t.mu.Lock()
	if err != nil {
		t.saveErr = "累计统计保存失败，当前数据仅保留在内存中"
	} else {
		t.saveErr = ""
	}
	t.mu.Unlock()
	return err
}

func saveAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".zbproxy-statistics-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// StartPersistence checkpoints active sessions as well as finished sessions.
// The returned stop function waits for a final checkpoint on graceful shutdown.
func (t *Tracker) StartPersistence(ctx context.Context, onError func(error)) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		save := func() {
			if err := t.Save(); err != nil && onError != nil {
				onError(err)
			}
		}
		save()
		for {
			select {
			case <-ticker.C:
				save()
			case <-ctx.Done():
				save()
				return
			}
		}
	}()
	return func() { cancel(); <-done }
}
