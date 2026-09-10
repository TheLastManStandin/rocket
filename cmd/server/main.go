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
	"github.com/cahisa/racketka/internal/botpack"
	"github.com/cahisa/racketka/internal/config"
	"github.com/cahisa/racketka/internal/game"
	"github.com/cahisa/racketka/internal/httpapi"
	"github.com/cahisa/racketka/internal/payments"
	"github.com/cahisa/racketka/internal/session"
	"github.com/cahisa/racketka/internal/storage"
	"github.com/cahisa/racketka/internal/telegram"
	"github.com/cahisa/racketka/internal/wallet"
)

// Where the crowd is dealt from, relative to the working directory the server
// is started in -- the same way web/dist is.
const (
	botNamesFile = "assets/bot-names.txt"
	botAvatarDir = "assets/bot-avatars"
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

	// The crowd's names and faces come off disk. Neither is required: without
	// them the bots fall back to the built-in names and to a letter on a
	// coloured disc, which is how they looked before the folder existed.
	crowd, err := botpack.Load(botNamesFile, botAvatarDir, botpack.URLPrefix)
	if err != nil {
		log.Warn("could not read the bot pack", "error", err)
	}
	log.Info("bot pack", "names", len(crowd.Names), "avatars", len(crowd.Avatars))

	purse := wallet.NewPostgres(store.Pool())
	manager := session.NewManager(game.Config{
		HouseEdge:  cfg.HouseEdge,
		MaxCrash:   cfg.MaxCrash,
		MinBet:     cfg.MinBet,
		MaxBet:     cfg.MaxBet,
		BotNames:   crowd.Names,
		BotAvatars: crowd.Avatars,
	}, purse, store, log)
	defer manager.Shutdown()

	// Stars need a bot to sell them. Without a token the game still runs, it
	// just cannot be topped up -- which is exactly the dev-mode case.
	var payer *payments.Service
	if cfg.BotToken != "" {
		payer = payments.New(telegram.NewBot(cfg.BotToken), store, purse, manager, log)
		go payer.Watch(ctx)
		log.Info("watching for Stars payments")
	} else {
		log.Warn("BOT_TOKEN is empty: top-ups are switched off")
	}

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.NewRouter(httpapi.Deps{
			Config:        cfg,
			Store:         store,
			Wallet:        purse,
			Payments:      payer,
			Manager:       manager,
			Issuer:        auth.NewIssuer(cfg.JWTSecret, cfg.JWTTTL),
			Logger:        log,
			WebRoot:       "web/dist",
			BotAvatarRoot: botAvatarDir,
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
