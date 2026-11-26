package admin

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/layou233/zbproxy/v3/common/network"
	"github.com/layou233/zbproxy/v3/common/network/proxydial"
	"github.com/layou233/zbproxy/v3/config"
)

type Runtime interface {
	ApplyConfig(*config.Root, func() error) error
}

type NetworkState struct {
	Revision                 uint64               `json:"revision"`
	Services                 []*config.Service    `json:"services"`
	Outbounds                []*config.Outbound   `json:"outbounds"`
	PlayerRoutes             []config.PlayerRoute `json:"player_routes"`
	ProxyPasswords           map[string]bool      `json:"proxy_passwords,omitempty"`
	EncryptionPasswords      map[string]bool      `json:"encryption_passwords,omitempty"`
	ClearProxyPasswords      []string             `json:"clear_proxy_passwords,omitempty"`
	ClearEncryptionPasswords []string             `json:"clear_encryption_passwords,omitempty"`
}

type NetworkManager struct {
	mu         sync.Mutex
	path       string
	raw        map[string]json.RawMessage
	current    *config.Root
	runtime    Runtime
	revision   uint64
	probeSlots chan struct{}
}

func cloneRoot(root *config.Root) *config.Root {
	// Log's zero value is not JSON-round-trippable; copy it independently.
	data, _ := json.Marshal(map[string]any{"Web": root.Web, "Services": root.Services, "Outbounds": root.Outbounds, "Router": root.Router, "Lists": root.Lists})
	var out config.Root
	json.Unmarshal(data, &out)
	out.Log = root.Log
	return &out
}

func OpenNetworkManager(path string, root *config.Root, runtime Runtime) (*NetworkManager, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := &NetworkManager{path: path, current: cloneRoot(root), runtime: runtime, probeSlots: make(chan struct{}, 2)}
	if err = json.Unmarshal(data, &m.raw); err != nil {
		return nil, err
	}
	importServiceDefaults(m.current)
	return m, nil
}

// Import the original simple service routes into the form-based editor.
func importServiceDefaults(c *config.Root) {
	for _, s := range c.Services {
		if s.DefaultOutbound != "" {
			continue
		}
		for _, rule := range c.Router.Rules {
			var name string
			if rule.Type == config.RuleTypeServiceName && !rule.Invert && json.Unmarshal(rule.Parameter, &name) == nil && name == s.Name && rule.Outbound != "" {
				s.DefaultOutbound = rule.Outbound
				break
			}
		}
	}
}

