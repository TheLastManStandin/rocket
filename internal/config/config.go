// Package config loads every runtime knob from the environment, so the binary
// stays deployable without shipping a config file next to it.
package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr        string
	DatabaseURL string

	BotToken    string
	JWTSecret   []byte
	JWTTTL      time.Duration
	InitDataTTL time.Duration
	DevMode     bool

	StartBalance int64
	HouseEdge    float64
	MaxCrash     float64
	MinBet       int64
	MaxBet       int64
}

func Load() (*Config, error) {
	c := &Config{
		Addr:        env("ADDR", ":8090"),
		DatabaseURL: env("DATABASE_URL", "postgres://racketka:racketka@localhost:5433/racketka?sslmode=disable"),
		BotToken:    env("BOT_TOKEN", ""),
		JWTTTL:      12 * time.Hour,
		InitDataTTL: 24 * time.Hour,
		DevMode:     envBool("DEV_MODE", false),
	}

	var err error
	if c.StartBalance, err = envInt("START_BALANCE", 1000); err != nil {
		return nil, err
	}
	if c.MinBet, err = envInt("MIN_BET", 10); err != nil {
		return nil, err
	}
	if c.MaxBet, err = envInt("MAX_BET", 100000); err != nil {
		return nil, err
	}
	if c.HouseEdge, err = envFloat("HOUSE_EDGE", 0.04); err != nil {
		return nil, err
	}
	if c.MaxCrash, err = envFloat("MAX_CRASH", 1000); err != nil {
		return nil, err
	}

	if secret := env("JWT_SECRET", ""); secret != "" {
		c.JWTSecret = []byte(secret)
	} else {
		// A generated secret invalidates every session on restart, which is
		// tolerable while developing but never in production.
		if !c.DevMode {
			return nil, fmt.Errorf("config: JWT_SECRET is required when DEV_MODE is off")
		}
		c.JWTSecret = make([]byte, 32)
		if _, err := rand.Read(c.JWTSecret); err != nil {
			return nil, fmt.Errorf("config: generating a dev JWT secret: %w", err)
		}
	}

	if c.BotToken == "" && !c.DevMode {
		return nil, fmt.Errorf("config: BOT_TOKEN is required when DEV_MODE is off")
	}
	if c.HouseEdge < 0 || c.HouseEdge >= 1 {
		return nil, fmt.Errorf("config: HOUSE_EDGE must sit in [0,1), got %v", c.HouseEdge)
	}
	if c.MaxCrash < 1 {
		return nil, fmt.Errorf("config: MAX_CRASH must be at least 1, got %v", c.MaxCrash)
	}
	if c.MinBet <= 0 || c.MaxBet < c.MinBet {
		return nil, fmt.Errorf("config: need 0 < MIN_BET <= MAX_BET, got %d and %d", c.MinBet, c.MaxBet)
	}
	return c, nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envInt(key string, fallback int64) (int64, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a whole number: %w", key, err)
	}
	return n, nil
}

func envFloat(key string, fallback float64) (float64, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a number: %w", key, err)
	}
	return f, nil
}
