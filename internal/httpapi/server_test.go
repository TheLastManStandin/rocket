package httpapi_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cahisa/racketka/internal/auth"
	"github.com/cahisa/racketka/internal/config"
	"github.com/cahisa/racketka/internal/game"
	"github.com/cahisa/racketka/internal/httpapi"
	"github.com/cahisa/racketka/internal/session"
	"github.com/cahisa/racketka/internal/storage"
)

const botToken = "8000000000:AA-fake-token-for-tests"

type fakeWallet struct{}

func (fakeWallet) Balance(context.Context, int64) (int64, error) { return 1000, nil }
func (fakeWallet) Debit(context.Context, int64, int64, string, string) (int64, error) {
	return 1000, nil
}
func (fakeWallet) Credit(context.Context, int64, int64, string, string) (int64, error) {
	return 1000, nil
}

// signInitData produces the payload a Telegram client would send.
func signInitData(t *testing.T, token string, v url.Values) string {
	t.Helper()

	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+v.Get(k))
	}

	mac := func(key, data []byte) []byte {
		m := hmac.New(sha256.New, key)
		m.Write(data)
		return m.Sum(nil)
	}
	secret := mac([]byte("WebAppData"), []byte(token))

	v.Set("hash", hex.EncodeToString(mac(secret, []byte(strings.Join(parts, "\n")))))
	return v.Encode()
}

func freshInitData(t *testing.T, tgID int64, token string) string {
	t.Helper()
	return signInitData(t, token, url.Values{
		"user":      {`{"id":` + strconv.FormatInt(tgID, 10) + `,"first_name":"Тест","username":"api_test"}`},
		"auth_date": {strconv.FormatInt(time.Now().Unix(), 10)},
	})
}

// newServer wires the real router against a real database, skipping when there
// is none to reach.
func newServer(t *testing.T, devMode bool) (*httptest.Server, *auth.Issuer) {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://racketka:racketka@localhost:5433/racketka?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	store, err := storage.Open(ctx, dsn)
	if err != nil {
		t.Skipf("no database at %s (%v)", dsn, err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	t.Cleanup(store.Close)

	// A directory standing in for the built frontend.
	webRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("<!doctype html>ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		BotToken:     botToken,
		JWTSecret:    []byte("test-secret"),
		JWTTTL:       time.Hour,
		InitDataTTL:  24 * time.Hour,
		DevMode:      devMode,
		StartBalance: 1000,
	}
	issuer := auth.NewIssuer(cfg.JWTSecret, cfg.JWTTTL)

	manager := session.NewManager(game.Config{MinBet: 10, MaxBet: 10000}, fakeWallet{}, nil, nil)
	t.Cleanup(manager.Shutdown)

	srv := httptest.NewServer(httpapi.NewRouter(httpapi.Deps{
		Config:  cfg,
		Store:   store,
		Wallet:  fakeWallet{},
		Manager: manager,
		Issuer:  issuer,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		WebRoot: webRoot,
	}))
	t.Cleanup(srv.Close)
	return srv, issuer
}

func signIn(t *testing.T, srv *httptest.Server, initData string) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string]string{"initData": initData})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Post(srv.URL+"/api/auth/telegram", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestSignInAcceptsGenuineInitData(t *testing.T) {
	srv, issuer := newServer(t, false)

	const tgID = -424242
	resp := signIn(t, srv, freshInitData(t, tgID, botToken))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status is %d, want 200", resp.StatusCode)
	}

	var out struct {
		Token   string `json:"token"`
		Balance int64  `json:"balance"`
		User    struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}

	if out.User.ID != tgID {
		t.Errorf("signed in as %d, want %d", out.User.ID, tgID)
	}
	if out.User.Username != "api_test" {
		t.Errorf("username is %q, want %q", out.User.Username, "api_test")
	}
	if out.Balance != 1000 {
		t.Errorf("opening balance is %d, want 1000", out.Balance)
	}

	// The token has to be one this server will accept back.
	claims, err := issuer.Parse(out.Token, time.Now())
	if err != nil {
		t.Fatalf("the issued token did not parse: %v", err)
	}
	if claims.TgID != tgID {
		t.Errorf("token carries tg id %d, want %d", claims.TgID, tgID)
	}
}

