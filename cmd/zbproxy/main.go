package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/layou233/zbproxy/v3"
	"github.com/layou233/zbproxy/v3/admin"
	"github.com/layou233/zbproxy/v3/common"
	"github.com/layou233/zbproxy/v3/common/console"
	"github.com/layou233/zbproxy/v3/common/console/color"
	"github.com/layou233/zbproxy/v3/config"
	"github.com/layou233/zbproxy/v3/transport"
	"github.com/layou233/zbproxy/v3/version"
	"github.com/phuslu/log"
)

func main() {
	configPath := flag.String("config", "ZBProxy.json", "Proxy configuration file")
	webAddress := flag.String("web", "", "Override Web.Bind/Port in config; empty disables the web UI")
	whitelistPath := flag.String("whitelist", "ZBProxy.whitelist.json", "Managed player whitelist file")
	clientMode := flag.Bool("client", false, "Run local encrypted Minecraft client using EncryptionClient in config")
	flag.Parse()
	if *clientMode {
		if err := runEncryptedClient(*configPath); err != nil {
			fmt.Fprintln(os.Stderr, "Encrypted client:", err)
			os.Exit(1)
		}
		return
	}
	console.SetTitle(fmt.Sprintf("ZBProxy %v | Running...", version.Version))
	fmt.Println(color.Apply(color.FgHiRed, ` ______  _____   _____   _____    _____  __    __ __    __
|___  / |  _  \ |  _  \ |  _  \  /  _  \ \ \  / / \ \  / /
   / /  | |_| | | |_| | | |_| |  | | | |  \ \/ /   \ \/ /`), color.Apply(color.FgHiWhite, `
  / /   |  _  { |  ___/ |  _  /  | | | |   }  {     \  /
 / /__  | |_| | | |     | | \ \  | |_| |  / /\ \    / /
/_____| |_____/ |_|     |_|  \_\ \_____/ /_/  \_\  /_/`))
	fmt.Printf(color.Apply(color.FgHiGreen, "Welcome to ZBProxy %s (%s)!\n"), version.Version, version.CommitHash)
	fmt.Printf(color.Apply(color.FgHiBlack, "Build Information: %s, %s/%s, CGO %s\n"),
		runtime.Version(), runtime.GOOS, runtime.GOARCH, common.CGOHint)
	// go version.CheckUpdate()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startupLogger := &log.Logger{Level: log.WarnLevel, TimeFormat: "15:04:05", Writer: &log.ConsoleWriter{ColorOutput: true, EndWithMessage: true}}
	webLogger := *startupLogger
	webLogger.Level = log.InfoLevel
	settings, err := config.LoadConfigFromFile(ctx, *configPath, true, startupLogger)
	if err != nil {
		panic(err)
	}
	webOverride := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "web" {
			webOverride = true
		}
	})
	address := webListenAddress(settings.Web, *webAddress, webOverride)
	store, err := admin.OpenStore(*whitelistPath)
	if err != nil {
		panic(err)
	}
	var webServer *http.Server
	var webListener net.Listener
	if address != "" {
		var accessLogger *log.Logger
		if settings.Web.AccessLog {
			accessLogger = &webLogger
		}
		password := settings.Web.Password
		if override := os.Getenv("ZBPROXY_ADMIN_PASSWORD"); override != "" {
			password = override
		}
		webServer, err = admin.NewServer(address, settings.Web.Username, password, store, accessLogger)
		if err != nil {
			panic(err)
		}
		webListener, err = net.Listen("tcp", address)
		if err != nil {
			panic(err)
		}
		defer webListener.Close()
	}
	instance, err := zbproxy.NewInstance(ctx, zbproxy.Options{
		Config:       settings,
		PlayerAccess: store.AllowPlayer,
	})
	if err != nil {
		panic(err)
	}

	err = instance.Start()
	if err != nil {
		panic(err)
	}
	settings.SetUpdateHandler(instance.UpdateConfig)
	if webServer != nil {
		go func() {
			if err := webServer.Serve(webListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				webLogger.Error().Err(err).Msg("Web admin stopped unexpectedly")
			}
		}()
		webLogger.Info().Str("url", "http://"+webListener.Addr().String()).Msg("Web admin started")
		defer func() {
			shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			webServer.Shutdown(shutdown)
			webLogger.Info().Msg("Web admin stopped")
		}()
	} else {
		webLogger.Info().Msg("Web admin disabled")
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGHUP, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(signalChan)
	for {
		select {
		case s := <-signalChan:
			switch s {
			case syscall.SIGHUP:
				instance.Reload()
			case os.Interrupt, syscall.SIGTERM:
				cancel()
				return
			}
		}
	}
}

func runEncryptedClient(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var settings struct{ EncryptionClient config.EncryptionClient }
	if err := json.Unmarshal(content, &settings); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	logger := &log.Logger{Level: log.InfoLevel, TimeFormat: "15:04:05", Writer: &log.ConsoleWriter{ColorOutput: true, EndWithMessage: true}}
	return transport.RunClient(ctx, settings.EncryptionClient, logger)
}

func webListenAddress(settings config.Web, override string, explicit bool) string {
	if explicit {
		return override
	}
	if !settings.Enabled {
		return ""
	}
	return settings.Address()
}
