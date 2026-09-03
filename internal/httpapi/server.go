// Package httpapi wires the auth endpoint, the websocket and the built
// frontend onto one origin, which is all a Telegram Mini App can point at.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cahisa/racketka/internal/auth"
	"github.com/cahisa/racketka/internal/config"
	"github.com/cahisa/racketka/internal/session"
	"github.com/cahisa/racketka/internal/storage"
	"github.com/cahisa/racketka/internal/telegram"
	"github.com/cahisa/racketka/internal/wallet"
	"github.com/cahisa/racketka/internal/ws"
)

// devTgID stands in for a real Telegram account when DEV_MODE lets the app run
// in a plain browser.
const devTgID int64 = 1

const maxAuthBody = 8 << 10

type Deps struct {
	Config  *config.Config
	Store   *storage.Store
	Wallet  wallet.Wallet
	Manager *session.Manager
	Issuer  *auth.Issuer
	Logger  *slog.Logger
	WebRoot string
}

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/auth/telegram", d.handleTelegramAuth)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "online": d.Manager.Online()})
	})
	mux.Handle("GET /ws", ws.Handler(ws.Deps{
		Manager: d.Manager,
		Issuer:  d.Issuer,
		Wallet:  d.Wallet,
		Logger:  d.Logger,
	}))
	mux.Handle("/", spa(d.WebRoot))

	if d.Config.DevMode {
		// Vite serves the frontend on its own port while developing; in
		// production everything is one origin and this is never installed.
		return devCORS(mux)
	}
	return mux
}

type authRequest struct {
	InitData string `json:"initData"`
}

type authResponse struct {
	Token   string `json:"token"`
	Balance int64  `json:"balance"`
	Limits  struct {
		MinBet int64 `json:"minBet"`
		MaxBet int64 `json:"maxBet"`
	} `json:"limits"`
	User struct {
		ID        int64  `json:"id"`
		Username  string `json:"username"`
		FirstName string `json:"firstName"`
		PhotoURL  string `json:"photoUrl"`
	} `json:"user"`
}

func (d Deps) handleTelegramAuth(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBody)

	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed request"})
		return
	}

	profile, err := d.resolveProfile(req.InitData)
	if err != nil {
		d.Logger.Warn("rejected a sign-in", "error", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "telegram authorisation failed"})
		return
	}

	user, err := d.Store.UpsertUser(r.Context(), profile, d.Config.StartBalance)
	if err != nil {
		d.Logger.Error("could not upsert the player", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	token, err := d.Issuer.Issue(auth.Claims{UserID: user.ID, TgID: user.TgID}, time.Now())
	if err != nil {
		d.Logger.Error("could not issue a session token", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	var resp authResponse
	resp.Token = token
	resp.Balance = user.Balance
	resp.Limits.MinBet = d.Config.MinBet
	resp.Limits.MaxBet = d.Config.MaxBet
	resp.User.ID = user.TgID
	resp.User.Username = user.Username
	resp.User.FirstName = user.FirstName
	resp.User.PhotoURL = user.PhotoURL
	writeJSON(w, http.StatusOK, resp)
}

// resolveProfile turns initData into a player, or refuses. The signature is the
// only thing that establishes who someone is.
func (d Deps) resolveProfile(initData string) (storage.Profile, error) {
	if initData == "" {
		if !d.Config.DevMode {
			return storage.Profile{}, errors.New("httpapi: initData is required")
		}
		return storage.Profile{TgID: devTgID, Username: "dev", FirstName: "Разработчик"}, nil
	}

	data, err := telegram.Validate(initData, d.Config.BotToken, d.Config.InitDataTTL, time.Now())
	if err != nil {
		return storage.Profile{}, err
	}
	return storage.Profile{
		TgID:      data.User.ID,
		Username:  data.User.Username,
		FirstName: data.User.FirstName,
		PhotoURL:  data.User.PhotoURL,
	}, nil
}

// spa serves the built frontend, falling back to index.html so client-side
// routes survive a reload.
func spa(root string) http.Handler {
	files := http.FileServer(http.Dir(root))
	index := filepath.Join(root, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean before joining: this is what keeps ../ out of the served path.
		target := filepath.Join(root, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		if _, err := os.Stat(index); err != nil {
			http.Error(w, "frontend is not built yet: run npm run build in web/", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, index)
	})
}

func devCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