// The sign-in has to hand back the id the player will wear at the table, and
// that is the row id, not the Telegram one. Every bet_placed and cashed_out
// the socket broadcasts carries the row id, so a client holding the Telegram
// id never recognises its own stake among them -- and a stake it cannot see is
// a stake it will not offer to cash out.
func TestSignInHandsBackTheIDTheTableSeatsYouUnder(t *testing.T) {
	srv, issuer := newServer(t, false)

	const tgID = -424243
	resp := signIn(t, srv, freshInitData(t, tgID, botToken))
	defer resp.Body.Close()

	var out struct {
		Token    string `json:"token"`
		PlayerID int64  `json:"playerId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}

	claims, err := issuer.Parse(out.Token, time.Now())
	if err != nil {
		t.Fatalf("the issued token did not parse: %v", err)
	}
	if out.PlayerID != claims.UserID {
		t.Errorf("signed in as player %d, but the socket seats %d", out.PlayerID, claims.UserID)
	}
}

func TestSignInRefusesAnythingUnsigned(t *testing.T) {
	srv, _ := newServer(t, false)

	tests := map[string]string{
		"empty outside dev mode": "",
		"not a query string":     "%%%",
		"unsigned fields":        "user=%7B%22id%22%3A7%7D&auth_date=1788368177",
		"another bot's token":    freshInitData(t, -9, "9999999999:AA-some-other-bot"),
	}

	for name, initData := range tests {
		t.Run(name, func(t *testing.T) {
			resp := signIn(t, srv, initData)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status is %d, want 401", resp.StatusCode)
			}
		})
	}
}

// A tampered user is the forgery the signature exists to stop.
func TestSignInRefusesATamperedUser(t *testing.T) {
	srv, _ := newServer(t, false)

	raw := freshInitData(t, -4242, botToken)
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	v.Set("user", `{"id":1,"first_name":"Админ"}`)

	resp := signIn(t, srv, v.Encode())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status is %d, want 401", resp.StatusCode)
	}
}

// Dev mode is the only way in without Telegram, and only when it is on.
func TestDevModeLetsAnEmptyPayloadThrough(t *testing.T) {
	srv, _ := newServer(t, true)

	resp := signIn(t, srv, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status is %d, want 200 with DEV_MODE on", resp.StatusCode)
	}
}

func TestFrontendIsServedWithASinglePageFallback(t *testing.T) {
	srv, _ := newServer(t, false)

	for _, path := range []string{"/", "/roulette/giftcrash", "/anything/at/all"} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s gave %d, want 200", path, resp.StatusCode)
		}
		if !strings.Contains(string(body), "<!doctype html>") {
			t.Errorf("GET %s did not fall back to index.html", path)
		}
	}
}

// The join must never let a request climb out of the web root.
func TestFrontendRefusesToClimbOutOfItsRoot(t *testing.T) {
	srv, _ := newServer(t, false)

	// Sent raw so net/http does not tidy the dot segments away for us.
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.URL.Opaque = "//" + req.URL.Host + "/../../../../etc/passwd"

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if strings.Contains(string(body), "root:") {
		t.Fatal("the static handler served a file from outside the web root")
	}
}

func TestHealthReportsWhoIsOnline(t *testing.T) {
	srv, _ := newServer(t, false)

	resp, err := srv.Client().Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out struct {
		OK     bool `json:"ok"`
		Online int  `json:"online"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Error("health reported not ok")
	}
	if out.Online != 0 {
		t.Errorf("online is %d, want 0 with nobody connected", out.Online)
	}
}
