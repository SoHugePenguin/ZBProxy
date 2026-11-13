package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

type policy struct {
	enabled bool
	names   map[string]string
	keys    []string
}

type diskPolicy struct {
	Enabled bool     `json:"enabled"`
	Players []string `json:"players"`
}

// Store publishes immutable snapshots. Login checks do one atomic load and
// one map lookup; disk I/O and sorting only happen when an administrator writes.
type Store struct {
	path    string
	write   sync.Mutex
	current atomic.Pointer[policy]
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	p := &policy{names: make(map[string]string)}
	data, err := os.ReadFile(path)
	if err == nil {
		var disk diskPolicy
		if err = json.Unmarshal(data, &disk); err != nil {
			return nil, fmt.Errorf("read whitelist: %w", err)
		}
		p.enabled = disk.Enabled
		for _, name := range disk.Players {
			if !validName(name) {
				return nil, fmt.Errorf("invalid Minecraft name in whitelist: %q", name)
			}
			p.names[strings.ToLower(name)] = name
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	p.sort()
	s.current.Store(p)
	return s, nil
}

func validName(name string) bool {
	if len(name) == 0 || len(name) > 16 {
		return false
	}
	for _, c := range []byte(name) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func (p *policy) sort() {
	p.keys = make([]string, 0, len(p.names))
	for key := range p.names {
		p.keys = append(p.keys, key)
	}
	sort.Strings(p.keys)
}

func (s *Store) AllowPlayer(name string) bool {
	p := s.current.Load()
	if !p.enabled {
		return true
	}
	_, found := p.names[strings.ToLower(name)]
	return found
}

// Update commits to disk before publishing. A failed save leaves the active
// whitelist unchanged. There is no player-count quota.
func (s *Store) Update(enabled *bool, add, remove []string) (int, error) {
	for _, names := range [][]string{add, remove} {
		for _, name := range names {
			if !validName(name) {
				return 0, fmt.Errorf("玩家名 %q 无效：只允许 1–16 位英文字母、数字和下划线", name)
			}
		}
	}
	s.write.Lock()
	defer s.write.Unlock()
	old := s.current.Load()
	next := &policy{enabled: old.enabled, names: make(map[string]string, len(old.names)+len(add))}
	for key, name := range old.names {
		next.names[key] = name
	}
	changed := 0
	if enabled != nil && *enabled != next.enabled {
		next.enabled = *enabled
		changed++
	}
	for _, name := range add {
		key := strings.ToLower(name)
		if _, exists := next.names[key]; !exists {
			next.names[key] = name
			changed++
		}
	}
	for _, name := range remove {
		key := strings.ToLower(name)
		if _, exists := next.names[key]; exists {
			delete(next.names, key)
			changed++
		}
	}
	if changed == 0 {
		return 0, nil
	}
	next.sort()
	players := make([]string, 0, len(next.keys))
	for _, key := range next.keys {
		players = append(players, next.names[key])
	}
	data, err := json.Marshal(diskPolicy{Enabled: next.enabled, Players: players})
	if err != nil {
		return 0, err
	}
	if err = saveAtomic(s.path, data); err != nil {
		return 0, err
	}
	s.current.Store(next)
	return changed, nil
}

func saveAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".zbproxy-whitelist-*")
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

type Page struct {
	Enabled bool     `json:"enabled"`
	Total   int      `json:"total"`
	Matched int      `json:"matched"`
	Page    int      `json:"page"`
	Pages   int      `json:"pages"`
	Players []string `json:"players"`
}

func (s *Store) List(query string, page int) Page {
	const pageSize = 50
	p := s.current.Load()
	query = strings.ToLower(query)
	if page < 1 {
		page = 1
	}
	// Bound the offset by the list length, including malicious page parameters.
	if max := len(p.keys)/pageSize + 1; page > max {
		page = max
	}
	result := Page{Enabled: p.enabled, Total: len(p.keys), Page: page, Players: make([]string, 0, pageSize)}
	start := (page - 1) * pageSize
	for _, key := range p.keys {
		if query != "" && !strings.Contains(key, query) {
			continue
		}
		if result.Matched >= start && len(result.Players) < pageSize {
			result.Players = append(result.Players, p.names[key])
		}
		result.Matched++
	}
	result.Pages = (result.Matched + pageSize - 1) / pageSize
	if result.Pages < 1 {
		result.Pages = 1
	}
	if page > result.Pages {
		return s.listLastPage(p, query, result.Pages)
	}
	return result
}

func (s *Store) listLastPage(p *policy, query string, page int) Page {
	r := Page{Enabled: p.enabled, Total: len(p.keys), Page: page, Pages: page, Players: make([]string, 0, 50)}
	for _, key := range p.keys {
		if query != "" && !strings.Contains(key, query) {
			continue
		}
		if r.Matched >= (page-1)*50 {
			r.Players = append(r.Players, p.names[key])
		}
		r.Matched++
	}
	return r
}
