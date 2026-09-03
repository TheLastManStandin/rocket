package telegram

import (
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"testing"
	"time"
)

const testToken = "8000000000:AA-this-token-is-fake-and-only-used-in-tests"

// sign produces the initData string Telegram would emit for these fields.
func sign(t *testing.T, v url.Values, botToken string) string {
	t.Helper()
	secret := mac([]byte("WebAppData"), []byte(botToken))
	v.Set("hash", hex.EncodeToString(mac(secret, []byte(dataCheckString(v)))))
	return v.Encode()
}

// fields mirrors the shape a real Telegram client sends, signature included.
func fields(authDate time.Time) url.Values {
	return url.Values{
		"user":          {`{"id":4242,"first_name":"Тест","username":"test_player","language_code":"ru"}`},
		"chat_instance": {"1705194651769362106"},
		"chat_type":     {"sender"},
		"auth_date":     {strconv.FormatInt(authDate.Unix(), 10)},
		"signature":     {"bXVzdC1zdGF5LWluc2lkZS10aGUtZGF0YS1jaGVjay1zdHJpbmc"},
	}
}

func TestValidateAcceptsAGenuinePayload(t *testing.T) {
	now := time.Unix(1788368177, 0)
	raw := sign(t, fields(now), testToken)

	got, err := Validate(raw, testToken, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("Validate returned %v, want no error", err)
	}
	if got.User.ID != 4242 {
		t.Errorf("User.ID = %d, want 4242", got.User.ID)
	}
	if got.User.Username != "test_player" {
		t.Errorf("User.Username = %q, want %q", got.User.Username, "test_player")
	}
	if got.User.FirstName != "Тест" {
		t.Errorf("User.FirstName = %q, want %q", got.User.FirstName, "Тест")
	}
	if !got.AuthDate.Equal(now) {
		t.Errorf("AuthDate = %v, want %v", got.AuthDate, now)
	}
}

func TestValidateRejectsATamperedUser(t *testing.T) {
	now := time.Unix(1788368177, 0)
	raw := sign(t, fields(now), testToken)

	// Swap in a different player id while keeping the original hash: this is
	// exactly the forgery the signature exists to stop.
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	v.Set("user", `{"id":9999,"first_name":"Злоумышленник"}`)

	if _, err := Validate(v.Encode(), testToken, 24*time.Hour, now); !errors.Is(err, ErrBadHash) {
		t.Fatalf("Validate returned %v, want ErrBadHash", err)
	}
}

func TestValidateRejectsAnotherBotsToken(t *testing.T) {
	now := time.Unix(1788368177, 0)
	raw := sign(t, fields(now), "9999999999:AA-some-other-bot")

	if _, err := Validate(raw, testToken, 24*time.Hour, now); !errors.Is(err, ErrBadHash) {
		t.Fatalf("Validate returned %v, want ErrBadHash", err)
	}
}

func TestValidateRejectsStalePayload(t *testing.T) {
	issued := time.Unix(1788368177, 0)
	raw := sign(t, fields(issued), testToken)

	if _, err := Validate(raw, testToken, time.Hour, issued.Add(2*time.Hour)); !errors.Is(err, ErrExpired) {
		t.Fatalf("Validate returned %v, want ErrExpired", err)
	}
	// A zero ttl means the caller opted out of the freshness check.
	if _, err := Validate(raw, testToken, 0, issued.Add(365*24*time.Hour)); err != nil {
		t.Fatalf("Validate with ttl=0 returned %v, want no error", err)
	}
}

func TestValidateRejectsMissingPieces(t *testing.T) {
	now := time.Unix(1788368177, 0)

	t.Run("no hash", func(t *testing.T) {
		v := fields(now)
		if _, err := Validate(v.Encode(), testToken, 24*time.Hour, now); !errors.Is(err, ErrMissingHash) {
			t.Fatalf("Validate returned %v, want ErrMissingHash", err)
		}
	})

	t.Run("no user", func(t *testing.T) {
		v := fields(now)
		v.Del("user")
		raw := sign(t, v, testToken)
		if _, err := Validate(raw, testToken, 24*time.Hour, now); !errors.Is(err, ErrMissingUser) {
			t.Fatalf("Validate returned %v, want ErrMissingUser", err)
		}
	})

	t.Run("no auth_date", func(t *testing.T) {
		v := fields(now)
		v.Del("auth_date")
		raw := sign(t, v, testToken)
		if _, err := Validate(raw, testToken, 24*time.Hour, now); !errors.Is(err, ErrBadAuthDate) {
			t.Fatalf("Validate returned %v, want ErrBadAuthDate", err)
		}
	})
}

// Telegram signs the signature field too, so dropping it from the
// data-check-string silently breaks every real client. Guard against that.
func TestValidateCountsSignatureAmongSignedFields(t *testing.T) {
	now := time.Unix(1788368177, 0)
	raw := sign(t, fields(now), testToken)

	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	v.Del("signature")

	if _, err := Validate(v.Encode(), testToken, 24*time.Hour, now); !errors.Is(err, ErrBadHash) {
		t.Fatalf("Validate returned %v after signature was stripped, want ErrBadHash", err)
	}
}
