// Command ninjaone-mcp is an MCP server for the NinjaOne public API v2.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/lcleveland/ninjaone-mcp/internal/config"
	"github.com/lcleveland/ninjaone-mcp/internal/ninjaone"
	"github.com/lcleveland/ninjaone-mcp/internal/server"
	"github.com/lcleveland/ninjaone-mcp/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ninjaone-mcp:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, warnings, err := config.Parse(args, os.Getenv)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if cfg.ShowVersion {
		fmt.Println(version.Version)
		return nil
	}
	// stdout belongs to the stdio transport; logs go to stderr.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	for _, w := range warnings {
		log.Warn(w)
	}
	log.Info("starting", "version", version.Version, "config", cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := ninjaone.New(cfg.BaseURL, cfg.ClientID, cfg.ClientSecret, cfg.Scopes, &http.Client{Timeout: cfg.RequestTimeout}, log)
	s, n := server.New(cfg, c, log)
	log.Info("registered tools", "count", n)
	if cfg.HTTP {
		return server.ServeHTTP(ctx, cfg, s, log)
	}
	return server.ServeStdio(ctx, s)
}
