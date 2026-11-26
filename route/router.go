package route

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/layou233/zbproxy/v3/adapter"
	"github.com/layou233/zbproxy/v3/common"
	"github.com/layou233/zbproxy/v3/common/bufio"
	"github.com/layou233/zbproxy/v3/common/set"
	"github.com/layou233/zbproxy/v3/config"
	"github.com/layou233/zbproxy/v3/monitoring"
	"github.com/layou233/zbproxy/v3/protocol"
	"github.com/layou233/zbproxy/v3/protocol/minecraft"

	"github.com/phuslu/log"
)

type RouterOptions struct {
	Config          *config.Router
	OutboundMap     map[string]adapter.Outbound
	ListMap         map[string]set.StringSet
	RuleRegistry    map[string]CustomRuleInitializer
	SnifferRegistry map[string]protocol.SnifferFunc
	PlayerAccess    func(string) bool
	Services        []*config.Service
	Targets         []*config.Outbound
	Monitor         *monitoring.Tracker
}

type Router struct {
	access          sync.RWMutex
	ctx             context.Context
	logger          *log.Logger
	outboundMap     map[string]adapter.Outbound
	listMap         map[string]set.StringSet
	ruleRegistry    map[string]CustomRuleInitializer
	snifferRegistry map[string]protocol.SnifferFunc
	rules           []Rule
	defaultOutbound adapter.Outbound
	started         bool
	playerAccess    func(string) bool
	serviceDefaults map[string]string
	playerRoutes    []config.PlayerRoute
	targets         map[string]*config.Outbound
	monitor         *monitoring.Tracker
}

var _ adapter.Router = (*Router)(nil)

func (r *Router) Initialize(ctx context.Context, logger *log.Logger, options RouterOptions) error {
	if r.started {
		return errors.New("already initialized")
	}
	rules := make([]Rule, 0, len(options.Config.Rules))
	for i, ruleConfig := range options.Config.Rules {
		rule, err := NewRule(logger, ruleConfig, options.ListMap, options.RuleRegistry)
		if err != nil {
			return fmt.Errorf("initialize rule [index=%d]: %w", i, err)
		}
		rules = append(rules, rule)
	}
	if options.Config.DefaultOutbound != "" {
		defaultOutbound, err := r.findOutboundByName(options.OutboundMap, options.Config.DefaultOutbound)
		if err != nil {
			return common.Cause("default outbound is not found: ", err)
		}
		r.defaultOutbound = defaultOutbound
	} else {
		r.defaultOutbound, _ = protocol.NewOutbound(r.logger, &config.Outbound{
			Name: "default",
		})
		r.defaultOutbound.PostInitialize(r, r) // this is dangerous since the router is not fully initialized yet
	}
	r.ctx = ctx
	r.logger = logger
	r.outboundMap = options.OutboundMap
	r.listMap = options.ListMap
	r.ruleRegistry = options.RuleRegistry
	r.snifferRegistry = options.SnifferRegistry
	r.playerAccess = options.PlayerAccess
	r.monitor = options.Monitor
	r.serviceDefaults = make(map[string]string)
	for _, service := range options.Services {
		if service.DefaultOutbound != "" {
			if _, err := r.findOutboundByName(options.OutboundMap, service.DefaultOutbound); err != nil {
				return err
			}
			r.serviceDefaults[service.Name] = service.DefaultOutbound
		}
	}
	r.playerRoutes = append([]config.PlayerRoute(nil), options.Config.PlayerRoutes...)
	for _, rule := range r.playerRoutes {
		if _, err := r.findOutboundByName(options.OutboundMap, rule.Outbound); err != nil {
			return err
		}
	}
	r.targets = make(map[string]*config.Outbound)
	for _, target := range options.Targets {
		r.targets[target.Name] = target
	}
	r.rules = rules
	r.started = true
	return nil
}

