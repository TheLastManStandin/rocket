package game

import (
	"errors"
	"math/rand/v2"
	"time"
)

type Phase string

const (
	PhaseBetting Phase = "betting"
	PhaseFlying  Phase = "flying"
	PhaseCrashed Phase = "crashed"
)

// Event names on the wire.
const (
	EventState        = "state"
	EventRoundOpened  = "round_opened"
	EventTookOff      = "took_off"
	EventTick         = "tick"
	EventBotJoined    = "bot_joined"
	EventBotCashedOut = "bot_cashed_out"
	EventCrashed      = "crashed"
	EventBetPlaced    = "bet_placed"
	EventBetQueued    = "bet_queued"
	EventCashedOut    = "cashed_out"
	EventBalance      = "balance"
	EventError        = "error"
)

var (
	ErrBetsClosed       = errors.New("game: bets are closed for this round")
	ErrAlreadyBet       = errors.New("game: a bet is already down for this round")
	ErrAlreadyQueued    = errors.New("game: a bet is already waiting for the next round")
	ErrStakeOutOfRange  = errors.New("game: stake is outside the allowed range")
	ErrNotFlying        = errors.New("game: the rocket is not in flight")
	ErrNoBet            = errors.New("game: no bet to cash out")
	ErrAlreadyCashedOut = errors.New("game: already cashed out")
)

// HistoryLen is how many bursts the strip keeps.
const HistoryLen = 10

type Config struct {
	HouseEdge     float64
	MaxCrash      float64
	MinBet        int64
	MaxBet        int64
	BettingWindow time.Duration
	CrashedPause  time.Duration

	// BotNames and BotAvatars are what the crowd is dealt from, loaded off
	// disk at start-up. Empty names fall back to the built-in list; empty
	// avatars leave every bot with the letter on a coloured disc.
	BotNames   []string
	BotAvatars []string

	// DrawCrash decides where a round bursts. Nil in production, where the
	// secure draw is used; tests pin it to make a round reproducible.
	DrawCrash func() Multiplier
}

func (c Config) withDefaults() Config {
	if c.BettingWindow <= 0 {
		c.BettingWindow = 4 * time.Second
	}
	if c.CrashedPause <= 0 {
		// The crash clip on the client runs a fixed 3s; the extra 1.5s here is
		// what's left over for the coefficient to stand on its own once it does.
		c.CrashedPause = 4500 * time.Millisecond
	}
	if c.MaxCrash <= 0 {
		c.MaxCrash = 1000
	}
	if c.DrawCrash == nil {
		edge, ceiling := c.HouseEdge, c.MaxCrash
		c.DrawCrash = func() Multiplier { return DrawCrashPoint(edge, ceiling) }
	}
	return c
}

// Player is a human at the table, in the terms the table is drawn in. It rides
// along with their stake so everyone else can see who is in the round.
type Player struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	PhotoURL string `json:"photoUrl,omitempty"`
	Hue      int    `json:"hue"`
	Initial  string `json:"initial"`
}

// NewPlayer fills in what the table draws from what the account carries. The
// avatar colour and letter are worked out the same way a bot's are, so a
// player without a photo sits in the list looking like everyone else.
func NewPlayer(id int64, name, photoURL string) Player {
	return Player{
		ID:       id,
		Name:     name,
		PhotoURL: photoURL,
		Hue:      hueFor(name),
		Initial:  initialFor(name),
	}
}

// PlayerBet is one human's stake on a round.
type PlayerBet struct {
	Player      Player
	Amount      int64
	CashedOutAt Multiplier // 0 while still riding
	Payout      int64
}

// View is the stake as the rest of the table is allowed to see it.
func (b *PlayerBet) View() PlayerView {
	return PlayerView{
		Player:      b.Player,
		Bet:         b.Amount,
		CashedOutAt: b.CashedOutAt,
		Payout:      b.Payout,
	}
}

// PlayerView is a human's stake on the wire, shaped like a bot's so the client
// can lay the two out in one list.
type PlayerView struct {
	Player
	Bet         int64      `json:"bet"`
	CashedOutAt Multiplier `json:"cashedOutAt,omitempty"`
	Payout      int64      `json:"payout,omitempty"`
}

func (b *PlayerBet) cashedOut() bool { return b != nil && b.CashedOutAt != 0 }

// Placement says which round a stake landed on.
type Placement int

const (
	PlacedThisRound Placement = iota
	QueuedForNext
)

