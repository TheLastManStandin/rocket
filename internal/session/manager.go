package session

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cahisa/racketka/internal/game"
	"github.com/cahisa/racketka/internal/wallet"
)

// GracePeriod keeps the table alive after the last client drops, so a flaky
// connection resumes the same round instead of losing the stake on it.
const GracePeriod = 60 * time.Second

// Manager owns the one table. It is started by the first player to connect and
// torn down once the last of them has been gone for the grace period -- there
// is no round worth running with nobody watching it, and the next one to
// arrive opens a fresh one.
type Manager struct {
	cfg     game.Config
	wallet  wallet.Wallet
	archive Archive
	log     *slog.Logger
	grace   time.Duration

	mu    sync.Mutex
	table *entry
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
		cfg:     cfg,
		wallet:  w,
		archive: a,
		log:     log,
		grace:   GracePeriod,
	}
}

// Acquire returns the table, starting it for the first player through the
// door. Every Acquire must be paired with a Release.
func (m *Manager) Acquire() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	e := m.table
	if e == nil {
		ctx, cancel := context.WithCancel(context.Background())
		s := newSession(m.cfg, m.wallet, m.archive, m.log)
		e = &entry{session: s, cancel: cancel}
		m.table = e
		go func() {
			s.run(ctx)
			m.forget(e)
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

// Release drops one connection. The table keeps running until the grace period
// runs out with nobody on it, so a reconnect lands back in the same round.
func (m *Manager) Release() {
	m.mu.Lock()
	defer m.mu.Unlock()

	e := m.table
	if e == nil {
		return
	}
	if e.viewers--; e.viewers > 0 {
		return
	}
	e.reaper = time.AfterFunc(m.grace, func() {
		m.mu.Lock()
		stillIdle := e.viewers == 0 && m.table == e
		m.mu.Unlock()
		if stillIdle {
			e.cancel()
		}
	})
}

// NudgeBalance tells a player who is currently connected that their balance
// moved outside the game. Nobody being connected is the ordinary case, not an
// error: the balance is read fresh at sign-in anyway.
func (m *Manager) NudgeBalance(userID, balance int64) {
	m.mu.Lock()
	e := m.table
	m.mu.Unlock()

	if e != nil {
		e.session.PushBalance(userID, balance)
	}
}

func (m *Manager) forget(e *entry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.table == e {
		m.table = nil
	}
}

// Online is how many connections the table is currently serving.
func (m *Manager) Online() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.table == nil {
		return 0
	}
	return m.table.viewers
}

// Running reports whether the table is up at all. It outlives the last
// connection by the grace period, so this is not the same question as Online.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.table != nil
}

// Shutdown stops the table.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	e := m.table
	// reaper is guarded by mu, so it is stopped in here rather than alongside
	// cancel below. Stop never waits on a callback already running, so holding
	// the lock it wants cannot deadlock.
	if e != nil && e.reaper != nil {
		e.reaper.Stop()
	}
	m.mu.Unlock()

	// Cancelling stays outside the lock: tearing the table down ends in
	// forget, which wants the same mutex.
	if e != nil {
		e.cancel()
	}
}