func (r *Router) MonitorConnection(metadata *adapter.Metadata, outbound, target string) *monitoring.Session {
	r.access.RLock()
	monitor := r.monitor
	r.access.RUnlock()
	return monitor.Begin(metadata, outbound, target)
}

func (r *Router) HandleConnection(conn net.Conn, metadata *adapter.Metadata) {
	r.access.RLock()
	routeLogger := r.logger
	ctx := r.ctx
	outbound := r.defaultOutbound
	rules := r.rules
	outboundMap := r.outboundMap
	snifferRegistry := r.snifferRegistry
	serviceDefaults, playerRoutes, targets := r.serviceDefaults, r.playerRoutes, r.targets
	r.access.RUnlock()
	cachedConn := bufio.NewCachedConn(conn)
	if selected, managed := serviceDefaults[metadata.ServiceName]; managed {
		if err := minecraft.SniffClientHandshake(cachedConn, metadata); err != nil {
			cachedConn.Close()
			return
		}
		cachedConn.Rewind(0)
		selected = selectPlayerOutbound(selected, metadata, playerRoutes)
		var err error
		outbound, err = r.findOutboundByName(outboundMap, selected)
		if err != nil {
			cachedConn.Close()
			return
		}
		if target := targets[selected]; target != nil {
			metadata.DestinationHostname, metadata.DestinationPort = target.TargetAddress, target.TargetPort
			metadata.Minecraft.RewrittenDestination, metadata.Minecraft.RewrittenPort = target.TargetAddress, target.TargetPort
		}
	} else {
		for i, rule := range rules {
			match := rule.Match(metadata)
			if match {
				routeLogger.Trace().Str("id", metadata.ConnectionID).Int("rule_index", i).Msg("Rule matched")
				ruleConfig := rule.Config()
				// handle sniff
				if len(ruleConfig.Sniff) > 0 {
					protocol.Sniff(routeLogger, cachedConn, metadata, snifferRegistry, ruleConfig.Sniff...)
				}
				// handle rewrite
				if ruleConfig.Rewrite.TargetAddress != "" {
					metadata.DestinationHostname = ruleConfig.Rewrite.TargetAddress
				}
				if ruleConfig.Rewrite.TargetPort > 0 {
					metadata.DestinationPort = ruleConfig.Rewrite.TargetPort
				}
				if ruleConfig.Rewrite.Minecraft != nil {
					if metadata.Minecraft == nil {
						routeLogger.Debug().Str("id", metadata.ConnectionID).Int("rule_index", i).Msg("No Minecraft metadata, skipped rewrite")
					} else {
						if ruleConfig.Rewrite.Minecraft.Hostname != "" {
							metadata.Minecraft.RewrittenDestination = ruleConfig.Rewrite.Minecraft.Hostname
						}
						if ruleConfig.Rewrite.Minecraft.Port > 0 {
							metadata.Minecraft.RewrittenPort = ruleConfig.Rewrite.Minecraft.Port
						}
						if ruleConfig.Rewrite.Minecraft.Intent > 0 {
							metadata.Minecraft.NextState = ruleConfig.Rewrite.Minecraft.Intent
						}
					}
				}
				// handle outbound
				if ruleConfig.Outbound != "" {
					var err error
					outbound, err = r.findOutboundByName(outboundMap, ruleConfig.Outbound)
					if err != nil {
						routeLogger.Error().Str("id", metadata.ConnectionID).Int("rule_index", i).
							Err(err).Msg("Failed to find outbound")
						conn.Close()
						return
					}
					break
				}
			}
		}
	}

	if injectOutbound, isInject := outbound.(adapter.InjectOutbound); isInject {
		err := injectOutbound.InjectConnection(ctx, cachedConn, metadata)
		var logger *log.Entry
		if err == nil {
			logger = routeLogger.Info()
		} else {
			logger = routeLogger.Warn()
		}
		logger = logger.Str("id", metadata.ConnectionID).Str("outbound", outbound.Name())
		if err != nil {
			logger = logger.Err(err)
		}
		logger.Msg("Handled outbound connection")
		cachedConn.Close()
		return
	} else if metadata.DestinationHostname != "" && metadata.DestinationPort > 0 {
		destinationConn, err := adapter.DialContextWithMetadata(outbound, ctx, "tcp",
			net.JoinHostPort(metadata.DestinationHostname, strconv.FormatUint(uint64(metadata.DestinationPort), 10)),
			metadata)
		if err != nil {
			routeLogger.Warn().Str("id", metadata.ConnectionID).Str("outbound", outbound.Name()).
				Err(err).Msg("Failed to dial outbound connection")
			cachedConn.Close()
			return
		}
		err = bufio.CopyConn(destinationConn, cachedConn)
		var logger *log.Entry
		if err == nil {
			logger = routeLogger.Info()
		} else {
			logger = routeLogger.Warn()
		}
		logger = logger.Str("id", metadata.ConnectionID).Str("outbound", outbound.Name())
		if err != nil {
			logger = logger.Err(err)
		}
		logger.Msg("Handled outbound connection")
		cachedConn.Close()
		return
	}
	routeLogger.Info().Str("id", metadata.ConnectionID).Msg("Closed leaked connection")
	cachedConn.Close()
}