func (m *NetworkManager) Snapshot() NetworkState {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := cloneRoot(m.current)
	state := NetworkState{Revision: m.revision, Services: c.Services, Outbounds: c.Outbounds, PlayerRoutes: c.Router.PlayerRoutes, ProxyPasswords: map[string]bool{}, EncryptionPasswords: map[string]bool{}}
	if state.Services == nil {
		state.Services = []*config.Service{}
	}
	if state.Outbounds == nil {
		state.Outbounds = []*config.Outbound{}
	}
	if state.PlayerRoutes == nil {
		state.PlayerRoutes = []config.PlayerRoute{}
	}
	for _, out := range state.Outbounds {
		state.ProxyPasswords[out.Name] = out.ProxyOptions.Password != ""
		out.ProxyOptions.Password = ""
	}
	for _, in := range state.Services {
		if in.Encryption != nil {
			state.EncryptionPasswords[in.Name] = in.Encryption.Password != ""
			in.Encryption.Password = ""
		}
	}
	return state
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func (m *NetworkManager) Save(state NetworkState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state.Revision != m.revision {
		return errors.New("配置已被其他页面修改，请刷新后重试")
	}
	next := cloneRoot(m.current)
	oldOut := map[string]*config.Outbound{}
	for _, out := range next.Outbounds {
		oldOut[out.Name] = out
	}
	oldIn := map[string]*config.Service{}
	for _, in := range next.Services {
		oldIn[in.Name] = in
	}
	for _, out := range state.Outbounds {
		if out == nil {
			return errors.New("出口不能为空")
		}
		if old := oldOut[out.Name]; old != nil && out.ProxyOptions.Password == "" && !contains(state.ClearProxyPasswords, out.Name) {
			out.ProxyOptions.Password = old.ProxyOptions.Password
		}
		if contains(state.ClearProxyPasswords, out.Name) || out.ProxyOptions.Type == "direct" || out.ProxyOptions.Type == "" {
			out.ProxyOptions.Password = ""
		}
	}
	for _, in := range state.Services {
		if in == nil {
			return errors.New("入口不能为空")
		}
		if old := oldIn[in.Name]; old != nil && old.Encryption != nil && in.Encryption != nil && in.Encryption.Password == "" && !contains(state.ClearEncryptionPasswords, in.Name) {
			in.Encryption.Password = old.Encryption.Password
		}
	}
	next.Services, next.Outbounds, next.Router.PlayerRoutes = state.Services, state.Outbounds, state.PlayerRoutes
	// Replaced service rules are represented by DefaultOutbound from now on.
	rules := next.Router.Rules[:0]
	for _, rule := range next.Router.Rules {
		var name string
		if rule.Type == config.RuleTypeServiceName && !rule.Invert && json.Unmarshal(rule.Parameter, &name) == nil && oldIn[name] != nil && oldIn[name].DefaultOutbound != "" {
			continue
		}
		rules = append(rules, rule)
	}
	next.Router.Rules = rules
	if err := validateNetwork(next); err != nil {
		return err
	}
	raw := make(map[string]json.RawMessage, len(m.raw))
	for key, value := range m.raw {
		if strings.EqualFold(key, "Services") || strings.EqualFold(key, "Outbounds") || strings.EqualFold(key, "Router") {
			continue
		}
		raw[key] = value
	}
	for key, value := range map[string]any{"Services": next.Services, "Outbounds": next.Outbounds, "Router": next.Router} {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		raw[key] = data
	}
	data, err := json.MarshalIndent(raw, "", "    ")
	if err != nil {
		return err
	}
	if err = m.runtime.ApplyConfig(cloneRoot(next), func() error { return saveAtomic(m.path, append(data, '\n')) }); err != nil {
		return err
	}
	m.current, m.raw = next, raw
	m.revision++
	return nil
}

func validLabel(name string) bool {
	return strings.TrimSpace(name) != "" && len(name) <= 128 && !strings.ContainsAny(name, "\r\n\x00")
}

func validateNetwork(c *config.Root) error {
	outbounds := map[string]*config.Outbound{}
	for _, out := range c.Outbounds {
		if !validLabel(out.Name) || out.Name == "RESET" || out.Name == "REJECT" {
			return errors.New("出口名称无效或使用了保留名称")
		}
		if outbounds[out.Name] != nil {
			return fmt.Errorf("出口名称重复：%s", out.Name)
		}
		if out.TargetAddress == "" || strings.ContainsAny(out.TargetAddress, "\r\n /@") || out.TargetPort == 0 {
			return fmt.Errorf("出口 %s：请填写目标服务器和有效端口", out.Name)
		}
		if out.Minecraft == nil {
			return fmt.Errorf("出口 %s：网站管理的出口需要 Minecraft 配置", out.Name)
		}
		p := out.ProxyOptions
		if _, err := proxydial.New(network.SystemDialer, p.Type, p.Network, p.Address, p.Username, p.Password); err != nil {
			return fmt.Errorf("出口 %s：%w", out.Name, err)
		}
		outbounds[out.Name] = out
	}
	hasOutbound := func(name string) bool { return name == "RESET" || name == "REJECT" || outbounds[name] != nil }
	if c.Router.DefaultOutbound != "" && !hasOutbound(c.Router.DefaultOutbound) {
		return errors.New("全局默认出口不存在，请先调整原有路由")
	}
	for i, rule := range c.Router.Rules {
		if rule.Outbound != "" && !hasOutbound(rule.Outbound) {
			return fmt.Errorf("原有路由规则 %d 引用了不存在的出口：%s", i+1, rule.Outbound)
		}
	}
	for _, out := range c.Outbounds {
		seen := map[string]bool{out.Name: true}
		for name := out.Dialer; name != ""; {
			if seen[name] || outbounds[name] == nil {
				return fmt.Errorf("出口 %s：拨号链循环或引用了不存在的出口", out.Name)
			}
			if outbounds[name].Minecraft != nil {
				return fmt.Errorf("出口 %s：Minecraft 出口不能用作底层拨号器", out.Name)
			}
			seen[name] = true
			name = outbounds[name].Dialer
		}
	}
	services, addresses := map[string]bool{}, map[string]bool{}
	for _, in := range c.Services {
		if !validLabel(in.Name) || services[in.Name] {
			return errors.New("入口名称无效或重复")
		}
		if in.Listen == 0 {
			return errors.New("入口端口必须在 1–65535 之间")
		}
		if in.Bind != "" && net.ParseIP(in.Bind) == nil {
			return errors.New("入口监听地址必须是 IP 地址")
		}
		address := net.JoinHostPort(in.Bind, strconv.Itoa(int(in.Listen)))
		if addresses[address] {
			return fmt.Errorf("监听地址重复：%s", address)
		}
		if in.DefaultOutbound != "" && !hasOutbound(in.DefaultOutbound) {
			return fmt.Errorf("入口 %s：请选择有效的默认出口", in.Name)
		}
		if in.Minecraft != nil || in.TLSSniffing != nil {
			return fmt.Errorf("入口 %s 使用旧版内嵌协议配置，请先迁移为独立 Minecraft 出口", in.Name)
		}
		services[in.Name], addresses[address] = true, true
	}
	seen := map[string]bool{}
	for i := range c.Router.PlayerRoutes {
		r := &c.Router.PlayerRoutes[i]
		if r.Service != "" && !services[r.Service] {
			return errors.New("分流规则引用了不存在的入口")
		}
		if !hasOutbound(r.Outbound) {
			return errors.New("分流规则引用了不存在的出口，请先调整对应规则")
		}
		r.Value = strings.TrimSpace(r.Value)
		switch r.Kind {
		case "name":
			if len(r.Players) > 0 && r.Value != "" {
				return errors.New("分流规则不能同时设置单个玩家和玩家列表")
			}
			names := r.Players
			if len(names) == 0 {
				names = []string{r.Value}
			}
			for j, name := range names {
				name = strings.TrimSpace(name)
				if !validName(name) {
					return errors.New("分流玩家名必须是 1–16 位字母、数字或下划线")
				}
				key := "name\x00" + strings.ToLower(name)
				if seen[key] {
					return fmt.Errorf("玩家 %s 已被其他分流规则占用，每位玩家只能分配一个出口", name)
				}
				seen[key] = true
				if len(r.Players) > 0 {
					r.Players[j] = name
				}
			}
		case "uuid":
			if len(r.Players) > 0 {
				return errors.New("UUID 规则不能包含玩家名列表")
			}
			v := strings.ToLower(strings.ReplaceAll(r.Value, "-", ""))
			decoded, err := hex.DecodeString(v)
			if err != nil || len(decoded) != 16 || v == strings.Repeat("0", 32) {
				return errors.New("请输入有效的玩家 UUID")
			}
			r.Value = v
		default:
			return errors.New("分流类型必须是玩家名或 UUID")
		}
		if r.Kind == "name" {
			continue
		}
		key := r.Kind + "\x00" + strings.ToLower(r.Value)
		if seen[key] {
			return errors.New("同一玩家不能重复配置分流规则")
		}
		seen[key] = true
	}
	return nil
}

func (m *NetworkManager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/network" && r.Method == http.MethodGet:
		jsonResponse(w, 200, m.Snapshot())
	case r.URL.Path == "/api/network" && r.Method == http.MethodPut:
		var state NetworkState
		if err := decode(w, r, &state); err != nil {
			jsonResponse(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if err := m.Save(state); err != nil {
			jsonResponse(w, 400, map[string]string{"error": err.Error()})
			return
		}
		jsonResponse(w, 200, m.Snapshot())
	case r.URL.Path == "/api/probe" && r.Method == http.MethodPost:
		var body struct {
			Name string `json:"name"`
			Mode string `json:"mode"`
		}
		if err := decode(w, r, &body); err != nil {
			jsonResponse(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if body.Mode != "tcp" && body.Mode != "minecraft" {
			jsonResponse(w, 400, map[string]string{"error": "请选择有效的测试方式"})
			return
		}
		select {
		case m.probeSlots <- struct{}{}:
			defer func() { <-m.probeSlots }()
		default:
			jsonResponse(w, 429, map[string]string{"error": "已有线路测试正在进行，请稍后重试"})
			return
		}
		result, err := m.Probe(r.Context(), body.Name, body.Mode)
		if err != nil {
			jsonResponse(w, 400, map[string]string{"error": err.Error()})
			return
		}
		jsonResponse(w, 200, result)
	default:
		jsonResponse(w, 404, map[string]string{"error": "接口不存在或请求方法不支持"})
	}
}

func (m *NetworkManager) Reload(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := os.ReadFile(m.path)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err = json.Unmarshal(data, &raw); err != nil {
		return err
	}
	oldJSON, _ := json.Marshal(m.raw)
	newJSON, _ := json.Marshal(raw)
	if bytes.Equal(oldJSON, newJSON) {
		return nil
	}
	var next config.Root
	next.Web = config.DefaultWeb()
	if err = json.Unmarshal(data, &next); err != nil {
		return err
	}
	importServiceDefaults(&next)
	if err = m.runtime.ApplyConfig(cloneRoot(&next), nil); err != nil {
		return err
	}
	m.raw = raw
	m.current = &next
	m.revision++
	return nil
}

// Watch the parent directory so atomic replacement does not detach the watch.
func (m *NetworkManager) Watch(ctx context.Context, onError func(error)) error {
	path, err := filepath.Abs(m.path)
	if err != nil {
		return err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err = watcher.Add(filepath.Dir(path)); err != nil {
		watcher.Close()
		return err
	}
	go func() {
		defer watcher.Close()
		timer := time.NewTimer(time.Hour)
		if !timer.Stop() {
			<-timer.C
		}
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Name == path && event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(100 * time.Millisecond)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				if onError != nil {
					onError(err)
				}
			case <-timer.C:
				if err := m.Reload(ctx); err != nil && ctx.Err() == nil && onError != nil {
					onError(err)
				}
			}
		}
	}()
	return nil
}
