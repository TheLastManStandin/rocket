// Package telegram validates the initData payload a Mini App receives from the
// Telegram client. Everything the game trusts about who a player is starts here.
package telegram

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// User is the subset of Telegram's user object the game persists.
type User struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
	PhotoURL     string `json:"photo_url"`
}

// InitData is a payload whose signature has already been checked.
type InitData struct {
	User     User
	AuthDate time.Time
}

var (
	ErrMissingHash = errors.New("telegram: initData carries no hash")
	ErrBadHash     = errors.New("telegram: initData signature does not match")
	ErrMissingUser = errors.New("telegram: initData carries no user")
	ErrBadAuthDate = errors.New("telegram: initData carries no usable auth_date")
	ErrExpired     = errors.New("telegram: initData is too old")
)

// Validate verifies the HMAC-SHA256 signature Telegram puts on initData and
// returns the embedded user.
//
// The data-check-string is every received field except hash, sorted by key and
// joined with newlines. signature deliberately stays in: Telegram computes it
// before hash, so it is one of the signed fields. Only the Ed25519 check that
// third parties run without a bot token drops both.
//
// Passing ttl <= 0 skips the freshness check; now is injected so tests can
// pin the clock.
func Validate(raw, botToken string, ttl time.Duration, now time.Time) (*InitData, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, err
	}

	wantHex := values.Get("hash")
	if wantHex == "" {
		return nil, ErrMissingHash
	}
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		return nil, ErrBadHash
	}
	values.Del("hash")

	// secret = HMAC(key: "WebAppData", data: bot token)
	// hash   = HMAC(key: secret,       data: data-check-string)
	secret := mac([]byte("WebAppData"), []byte(botToken))
	if !hmac.Equal(mac(secret, []byte(dataCheckString(values))), want) {
		return nil, ErrBadHash
	}

	unix, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil {
		return nil, ErrBadAuthDate
	}
	issued := time.Unix(unix, 0)
	if ttl > 0 && now.Sub(issued) > ttl {
		return nil, ErrExpired
	}

	rawUser := values.Get("user")
	if rawUser == "" {
		return nil, ErrMissingUser
	}
	var u User
	if err := json.Unmarshal([]byte(rawUser), &u); err != nil {
		return nil, err
	}
	if u.ID == 0 {
		return nil, ErrMissingUser
	}

	return &InitData{User: u, AuthDate: issued}, nil
}

func dataCheckString(values url.Values) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(values.Get(k))
	}
	return sb.String()
}

func mac(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}