type Round struct {
	ID        int64
	Crash     Multiplier
	Bots      []*Bot
	Phase     Phase
	OpenedAt  time.Time
	PhaseEnds time.Time
	TookOffAt time.Time

	// Bets is everyone in the round, in the order they got in. A slice rather
	// than a map: the table is drawn in arrival order, and a handful of names
	// is not worth an index.
	Bets []*PlayerBet
}

// Game is the crash table: one rocket, one crash point, one crowd of bots, and
// everyone who staked on the round in progress. There is only ever one of
// these.
//
// It is not safe for concurrent use; Session owns the goroutine that drives it.
type Game struct {
	cfg    Config
	rnd    *rand.Rand
	nextID int64
	round  Round

	// queued are stakes taken while the round on screen was already in the air.
	// They are paid for the moment they are taken, and openRound seats them --
	// which is why they live on the game rather than on the round they were
	// made from.
	queued  []*PlayerBet
	history []Multiplier
}

func New(cfg Config, rnd *rand.Rand, now time.Time) *Game {
	g := &Game{cfg: cfg.withDefaults(), rnd: rnd}
	g.openRound(now)
	return g
}

func (g *Game) Phase() Phase   { return g.round.Phase }
func (g *Game) RoundID() int64 { return g.round.ID }

// Bet is the player's stake on the round in progress, nil when they are not in
// it.
func (g *Game) Bet(userID int64) *PlayerBet { return find(g.round.Bets, userID) }

// QueuedBet is the player's stake waiting for the next round, nil when they
// have none waiting.
func (g *Game) QueuedBet(userID int64) *PlayerBet { return find(g.queued, userID) }

func find(bets []*PlayerBet, userID int64) *PlayerBet {
	for _, b := range bets {
		if b.Player.ID == userID {
			return b
		}
	}
	return nil
}

// Current is what the curve reads right now.
func (g *Game) Current(now time.Time) Multiplier {
	switch g.round.Phase {
	case PhaseFlying:
		if m := MultiplierAt(now.Sub(g.round.TookOffAt)); m < g.round.Crash {
			return m
		}
		return g.round.Crash
	case PhaseCrashed:
		return g.round.Crash
	default:
		return One
	}
}

// Advance moves the round on to where it should be at now and reports what
// happened. It never touches the wallet or the database, so the driving loop
// stays free of I/O and tests can step it by hand.
func (g *Game) Advance(now time.Time) []Event {
	var out []Event
	// betting -> flying -> crashed -> betting is the longest chain a single
	// call can walk, so a small bound rules out spinning on a stuck clock.
	for range 4 {
		events, moved := g.step(now)
		out = append(out, events...)
		if !moved {
			break
		}
	}
	return out
}

func (g *Game) step(now time.Time) ([]Event, bool) {
	switch g.round.Phase {
	// Every boundary is anchored to when it was scheduled, never to when the
	// loop got round to noticing. A ticker always fires a little late, and
	// taking that lateness as the truth stretches each round a few milliseconds
	// past the countdown the client was handed -- and after a real stall it
	// would launch a rocket that has flown for no time at all and so can never
	// burst.
	case PhaseBetting:
		if now.Before(g.round.PhaseEnds) {
			return g.admitBots(now.Sub(g.round.OpenedAt), false), false
		}
		// Anyone still queued is seated before the rocket leaves: nobody may
		// arrive once betting has shut.
		return append(g.admitBots(0, true), g.takeOff(g.round.PhaseEnds)...), true

	case PhaseFlying:
		current := MultiplierAt(now.Sub(g.round.TookOffAt))
		if current >= g.round.Crash {
			return g.burst(g.round.TookOffAt.Add(TimeToReach(g.round.Crash))), true
		}
		return append(g.revealBots(current), Event{Type: EventTick, Multiplier: current}), false

	case PhaseCrashed:
		if now.Before(g.round.PhaseEnds) {
			return nil, false
		}
		return g.openRound(g.round.PhaseEnds), true
	}
	return nil, false
}

