package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/syklonAK/night-watcher/internal/edge"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/syklonAK/night-watcher/cmd"
	"github.com/syklonAK/night-watcher/internal/app"
	"github.com/syklonAK/night-watcher/internal/localproxy"
	"github.com/syklonAK/night-watcher/internal/manage"
	"github.com/syklonAK/night-watcher/internal/menu"
	"github.com/syklonAK/night-watcher/internal/monitor"
	"github.com/syklonAK/night-watcher/internal/telegram"
	"github.com/syklonAK/night-watcher/internal/utils"
	"github.com/syklonAK/night-watcher/internal/webui"
)

var logger = utils.NewLogger("info")

// main has two modes:
//
//   - Engine mode:  `night-watcher -c /etc/night-watcher/<name>.toml`
//     Runs a single tunnel (server or client). This is what the systemd
//     units execute. Behaviour is identical to the original engine.
//
//   - Menu mode:    `night-watcher`  (no arguments)
//     Opens the interactive management CLI on the VPS.
func main() {
	configPath := flag.String("c", "", "path to a tunnel configuration file (TOML) — runs in engine mode")
	showVersion := flag.Bool("v", false, "print the version and exit")
	restartAll := flag.Bool("restart-all", false, "restart every configured tunnel and exit (used by the auto-refresh job)")
	tgReport := flag.Bool("telegram-report", false, "send a Telegram status report and exit (used by the scheduled job)")
	webPanel := flag.Bool("webui", false, "run the web panel (used by the night-watcher-webui service)")
	monitorMode := flag.Bool("monitor", false, "run the watchdog, Telegram bot and alerts (used by the night-watcher-monitor service)")
	proxyMode := flag.Bool("proxy", false, "run the built-in SOCKS5/HTTP proxy (used by the night-watcher-proxy service)")
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println(app.Version)
		return
	case *restartAll:
		ok, failed := manage.RestartAll()
		fmt.Printf("restarted %d tunnels, %d failed\n", ok, failed)
		return
	case *tgReport:
		if err := telegram.SendStatusNow(); err != nil {
			logger.Errorf("telegram report failed: %v", err)
			os.Exit(1)
		}
		return
	case *webPanel:
		if err := webui.Serve(); err != nil {
			logger.Fatalf("web panel failed: %v", err)
		}
		return
	case *monitorMode:
		monitor.Run()
		return
	case *proxyMode:
		runProxy()
		return
	}

	// No config file -> interactive menu.
	if *configPath == "" {
		menu.Run()
		return
	}

	runEngine(*configPath)
}

// runProxy runs the built-in proxy until a termination signal arrives. The
// proxy is a plain loopback service; the tunnel forwards to it like any backend.
func runProxy() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	em, err := edge.Start(ctx, app.ConfigDir, false)
	if err != nil {
		logger.Errorf("edge startup: %v", err)
		return
	}
	defer em.Close()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go localproxy.Run(ctx)
	<-sigChan
	cancel()
	logger.Info("night-watcher proxy stopped")
}

// runEngine starts a single tunnel from a TOML config and blocks until a
// termination signal arrives, then shuts down gracefully.
func runEngine(configPath string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	em, err := edge.Start(ctx, app.ConfigDir, false)
	if err != nil {
		logger.Errorf("edge startup: %v", err)
		return
	}
	defer em.Close()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	done := make(chan struct{})
	go func() { defer close(done); cmd.Run(configPath, ctx) }()

	<-sigChan
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		logger.Warn("tunnel shutdown timed out")
	}
	logger.Info("night-watcher engine stopped")
}