func (r *Router) FindOutboundByName(name string) (adapter.Outbound, error) {
	r.access.RLock()
	defer r.access.RUnlock()
	return r.findOutboundByName(r.outboundMap, name)
}

func selectPlayerOutbound(fallback string, metadata *adapter.Metadata, rules []config.PlayerRoute) string {
	m := metadata.Minecraft
	if m == nil || m.PlayerName == "" {
		return fallback
	}
	// UUID is more specific; names remain compatible with pre-1.19 clients.
	for _, kind := range []string{"uuid", "name"} {
		for _, serviceName := range []string{metadata.ServiceName, ""} {
			for _, rule := range rules {
				if !rule.Enabled || rule.Kind != kind || rule.Service != serviceName {
					continue
				}
				match := kind == "name" && strings.EqualFold(rule.Value, m.PlayerName)
				if kind == "name" {
					for _, name := range rule.Players {
						if strings.EqualFold(name, m.PlayerName) {
							match = true
							break
						}
					}
				}
				if kind == "uuid" && m.UUID != [16]byte{} {
					match = strings.EqualFold(strings.ReplaceAll(rule.Value, "-", ""), hex.EncodeToString(m.UUID[:]))
				}
				if match {
					return rule.Outbound
				}
			}
		}
	}
	return fallback
}

// AllowPlayer is checked only at Minecraft login, never on the relay data path.
func (r *Router) AllowPlayer(name string) bool {
	r.access.RLock()
	check := r.playerAccess
	r.access.RUnlock()
	return check == nil || check(name)
}

func (r *Router) findOutboundByName(outboundMap map[string]adapter.Outbound, name string) (adapter.Outbound, error) {
	switch name {
	case "REJECT":
		return rejectOutbound{}, nil
	case "RESET":
		return resetOutbound{}, nil
	}
	if outboundMap == nil {
		return nil, errors.New("outbounds are not initialized")
	}
	outbound, ok := outboundMap[name]
	if !ok {
		return nil, fmt.Errorf("outbound not found [%s]", name)
	}
	return outbound, nil
}

func (r *Router) FindListsByTag(tags []string) ([]set.StringSet, error) {
	if r.listMap == nil {
		return nil, errors.New("lists are not initialized")
	}
	lists := make([]set.StringSet, 0, len(tags))
	for _, tag := range tags {
		list, ok := r.listMap[tag]
		if !ok {
			return nil, fmt.Errorf("list not found [%s]", tag)
		}
		lists = append(lists, list)
	}
	return lists, nil
}

func (r *Router) UpdateConfig(newOptions RouterOptions) error {
	r.access.Lock()
	defer r.access.Unlock()
	r.started = false
	return r.Initialize(r.ctx, r.logger, newOptions)
}