// openRound starts taking bets at the instant the previous pause ran out.
func (g *Game) openRound(now time.Time) []Event {
	g.nextID++
	crash := g.cfg.DrawCrash()
	g.round = Round{
		ID:        g.nextID,
		Crash:     crash,
		Bots:      NewBots(crash, g.cfg.BettingWindow, g.rnd, g.cfg.BotNames, g.cfg.BotAvatars),
		Phase:     PhaseBetting,
		OpenedAt:  now,
		PhaseEnds: now.Add(g.cfg.BettingWindow),
	}
	// The table opens empty and fills over the countdown; botViews is therefore
	// empty here by design.
	out := []Event{{
		Type:     EventRoundOpened,
		RoundID:  g.round.ID,
		Phase:    PhaseBetting,
		EndsInMS: g.cfg.BettingWindow.Milliseconds(),
	}}

	// Stakes taken while the last round was in the air come down here. They
	// were paid for when they were taken, so seating them is bookkeeping and
	// not a wallet call -- which is what keeps Advance free of I/O. The events
	// carry no balance for the same reason: nothing moved.
	for _, bet := range g.queued {
		g.round.Bets = append(g.round.Bets, bet)
		player := bet.Player
		out = append(out, Event{
			Type:    EventBetPlaced,
			RoundID: g.round.ID,
			Player:  &player,
			Amount:  bet.Amount,
		})
	}
	g.queued = nil
	return out
}

// admitBots seats everyone whose arrival time has come. Passing all seats the
// whole remaining queue regardless.
func (g *Game) admitBots(elapsed time.Duration, all bool) []Event {
	var arrived []BotView
	for _, b := range g.round.Bots {
		if b.joined || (!all && b.JoinsAt > elapsed) {
			continue
		}
		b.joined = true
		arrived = append(arrived, BotView{Bot: b})
	}
	if len(arrived) == 0 {
		return nil
	}
	return []Event{{Type: EventBotJoined, Bots: arrived}}
}

// takeOff launches the round at the instant betting closed.
func (g *Game) takeOff(at time.Time) []Event {
	g.round.Phase = PhaseFlying
	g.round.TookOffAt = at
	return []Event{{Type: EventTookOff, RoundID: g.round.ID, Multiplier: One}}
}

// revealBots emits the bots whose exit the curve has just passed.
func (g *Game) revealBots(current Multiplier) []Event {
	var out []Event
	for _, b := range g.round.Bots {
		if b.revealed || !b.joined || !b.Won() || b.CashOutAt > current {
			continue
		}
		b.revealed = true
		out = append(out, Event{
			Type:       EventBotCashedOut,
			BotID:      b.ID,
			Multiplier: b.CashOutAt,
			Payout:     b.Payout(),
		})
	}
	return out
}

// burst ends the round at the instant the curve reached its crash point.
func (g *Game) burst(at time.Time) []Event {
	g.round.Phase = PhaseCrashed
	g.round.PhaseEnds = at.Add(g.cfg.CrashedPause)

	g.history = append([]Multiplier{g.round.Crash}, g.history...)
	if len(g.history) > HistoryLen {
		g.history = g.history[:HistoryLen]
	}

	return []Event{{
		Type:       EventCrashed,
		RoundID:    g.round.ID,
		Multiplier: g.round.Crash,
		History:    g.History(),
		Settled:    g.round.Bets,
	}}
}

// PlaceBet records a stake, on this round if it is still taking them and on the
// next one otherwise. The Placement says which happened, because a stake that
// will not ride until the next round has to read as waiting rather than as
// live.
//
// The caller debits the wallet before calling and refunds when this returns an
// error: money is authoritative in the ledger, and the round may have taken off
// between the balance check and here.
func (g *Game) PlaceBet(p Player, amount int64) (Placement, error) {
	if amount < g.cfg.MinBet || amount > g.cfg.MaxBet {
		return 0, ErrStakeOutOfRange
	}

	// Bets are shut for the round on screen, but there is a next one coming and
	// no reason to turn the stake away until it opens.
	if g.round.Phase != PhaseBetting {
		if find(g.queued, p.ID) != nil {
			return 0, ErrAlreadyQueued
		}
		g.queued = append(g.queued, &PlayerBet{Player: p, Amount: amount})
		return QueuedForNext, nil
	}

	if find(g.round.Bets, p.ID) != nil {
		return 0, ErrAlreadyBet
	}
	g.round.Bets = append(g.round.Bets, &PlayerBet{Player: p, Amount: amount})
	return PlacedThisRound, nil
}

