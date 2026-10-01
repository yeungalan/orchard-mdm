// Command orchard runs the Orchard MDM server.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yeungalan/orchard-mdm/internal/config"
	"github.com/yeungalan/orchard-mdm/internal/server"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println("orchard", version)
		return
	}
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewTextHandler(os.Stderr, opts)
	if cfg.LogJSON {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	log := slog.New(h)
	slog.SetDefault(log)

	server.Version = version
	app, err := server.New(cfg, log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
	defer app.Close()
	if cfg.PublicURL == "" && app.MDM.PublicURL() == "" {
		log.Warn("no public URL configured: set -url https://mdm.example.com (or Settings → General) before enrolling devices")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
	log.Info("bye")
}
