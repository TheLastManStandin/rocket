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
	ID   string `json:"id"`
	Name string `json:"name"`
	// PhotoURL is empty for the minority of bots that go without one, and the
	// client falls back to drawing Hue and Initial the way it always has.
	PhotoURL string `json:"photoUrl,omitempty"`
	Hue      int    `json:"hue"`
	Initial  string `json:"initial"`
	Bet      int64  `json:"bet"`

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

	// avatarShare is how many bots in a hundred wear a picture. It is settled
	// by the bot's name rather than by a roll, so a given name looks the same
	// every time it sits down -- including the ones that never get a picture.
	avatarShare = 90
)

// NewBots seats a crowd for a round that bursts at crash, spread across window
// so they arrive a few at a time rather than all at once. names and avatars are
// what the crowd is dealt from; either may be empty.
func NewBots(crash Multiplier, window time.Duration, rnd *rand.Rand, names, avatars []string) []*Bot {
	n := botsMin + rnd.IntN(botsMax-botsMin+1)

	pool := names
	if len(pool) == 0 {
		pool = botNames
	}
	wears := wearers(pool)

	bots := make([]*Bot, 0, n)
	for i, name := range pickNames(n, pool, rnd) {
		b := &Bot{
			ID:       fmt.Sprintf("b%d", i+1),
			Name:     name,
			PhotoURL: avatarFor(name, avatars, wears[name]),
			Hue:      hueFor(name),
			Initial:  initialFor(name),
			Bet:      drawBotBet(rnd),
			JoinsAt:  drawJoinDelay(window, rnd),
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

// pickNames deals n distinct names off the pool. A pool too short to fill the
// table shrinks the table rather than seating anyone twice: the same nickname
// in two rows reads as a bug however honestly it got there.
func pickNames(n int, pool []string, rnd *rand.Rand) []string {
	if n > len(pool) {
		n = len(pool)
	}

	picked := make([]string, 0, n)
	for _, i := range rnd.Perm(len(pool))[:n] {
		picked = append(picked, pool[i])
	}
	return picked
}

// wearers picks which names in the pool get a picture: the share of them
// whose name hashes lowest.
//
// Taking a share of the pool rather than rolling per name is what makes the
// share the share. Rolling gets it right on average over a pool of any size,
// but a pool is a fixed list of a few dozen, and one that happened to hash
// badly would sit at eight in ten or ten in ten for good.
func wearers(pool []string) map[string]bool {
	ranked := make([]string, len(pool))
	copy(ranked, pool)
	sort.Slice(ranked, func(i, j int) bool {
		return hashOf("wears:"+ranked[i]) < hashOf("wears:"+ranked[j])
	})

	out := make(map[string]bool, len(ranked))
	for _, name := range ranked[:(len(ranked)*avatarShare+50)/100] {
		out[name] = true
	}
	return out
}

// avatarFor settles which picture a name wears. It comes off the name, so a
// bot's face is the same every round it turns up in -- a crowd whose faces
// reshuffled every fifteen seconds would read as a slideshow rather than as a
// room.
func avatarFor(name string, avatars []string, wears bool) string {
	if !wears || len(avatars) == 0 {
		return ""
	}
	return avatars[hashOf("face:"+name)%uint32(len(avatars))]
}

func hashOf(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

func hueFor(name string) int { return int(hashOf(name) % 360) }

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

// The fallback pool, used when assets/bot-names.txt is missing or empty.
// Invented handles: real usernames off a live table belong to real people and
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
