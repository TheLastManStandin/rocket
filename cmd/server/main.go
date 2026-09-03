// Command server runs the Racketka backend: Telegram auth, the crash rounds and
// the built Mini App, all on one origin.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/cahisa/racketka/internal/auth"
	"github.com/cahisa/racketka/internal/config"
	"github.com/cahisa/racketka/internal/game"
	"github.com/cahisa/racketka/internal/httpapi"
	"github.com/cahisa/racketka/internal/session"
	"github.com/cahisa/racketka/internal/storage"
	"github.com/cahisa/racketka/internal/wallet"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(log); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// A missing .env is fine: the environment may already carry everything.
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.DevMode {
		log.Warn("DEV_MODE is on: Telegram signatures are not required and sessions reset on restart")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		return err
	}
	log.Info("schema is up to date")

	purse := wallet.NewPostgres(store.Pool())
	manager := session.NewManager(game.Config{
		HouseEdge: cfg.HouseEdge,
		MaxCrash:  cfg.MaxCrash,
		MinBet:    cfg.MinBet,
		MaxBet:    cfg.MaxBet,
	}, purse, store, log)
	defer manager.Shutdown()

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.NewRouter(httpapi.Deps{
			Config:  cfg,
			Store:   store,
			Wallet:  purse,
			Manager: manager,
			Issuer:  auth.NewIssuer(cfg.JWTSecret, cfg.JWTTTL),
			Logger:  log,
			WebRoot: "web/dist",
		}),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout on purpose: it would cut every websocket at the
		// deadline, however healthy the connection is.
	}

	listening := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listening <- err
			return
		}
		listening <- nil
	}()

	select {
	case err := <-listening:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