// CashOut settles the player's stake at whatever the curve reads now, which is
// the server's clock and never a value the client sent. The caller credits the
// returned payout.
func (g *Game) CashOut(userID int64, now time.Time) (Multiplier, int64, error) {
	bet := find(g.round.Bets, userID)
	switch {
	case g.round.Phase != PhaseFlying:
		return 0, 0, ErrNotFlying
	case bet == nil:
		return 0, 0, ErrNoBet
	case bet.cashedOut():
		return 0, 0, ErrAlreadyCashedOut
	}

	current := MultiplierAt(now.Sub(g.round.TookOffAt))
	if current >= g.round.Crash {
		// The request arrived after the burst instant, whatever the client was
		// still rendering. Let Advance settle it as a loss.
		return 0, 0, ErrNotFlying
	}

	bet.CashedOutAt = current
	bet.Payout = current.Payout(bet.Amount)
	return current, bet.Payout, nil
}

// SeedHistory restores the strip of recent bursts from the player's earlier
// sessions, so the chips are already filled in when they open the app.
func (g *Game) SeedHistory(past []Multiplier) {
	if len(past) > HistoryLen {
		past = past[:HistoryLen]
	}
	g.history = append(g.history[:0], past...)
}

func (g *Game) History() []Multiplier {
	out := make([]Multiplier, len(g.history))
	copy(out, g.history)
	return out
}

// Snapshot is the full picture one client needs on connect or reconnect. It is
// cut for a particular viewer: the table is everyone's, the stake fields on it
// are theirs.
func (g *Game) Snapshot(userID int64, now time.Time) Event {
	e := Event{
		Type:       EventState,
		RoundID:    g.round.ID,
		Phase:      g.round.Phase,
		Multiplier: g.Current(now),
		Bots:       g.botViews(),
		Players:    g.playerViews(),
		History:    g.History(),
	}
	if remaining := g.round.PhaseEnds.Sub(now); remaining > 0 && g.round.Phase != PhaseFlying {
		e.EndsInMS = remaining.Milliseconds()
	}
	if b := find(g.round.Bets, userID); b != nil {
		e.Amount = b.Amount
		if b.cashedOut() {
			e.Multiplier, e.Payout = b.CashedOutAt, b.Payout
		}
	}
	if q := find(g.queued, userID); q != nil {
		e.QueuedAmount = q.Amount
	}
	return e
}

// playerViews is everyone in the round as the table draws them.
func (g *Game) playerViews() []PlayerView {
	out := make([]PlayerView, 0, len(g.round.Bets))
	for _, b := range g.round.Bets {
		out = append(out, b.View())
	}
	return out
}

// BotView is a bot as the client is allowed to see it: the exit multiplier
// appears only once the curve has actually passed it.
type BotView struct {
	*Bot
	CashedOutAt Multiplier `json:"cashedOutAt,omitempty"`
	Payout      int64      `json:"payout,omitempty"`
}

// botViews lists the bots the client already knows about: those still queued
// have not arrived yet and must not appear early.
func (g *Game) botViews() []BotView {
	out := make([]BotView, 0, len(g.round.Bots))
	for _, b := range g.round.Bots {
		if !b.joined {
			continue
		}
		v := BotView{Bot: b}
		if b.revealed {
			v.CashedOutAt, v.Payout = b.CashOutAt, b.Payout()
		}
		out = append(out, v)
	}
	return out
}

// Event is one message on the wire. Fields stay omitempty so each message
// carries only what it means.
type Event struct {
	Type       string     `json:"type"`
	RoundID    int64      `json:"roundId,omitempty"`
	Phase      Phase      `json:"phase,omitempty"`
	EndsInMS   int64      `json:"endsInMs,omitempty"`
	Multiplier Multiplier `json:"multiplier,omitempty"`
	Bots       []BotView  `json:"bots,omitempty"`
	BotID      string     `json:"botId,omitempty"`

	// Player is whose stake an event is about. Bets and cash-outs go out to
	// the whole table, so every one of them has to say who it belongs to.
	Player  *Player      `json:"player,omitempty"`
	Players []PlayerView `json:"players,omitempty"`
	Amount  int64        `json:"amount,omitempty"`
	Payout  int64        `json:"payout,omitempty"`
	Balance int64        `json:"balance,omitempty"`
	History []Multiplier `json:"history,omitempty"`

	// QueuedAmount rides on a snapshot so a client that reconnects between
	// rounds still finds the stake it left waiting.
	QueuedAmount int64 `json:"queuedAmount,omitempty"`

	// Code is a stable identifier for a refusal, so the client owns the
	// wording; Message is a fallback for anything unmapped.
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`

	// Settled rides along with a burst so the archive can file the stakes as
	// they stood. Never serialised: every client already knows its own bet and
	// has watched the rest of the table, and by the time a caller reads Bet()
	// the next round may have opened.
	Settled []*PlayerBet `json:"-"`
}
