package game

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Bot is one of the fake players filling the table for a single round.
//
// The table is real -- everyone is on the same round -- but it would be a thin
// one on the strength of the humans alone, so a crowd is seated alongside them.
// Everything about a bot is decided when the round opens, and the engine only
// reveals it on schedule.
type Bot struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Hue     int    `json:"hue"` // avatar is drawn client-side from this
	Initial string `json:"initial"`
	Bet     int64  `json:"bet"`

	// CashOutAt is where this bot leaves, or 0 when it rides the round into the
	// burst. Never serialised: a bot leaving at x5.00 would tell the client the
	// round runs at least that far, which is a read on the crash point.
	CashOutAt Multiplier `json:"-"`

	// JoinsAt is how long after the round opens this bot sits down. The table
	// fills gradually the way a real one would, instead of a dozen strangers
	// appearing in a single frame.
	JoinsAt time.Duration `json:"-"`

	// joined and revealed each flip once, so a bot is announced exactly once on
	// arrival and once on leaving.
	joined   bool
	revealed bool
}

// Won reports whether the bot got out before the burst.
func (b *Bot) Won() bool { return b.CashOutAt != 0 }

// Payout is what the bot walks away with, zero if it burned.
func (b *Bot) Payout() int64 {
	if !b.Won() {
		return 0
	}
	return b.CashOutAt.Payout(b.Bet)
}

const (
	botsMin = 5
	botsMax = 12
)

// NewBots seats a crowd for a round that bursts at crash, spread across window
// so they arrive a few at a time rather than all at once.
func NewBots(crash Multiplier, window time.Duration, rnd *rand.Rand) []*Bot {
	n := botsMin + rnd.IntN(botsMax-botsMin+1)

	bots := make([]*Bot, 0, n)
	for i, name := range pickNames(n, rnd) {
		b := &Bot{
			ID:      fmt.Sprintf("b%d", i+1),
			Name:    name,
			Hue:     hueFor(name),
			Initial: initialFor(name),
			Bet:     drawBotBet(rnd),
			JoinsAt: drawJoinDelay(window, rnd),
		}
		// Strictly below: a bot aiming at the exact burst point is too late,
		// same rule the human player plays under.
		if target := drawBotTarget(rnd); target < crash {
			b.CashOutAt = target
		}
		bots = append(bots, b)
	}

	// Arrival order is the order they will be announced in, so sort once here
	// rather than making the caller reason about it.
	sort.Slice(bots, func(i, j int) bool { return bots[i].JoinsAt < bots[j].JoinsAt })
	return bots
}

// drawJoinDelay picks when a bot sits down. Arrivals bunch towards the start of
// the window -- squaring a uniform draw -- so the table looks alive immediately
// and still fills for the whole countdown.
func drawJoinDelay(window time.Duration, rnd *rand.Rand) time.Duration {
	if window <= 0 {
		return 0
	}
	u := rnd.Float64()
	// Leave the last tenth clear so nobody arrives after betting has shut.
	return time.Duration(u * u * 0.9 * float64(window))
}

// drawBotTarget picks where a bot means to leave. Real tables are mostly
// cautious with a few holdouts, so the bands are weighted towards early exits.
func drawBotTarget(rnd *rand.Rand) Multiplier {
	bands := []struct {
		weight float64
		lo, hi Multiplier
	}{
		{0.45, 110, 160},
		{0.30, 160, 250},
		{0.15, 250, 500},
		{0.07, 500, 1200},
		{0.03, 1200, 5000},
	}

	roll := rnd.Float64()
	for _, b := range bands {
		if roll < b.weight {
			return b.lo + Multiplier(rnd.IntN(int(b.hi-b.lo)))
		}
		roll -= b.weight
	}
	return bands[len(bands)-1].hi
}

// drawBotBet favours round numbers, the way people actually stake.
func drawBotBet(rnd *rand.Rand) int64 {
	round := []int64{100, 200, 250, 500, 500, 750, 1000}
	if rnd.Float64() < 0.7 {
		return round[rnd.IntN(len(round))]
	}
	return 300 + int64(rnd.IntN(2200))
}

func pickNames(n int, rnd *rand.Rand) []string {
	picked := make([]string, 0, n)
	for _, i := range rnd.Perm(len(botNames))[:n] {
		picked = append(picked, botNames[i])
	}
	return picked
}

func hueFor(name string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return int(h.Sum32() % 360)
}

// initialFor is the glyph drawn on the procedural avatar. Nicknames open with
// all sorts of punctuation, so fall back to the first letter or digit anywhere
// in the name before giving up.
func initialFor(name string) string {
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return strings.ToUpper(string(r))
		}
	}
	return "?"
}

// Invented handles. Real usernames off a live table belong to real people and
// have no business being seeded into a clone.
var botNames = []string{
	"neonfox", "Артём", "kiroshi", "Мурка", "dropzone", "Витя",
	"slonik", "Лиса", "mrblack", "Настя", "zerocool", "Костя",
	"pixel", "Аня", "tundra", "Дима", "voidcat", "Соня",
	"grumpy", "Марк", "lunar", "Ева", "toxic", "Илья",
	"shadow", "Рита", "nomad", "Гоша", "quartz", "Юля",
	"burnout", "Слава", "echo", "Полина", "vandal", "Тимур",
	"onyx", "Даша", "raven", "Егор", "flux", "Вера",
	"static", "Лёша", "crimson", "Ника", "husky", "Рома",
}
