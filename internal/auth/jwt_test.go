package auth

import (
	"errors"
	"testing"
	"time"
)

var now = time.Unix(1700000000, 0).UTC()

func TestIssuedTokenRoundTrips(t *testing.T) {
	issuer := NewIssuer([]byte("a-test-secret"), time.Hour)
	want := Claims{UserID: 17, TgID: 221346456}

	token, err := issuer.Issue(want, now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := issuer.Parse(token, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Parse returned %v", err)
	}
	if got != want {
		t.Errorf("Parse gave %+v, want %+v", got, want)
	}
}

func TestParseRejectsBadTokens(t *testing.T) {
	issuer := NewIssuer([]byte("a-test-secret"), time.Hour)
	token, err := issuer.Issue(Claims{UserID: 17, TgID: 42}, now)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("expired", func(t *testing.T) {
		if _, err := issuer.Parse(token, now.Add(2*time.Hour)); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("Parse of an expired token returned %v, want ErrInvalidToken", err)
		}
	})

	t.Run("another secret", func(t *testing.T) {
		other := NewIssuer([]byte("a-different-secret"), time.Hour)
		if _, err := other.Parse(token, now); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("Parse under another secret returned %v, want ErrInvalidToken", err)
		}
	})

	t.Run("garbage", func(t *testing.T) {
		if _, err := issuer.Parse("not.a.token", now); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("Parse of garbage returned %v, want ErrInvalidToken", err)
		}
	})

	// alg=none is the classic JWT forgery; ParseWithClaims must refuse it.
	t.Run("unsigned", func(t *testing.T) {
		const unsigned = "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." +
			"eyJzdWIiOiI5OTkiLCJqdGkiOiI5OTkiLCJleHAiOjk5OTk5OTk5OTl9."
		if _, err := issuer.Parse(unsigned, now); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("Parse of an unsigned token returned %v, want ErrInvalidToken", err)
		}
	})
}
