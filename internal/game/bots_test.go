package game

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

const testWindow = 7 * time.Second

func seeded() *rand.Rand { return rand.New(rand.NewPCG(42, 1024)) }

func TestNewBotsSeatsAPlausibleTable(t *testing.T) {
	bots := NewBots(500, testWindow, seeded(), nil, nil)

	if len(bots) < botsMin || len(bots) > botsMax {
		t.Fatalf("seated %d bots, want between %d and %d", len(bots), botsMin, botsMax)
	}

	seenName := map[string]bool{}
	seenID := map[string]bool{}
	for _, b := range bots {
		if seenName[b.Name] {
			t.Errorf("%q sits at the table twice", b.Name)
		}
		if seenID[b.ID] {
			t.Errorf("id %q used twice", b.ID)
		}
		seenName[b.Name], seenID[b.ID] = true, true

		if b.Bet <= 0 {
			t.Errorf("%q staked %d, want a positive bet", b.Name, b.Bet)
		}
		if b.Hue < 0 || b.Hue > 359 {
			t.Errorf("%q got hue %d, want 0..359", b.Name, b.Hue)
		}
		if b.Initial == "" {
			t.Errorf("%q got no avatar initial", b.Name)
		}
	}
}

// A bot may only be marked a winner if it really did get out before the burst.
func TestNewBotsOnlyLetWinnersOutBeforeTheBurst(t *testing.T) {
	for _, crash := range []Multiplier{One, 110, 200, 1337, 20000} {
		rnd := rand.New(rand.NewPCG(7, uint64(crash)))
		for _, b := range NewBots(crash, testWindow, rnd, nil, nil) {
			switch {
			case b.Won() && b.CashOutAt >= crash:
				t.Errorf("crash %v: %q cashed out at %v, at or past the burst", crash, b.Name, b.CashOutAt)
			case b.Won() && b.Payout() <= 0:
				t.Errorf("crash %v: %q won but takes home %d", crash, b.Name, b.Payout())
			case !b.Won() && b.Payout() != 0:
				t.Errorf("crash %v: %q burned but takes home %d", crash, b.Name, b.Payout())
			}
		}
	}
}

// x1.00 bursts before anyone can move, so the whole table has to burn.
func TestNewBotsAllBurnOnAnInstantBust(t *testing.T) {
	for _, b := range NewBots(One, testWindow, seeded(), nil, nil) {
		if b.Won() {
			t.Errorf("%q escaped a x1.00 round at %v", b.Name, b.CashOutAt)
		}
	}
}

func TestNewBotsIsReproducibleForASeed(t *testing.T) {
	a, b := NewBots(300, testWindow, seeded(), nil, nil), NewBots(300, testWindow, seeded(), nil, nil)

	if len(a) != len(b) {
		t.Fatalf("same seed seated %d bots then %d", len(a), len(b))
	}
	for i := range a {
		if *a[i] != *b[i] {
			t.Errorf("bot %d differs across runs: %+v vs %+v", i, *a[i], *b[i])
		}
	}
}

// The crash point must not be inferable from what goes over the wire.
func TestBotCashOutTargetStaysOffTheWire(t *testing.T) {
	bot := &Bot{ID: "b1", Name: "neonfox", Bet: 500, CashOutAt: 742}

	encoded, err := marshal(bot)
	if err != nil {
		t.Fatal(err)
	}
	if contains(encoded, "742") {
		t.Errorf("serialised bot leaks its cash-out target: %s", encoded)
	}
}

func TestInitialForHandlesAwkwardNames(t *testing.T) {
	tests := map[string]string{
		"neonfox": "N",
		"Артём":   "А",
		"⁰⁷⁷":     "?",
		"":        "?",
		"_pixel":  "P",
		"7up":     "7",
	}
	for name, want := range tests {
		if got := initialFor(name); got != want {
			t.Errorf("initialFor(%q) = %q, want %q", name, got, want)
		}
	}
}

// Roughly nine bots in ten wear a picture, and which one is settled by the
// name: a crowd whose faces reshuffled every round would read as a slideshow.
func TestBotsWearAvatarsFromThePack(t *testing.T) {
	avatars := []string{"/bot-avatars/a.png", "/bot-avatars/b.png", "/bot-avatars/c.png"}
	worn := map[string]string{}
	var with, total int

	for seed := range uint64(200) {
		rnd := rand.New(rand.NewPCG(seed, 99))
		for _, b := range NewBots(500, testWindow, rnd, nil, avatars) {
			total++
			if b.PhotoURL == "" {
				continue
			}
			with++
			if !slices.Contains(avatars, b.PhotoURL) {
				t.Fatalf("%q wears %q, which is not in the pack", b.Name, b.PhotoURL)
			}
			if seen, ok := worn[b.Name]; ok && seen != b.PhotoURL {
				t.Fatalf("%q wore %q and then %q", b.Name, seen, b.PhotoURL)
			}
			worn[b.Name] = b.PhotoURL
		}
	}

	// A share of the pool, not a roll per name, so this lands on the number
	// rather than near it -- the slack is only the luck of which names got
	// dealt across the rounds above.
	share := float64(with) / float64(total) * 100
	if share < 86 || share > 94 {
		t.Errorf("%.0f%% of bots wear a picture, want about %d%%", share, avatarShare)
	}
}

// An empty folder is the ordinary state of a fresh checkout: every bot falls
// back to the letter on a coloured disc.
func TestBotsGoBareWithoutAPack(t *testing.T) {
	for _, b := range NewBots(500, testWindow, seeded(), nil, nil) {
		if b.PhotoURL != "" {
			t.Errorf("%q wears %q with no pack loaded", b.Name, b.PhotoURL)
		}
		if b.Initial == "" {
			t.Errorf("%q has neither a picture nor a letter", b.Name)
		}
	}
}

// A pool too short to fill the table seats fewer bots rather than the same
// name twice.
func TestShortNamePoolSeatsFewerBots(t *testing.T) {
	pool := []string{"one", "two", "three"}
	bots := NewBots(500, testWindow, seeded(), pool, nil)

	if len(bots) != len(pool) {
		t.Fatalf("seated %d bots off a pool of %d", len(bots), len(pool))
	}
	seen := map[string]bool{}
	for _, b := range bots {
		if seen[b.Name] {
			t.Errorf("%q sat down twice", b.Name)
		}
		seen[b.Name] = true
	}
}
