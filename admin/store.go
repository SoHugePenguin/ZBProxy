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
	enabled        bool
	names          map[string]string
	keys           []string
	registered     map[string]string
	registeredKeys []string
}

type diskPolicy struct {
	Enabled           bool     `json:"enabled"`
	Players           []string `json:"players"`
	RegisteredPlayers []string `json:"registered_players"`
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
	p := &policy{names: make(map[string]string), registered: make(map[string]string)}
	data, err := os.ReadFile(path)
	if err == nil {
		var disk diskPolicy
		if err = json.Unmarshal(data, &disk); err != nil {
			return nil, fmt.Errorf("read whitelist: %w", err)
		}
		p.enabled = disk.Enabled
		// Old files used one list for both registration and access. Preserve access.
		if disk.RegisteredPlayers == nil {
			disk.RegisteredPlayers = disk.Players
		}
		for _, name := range disk.RegisteredPlayers {
			if !validName(name) {
				return nil, fmt.Errorf("invalid registered Minecraft name: %q", name)
			}
			p.registered[strings.ToLower(name)] = name
		}
		for _, name := range disk.Players {
			if !validName(name) {
				return nil, fmt.Errorf("invalid Minecraft name in whitelist: %q", name)
			}
			if _, ok := p.registered[strings.ToLower(name)]; !ok {
				return nil, fmt.Errorf("whitelisted player is not registered: %q", name)
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
	p.registeredKeys = make([]string, 0, len(p.registered))
	for key := range p.registered {
		p.registeredKeys = append(p.registeredKeys, key)
	}
	sort.Strings(p.registeredKeys)
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
	return s.update(false, enabled, add, remove)
}

func (s *Store) UpdatePlayers(add, remove []string) (int, error) {
	return s.update(true, nil, add, remove)
}

type inputError struct{ message string }

func (e *inputError) Error() string { return e.message }

func (s *Store) update(registry bool, enabled *bool, add, remove []string) (int, error) {
	for _, names := range [][]string{add, remove} {
		for _, name := range names {
			if !validName(name) {
				return 0, &inputError{fmt.Sprintf("玩家名 %q 无效：只允许 1–16 位英文字母、数字和下划线", name)}
			}
		}
	}
	s.write.Lock()
	defer s.write.Unlock()
	old := s.current.Load()
	next := &policy{enabled: old.enabled, names: make(map[string]string, len(old.names)+len(add)), registered: make(map[string]string, len(old.registered)+len(add))}
	for key, name := range old.registered {
		next.registered[key] = name
	}
	if !registry {
		for _, name := range add {
			if _, ok := next.registered[strings.ToLower(name)]; !ok {
				return 0, &inputError{fmt.Sprintf("玩家 %s 尚未收录，请先在玩家收录中添加", name)}
			}
		}
	}
	for key, name := range old.names {
		next.names[key] = name
	}
	changed := 0
	if enabled != nil && *enabled != next.enabled {
		next.enabled = *enabled
		changed++
	}
	target := next.names
	if registry {
		target = next.registered
	}
	for _, name := range add {
		key := strings.ToLower(name)
		if _, exists := target[key]; !exists {
			target[key] = name
			changed++
		}
	}
	for _, name := range remove {
		key := strings.ToLower(name)
		if _, exists := target[key]; exists {
			delete(target, key)
			if registry {
				delete(next.names, key)
			}
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
	registered := make([]string, 0, len(next.registered))
	for _, key := range next.registeredKeys {
		registered = append(registered, next.registered[key])
	}
	data, err := json.Marshal(diskPolicy{Enabled: next.enabled, Players: players, RegisteredPlayers: registered})
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
	Enabled     bool     `json:"enabled"`
	Total       int      `json:"total"`
	Matched     int      `json:"matched"`
	Page        int      `json:"page"`
	Pages       int      `json:"pages"`
	Players     []string `json:"players"`
	Whitelisted []string `json:"whitelisted"`
}

func (s *Store) List(query string, page int) Page        { return s.list(query, page, false) }
func (s *Store) ListPlayers(query string, page int) Page { return s.list(query, page, true) }

func (s *Store) list(query string, page int, registry bool) Page {
	const pageSize = 50
	p := s.current.Load()
	names, keys := p.names, p.keys
	if registry {
		names = p.registered
		keys = p.registeredKeys
	}
	query = strings.ToLower(query)
	matched := make([]string, 0)
	for _, key := range keys {
		if strings.Contains(key, query) {
			matched = append(matched, key)
		}
	}
	pages := (len(matched) + pageSize - 1) / pageSize
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	if page < 1 {
		page = 1
	}
	result := Page{Enabled: p.enabled, Total: len(names), Matched: len(matched), Page: page, Pages: pages, Players: []string{}, Whitelisted: []string{}}
	for i := (page - 1) * pageSize; i < len(matched) && i < page*pageSize; i++ {
		key := matched[i]
		result.Players = append(result.Players, names[key])
		if _, ok := p.names[key]; ok {
			result.Whitelisted = append(result.Whitelisted, names[key])
		}
	}
	return result
}
