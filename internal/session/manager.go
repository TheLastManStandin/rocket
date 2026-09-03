package session

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cahisa/racketka/internal/game"
	"github.com/cahisa/racketka/internal/wallet"
)

// GracePeriod keeps a game alive after the last client drops, so a flaky
// connection resumes the same round instead of losing the stake on it.
const GracePeriod = 60 * time.Second

// Manager is the registry of everyone currently connected. A player's game is
// created the first time they connect and torn down once they have been gone
// for the grace period.
type Manager struct {
	cfg     game.Config
	wallet  wallet.Wallet
	archive Archive
	log     *slog.Logger
	grace   time.Duration

	mu       sync.Mutex
	sessions map[int64]*entry
}

type entry struct {
	session *Session
	cancel  context.CancelFunc
	viewers int
	reaper  *time.Timer
}

// NewManager builds the registry. archive may be nil, in which case rounds are
// neither restored nor kept.
func NewManager(cfg game.Config, w wallet.Wallet, a Archive, log *slog.Logger) *Manager {
	return &Manager{
		cfg:      cfg,
		wallet:   w,
		archive:  a,
		log:      log,
		grace:    GracePeriod,
		sessions: map[int64]*entry{},
	}
}

// Acquire returns the player's game, starting it on their first connection.
// Every Acquire must be paired with a Release.
func (m *Manager) Acquire(userID int64) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, running := m.sessions[userID]
	if !running {
		ctx, cancel := context.WithCancel(context.Background())
		s := newSession(userID, m.cfg, m.wallet, m.archive, m.log)
		e = &entry{session: s, cancel: cancel}
		m.sessions[userID] = e
		go func() {
			s.run(ctx)
			m.forget(userID, e)
		}()
	}

	// Coming back inside the grace period cancels the pending teardown.
	if e.reaper != nil {
		e.reaper.Stop()
		e.reaper = nil
	}
	e.viewers++
	return e.session
}

// Release drops one connection. The game keeps running until the grace period
// runs out, so a reconnect lands back in the same round.
func (m *Manager) Release(userID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, running := m.sessions[userID]
	if !running {
		return
	}
	if e.viewers--; e.viewers > 0 {
		return
	}
	e.reaper = time.AfterFunc(m.grace, func() {
		m.mu.Lock()
		stillIdle := e.viewers == 0 && m.sessions[userID] == e
		m.mu.Unlock()
		if stillIdle {
			e.cancel()
		}
	})
}

func (m *Manager) forget(userID int64, e *entry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[userID] == e {
		delete(m.sessions, userID)
	}
}

// Online is how many players currently have a game running.
func (m *Manager) Online() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// Shutdown stops every running game.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	entries := make([]*entry, 0, len(m.sessions))
	for _, e := range m.sessions {
		entries = append(entries, e)
	}
	m.mu.Unlock()

	for _, e := range entries {
		if e.reaper != nil {
			e.reaper.Stop()
		}
		e.cancel()
	}
}
