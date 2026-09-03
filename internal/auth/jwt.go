// Package auth issues and checks the short-lived session tokens the Mini App
// carries after its Telegram initData has been validated once.
package auth

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidToken = errors.New("auth: session token is not valid")

type Claims struct {
	UserID int64
	TgID   int64
}

type Issuer struct {
	secret []byte
	ttl    time.Duration
}

func NewIssuer(secret []byte, ttl time.Duration) *Issuer {
	return &Issuer{secret: secret, ttl: ttl}
}

func (i *Issuer) Issue(c Claims, now time.Time) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(c.UserID, 10),
		ID:        strconv.FormatInt(c.TgID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
	})
	signed, err := token.SignedString(i.secret)
	if err != nil {
		return "", fmt.Errorf("auth: signing the session token: %w", err)
	}
	return signed, nil
}

func (i *Issuer) Parse(raw string, now time.Time) (Claims, error) {
	var registered jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(raw, &registered, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: unexpected signing method %v", t.Header["alg"])
		}
		return i.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	userID, err := strconv.ParseInt(registered.Subject, 10, 64)
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	tgID, err := strconv.ParseInt(registered.ID, 10, 64)
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	return Claims{UserID: userID, TgID: tgID}, nil
}
