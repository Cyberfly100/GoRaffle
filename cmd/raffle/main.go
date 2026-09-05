package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	assetsweb "github.com/goraffle/raffle"
	"github.com/goraffle/raffle/internal/db"
	"github.com/goraffle/raffle/internal/handler"
	"github.com/goraffle/raffle/internal/raffle"
	"github.com/goraffle/raffle/internal/ws"
)

func main() {
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	var assets fs.FS = assetsweb.FS
	if dir := os.Getenv("RAFFLE_WEB_DIR"); dir != "" {
		assets = os.DirFS(dir)
		slog.Info("loading web assets from disk", "dir", dir)
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgresql://user:pass@localhost:5432/raffle?sslmode=disable"
	}

	conn, err := db.Open(databaseURL)
	if err != nil {
		slog.Error("connect to database", "err", err)
		os.Exit(1)
	}
	defer conn.Close()

	if err := db.Migrate(conn); err != nil {
		slog.Error("run migrations", "err", err)
		os.Exit(1)
	}

	store := db.NewStore(conn)
	engine := raffle.NewEngine(store)
	hub := ws.NewHub()
	go hub.Run()

	tmplFS, err := fs.Sub(assets, "templates")
	if err != nil {
		slog.Error("templates not found in assets", "err", err)
		os.Exit(1)
	}
	tmpl, err := handler.LoadTemplates(tmplFS)
	if err != nil {
		slog.Error("load templates", "err", err)
		os.Exit(1)
	}

	srv := handler.NewServer(store, engine, hub, tmpl, assets)

	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8543"
	}

	httpServer := &http.Server{
		Addr:              listenAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("raffle listening", "addr", listenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "err", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	slog.Info("shutdown complete")
}
