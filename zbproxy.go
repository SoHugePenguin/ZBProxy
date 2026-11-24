package zbproxy

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/layou233/zbproxy/v3/adapter"
	"github.com/layou233/zbproxy/v3/common"
	"github.com/layou233/zbproxy/v3/config"
	"github.com/layou233/zbproxy/v3/protocol"
	"github.com/layou233/zbproxy/v3/protocol/minecraft"
	"github.com/layou233/zbproxy/v3/route"
	"github.com/layou233/zbproxy/v3/service"
	"github.com/layou233/zbproxy/v3/transport"

	"github.com/phuslu/log"
)

type Options struct {
	Config          *config.Root
	LogWriter       log.Writer
	ConfigFilePath  string
	RuleRegistry    map[string]route.CustomRuleInitializer
	SnifferRegistry map[string]protocol.SnifferFunc
	DisableReload   bool
	PlayerAccess    func(string) bool
}

type Instance struct {
	applyMu         sync.Mutex
	appliedServices []*config.Service
	ctx             context.Context
	logger          *log.Logger
	config          *config.Root
	router          *route.Router
	serviceMap      map[string]adapter.Service
	outboundMap     map[string]adapter.Outbound
	ruleRegistry    map[string]route.CustomRuleInitializer
	snifferRegistry map[string]protocol.SnifferFunc
	playerAccess    func(string) bool
}

func NewInstance(ctx context.Context, options Options) (*Instance, error) {
	instance := &Instance{
		ctx: ctx,
		logger: &log.Logger{
			TimeFormat: "15:04:05",
			Writer:     options.LogWriter,
		},
		config:          options.Config,
		ruleRegistry:    options.RuleRegistry,
		snifferRegistry: options.SnifferRegistry,
		playerAccess:    options.PlayerAccess,
	}
	if options.LogWriter == nil {
		instance.logger.Writer = &log.ConsoleWriter{
			ColorOutput:    true,
			EndWithMessage: true,
		}
	}
	if options.Config == nil {
		if options.ConfigFilePath != "" {
			newConfig, err := config.LoadConfigFromFile(
				ctx, options.ConfigFilePath, !options.DisableReload, instance.logger)
			if err != nil {
				return nil, err
			}
			instance.config = newConfig
			if !options.DisableReload {
				// configure handler here
				// if you provide config directly from option
				// then configure it by yourself like this
				newConfig.SetUpdateHandler(instance.UpdateConfig)
			}
		} else {
			return nil, errors.New("no config provided for ZBProxy")
		}
	}
	instance.logger.Level = instance.config.Log.Level

	return instance, nil
}

func (i *Instance) Logger() *log.Logger {
	return i.logger
}

func (i *Instance) Router() *route.Router {
	return i.router
}

func (i *Instance) Start() error {
	var err error
	startTime := time.Now()

	// initialize outbounds
	outboundMap := make(map[string]adapter.Outbound, len(i.config.Outbounds))
	for _, outboundConfig := range i.config.Outbounds {
		var outbound adapter.Outbound
		outbound, err = protocol.NewOutbound(i.logger, outboundConfig)
		if err != nil {
			return common.Cause("initialize outbound ["+outboundConfig.Name+"]: ", err)
		}
		outboundMap[outbound.Name()] = outbound
	}
	i.outboundMap = outboundMap

	// initialize router
	i.router = &route.Router{}
	err = i.router.Initialize(i.ctx, i.logger, route.RouterOptions{
		Config:          &i.config.Router,
		OutboundMap:     outboundMap,
		ListMap:         i.config.Lists,
		RuleRegistry:    i.ruleRegistry,
		SnifferRegistry: i.snifferRegistry,
		PlayerAccess:    i.playerAccess,
		Services:        i.config.Services,
		Targets:         i.config.Outbounds,
	})
	if err != nil {
		return common.Cause("initialize router: ", err)
	}
	for _, outbound := range outboundMap {
		err = outbound.PostInitialize(i.router, i.router)
		if err != nil {
			return common.Cause("post initialize outbound ["+outbound.Name()+"]: ", err)
		}
	}

	// initialize services
	i.serviceMap = make(map[string]adapter.Service, len(i.config.Services))
	for _, serviceConfig := range i.config.Services {
		newService := service.NewService(i.logger, serviceConfig)
		newService.UpdateRouter(i.router)
		err = newService.Start(i.ctx)
		if err != nil {
			return common.Cause("start service ["+serviceConfig.Name+"]: ", err)
		}
		i.serviceMap[serviceConfig.Name] = newService
	}

	i.logger.Info().Str("duration", time.Now().Sub(startTime).String()).Msg("ZBProxy started")
	i.appliedServices = i.config.Services
	return nil
}

