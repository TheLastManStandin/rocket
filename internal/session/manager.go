package session

import (
	"context"
	"log/slog"
	"sync"

	"github.com/cahisa/racketka/internal/game"
	"github.com/cahisa/racketka/internal/wallet"
)

// Manager owns the one table. It deals from the moment the server is up until
// the moment it goes down, with or without anybody watching: a player opening
// the app joins a round already in progress rather than starting one, and the
// history strip they are handed is of rounds that really happened.
type Manager struct {
	session *Session
	cancel  context.CancelFunc

	mu      sync.Mutex
	viewers int
}

// NewManager starts the table. archive may be nil, in which case rounds are
// neither restored nor kept. Every Manager must be Shutdown.
func NewManager(cfg game.Config, w wallet.Wallet, a Archive, log *slog.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	s := newSession(cfg, w, a, log)
	go s.run(ctx)
	return &Manager{session: s, cancel: cancel}
}

// Acquire returns the table and counts one more pair of eyes on it. Every
// Acquire must be paired with a Release.
func (m *Manager) Acquire() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.viewers++
	return m.session
}

// Release drops one connection. The table deals on regardless, so a reconnect
// lands in whatever round is running by then.
func (m *Manager) Release() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.viewers > 0 {
		m.viewers--
	}
}

// NudgeBalance tells a player who is currently connected that their balance
// moved outside the game. Nobody being connected is the ordinary case, not an
// error: the balance is read fresh at sign-in anyway.
func (m *Manager) NudgeBalance(userID, balance int64) {
	m.session.PushBalance(userID, balance)
}

// Online is how many connections the table is currently serving. Nobody
// watching is an ordinary state of affairs, not a stopped table.
func (m *Manager) Online() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.viewers
}

// Shutdown stops the table. Only the server going down does this.
func (m *Manager) Shutdown() { m.cancel() }