func (i *Instance) Reload() bool {
	return i.config.Reload()
}

func (i *Instance) UpdateConfig() {
	if err := i.ApplyConfig(i.config, nil); err != nil {
		i.logger.Error().Err(err).Msg("Configuration update failed")
	}
}

// ApplyConfig prepares a complete routing snapshot before switching new logins.
// The commit callback persists the file only after all listeners can be opened.
func (i *Instance) ApplyConfig(next *config.Root, commit func() error) error {
	i.applyMu.Lock()
	defer i.applyMu.Unlock()
	outbounds := make(map[string]adapter.Outbound)
	for _, c := range next.Outbounds {
		if c == nil || c.Name == "" {
			return errors.New("出口名称不能为空")
		}
		if _, exists := outbounds[c.Name]; exists {
			return fmt.Errorf("出口名称重复：%s", c.Name)
		}
		out, err := protocol.NewOutbound(i.logger, c)
		if err != nil {
			return err
		}
		outbounds[c.Name] = out
		if mc, ok := out.(*minecraft.Outbound); ok {
			if old, ok := i.outboundMap[c.Name].(*minecraft.Outbound); ok {
				mc.ShareConnectionCount(old)
			}
		}
	}
	options := route.RouterOptions{Config: &next.Router, OutboundMap: outbounds, ListMap: next.Lists, RuleRegistry: i.ruleRegistry, SnifferRegistry: i.snifferRegistry, PlayerAccess: i.playerAccess, Services: next.Services, Targets: next.Outbounds}
	prepared := &route.Router{}
	if err := prepared.Initialize(i.ctx, i.logger, options); err != nil {
		return err
	}
	for _, out := range outbounds {
		if err := out.PostInitialize(prepared, prepared); err != nil {
			return fmt.Errorf("出口 %s：%w", out.Name(), err)
		}
	}
	oldConfigs := make(map[string]*config.Service)
	for _, c := range i.appliedServices {
		oldConfigs[c.Name] = c
	}
	newServices := make(map[string]adapter.Service)
	changed := make(map[string]bool)
	for _, c := range next.Services {
		if c == nil || c.Name == "" {
			return errors.New("入口名称不能为空")
		}
		if _, duplicate := newServices[c.Name]; duplicate {
			return fmt.Errorf("入口名称重复：%s", c.Name)
		}
		if _, err := transport.NewServer(c.Encryption); err != nil {
			return err
		}
		old := oldConfigs[c.Name]
		// Changing only an entry's default route must not reopen its socket.
		if old != nil {
			a, b := *old, *c
			a.DefaultOutbound, b.DefaultOutbound = "", ""
			if reflect.DeepEqual(a, b) {
				newServices[c.Name] = i.serviceMap[c.Name]
				continue
			}
		}
		changed[c.Name] = true
		newServices[c.Name] = nil
	}
	closed := make(map[string]bool)
	opened := make(map[string]adapter.Service)
	rollback := func(cause error) error {
		for _, s := range opened {
			s.Close()
		}
		for name := range closed {
			s := service.NewService(i.logger, oldConfigs[name])
			s.UpdateRouter(i.router)
			if err := s.Start(i.ctx); err != nil {
				cause = errors.Join(cause, fmt.Errorf("恢复入口 %s 失败：%w", name, err))
			} else {
				i.serviceMap[name] = s
			}
		}
		return cause
	}
	for name, old := range i.serviceMap {
		if _, exists := newServices[name]; !exists || changed[name] {
			old.Close()
			closed[name] = true
		}
	}
	for _, c := range next.Services {
		if !changed[c.Name] {
			continue
		}
		s := service.NewService(i.logger, c)
		s.UpdateRouter(i.router)
		if err := s.Start(i.ctx); err != nil {
			return rollback(fmt.Errorf("入口 %s：%w", c.Name, err))
		}
		opened[c.Name], newServices[c.Name] = s, s
	}
	if commit != nil {
		if err := commit(); err != nil {
			return rollback(err)
		}
	}
	// Initialize already succeeded with exactly these options.
	if err := i.router.UpdateConfig(options); err != nil {
		return rollback(err)
	}
	i.config, i.serviceMap, i.outboundMap = next, newServices, outbounds
	i.appliedServices = next.Services
	return nil
}

func (i *Instance) Close() {
	i.applyMu.Lock()
	defer i.applyMu.Unlock()
	for _, s := range i.serviceMap {
		s.Close()
	}
}
