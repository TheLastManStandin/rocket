package game

import (
	"errors"
	"math/rand/v2"
	"testing"
	"time"
)

var epoch = time.Unix(1700000000, 0).UTC()

// Two names at the table, so a test can tell one player's stake from another.
var (
	alice = NewPlayer(1, "Alice", "")
	bob   = NewPlayer(2, "Bob", "")
)

const (
	testBetting = 7 * time.Second
	testPause   = 3 * time.Second
)

// newTestGame pins the burst point so a round can be walked instant by instant.
func newTestGame(crash Multiplier, start time.Time) *Game {
	return newTestGameSeq(start, crash)
}

// newTestGameSeq hands out a burst point per round, repeating the last one once
// the list runs out.
func newTestGameSeq(start time.Time, crashes ...Multiplier) *Game {
	i := -1
	cfg := Config{
		MinBet:        10,
		MaxBet:        100000,
		BettingWindow: testBetting,
		CrashedPause:  testPause,
		DrawCrash: func() Multiplier {
			if i < len(crashes)-1 {
				i++
			}
			return crashes[i]
		},
	}
	return New(cfg, rand.New(rand.NewPCG(1, 2)), start)
}

func TestRoundWalksFromBettingThroughFlightToBurst(t *testing.T) {
	g := newTestGame(250, epoch)

	if g.Phase() != PhaseBetting {
		t.Fatalf("a fresh game sits in %q, want %q", g.Phase(), PhaseBetting)
	}
	if len(g.round.Bots) == 0 {
		t.Error("no bots were seated for the opening round")
	}

	if evs := g.Advance(epoch.Add(3 * time.Second)); hasEvent(evs, EventTookOff) {
		t.Error("the rocket left inside the betting window")
	}

	takeoff := epoch.Add(testBetting)
	if evs := g.Advance(takeoff); !hasEvent(evs, EventTookOff) {
		t.Fatal("the rocket did not leave when the betting window closed")
	}
	if g.Phase() != PhaseFlying {
		t.Fatalf("phase is %q after take-off, want %q", g.Phase(), PhaseFlying)
	}

	burst := takeoff.Add(TimeToReach(250))
	if evs := g.Advance(burst.Add(-time.Millisecond)); hasEvent(evs, EventCrashed) {
		t.Error("the round burst a millisecond early")
	}

	crashed := eventsOfType(g.Advance(burst), EventCrashed)
	if len(crashed) != 1 {
		t.Fatalf("got %d crash events at the burst point, want 1", len(crashed))
	}
	if crashed[0].Multiplier != 250 {
		t.Errorf("burst reported at %v, want x2.50", crashed[0].Multiplier)
	}
	if g.Phase() != PhaseCrashed {
		t.Errorf("phase is %q after the burst, want %q", g.Phase(), PhaseCrashed)
	}
	if h := g.History(); len(h) != 1 || h[0] != 250 {
		t.Errorf("history is %v, want [2.50]", h)
	}

	if evs := g.Advance(burst.Add(testPause)); !hasEvent(evs, EventRoundOpened) {
		t.Fatal("the next round did not open after the pause")
	}
	if g.Phase() != PhaseBetting || g.RoundID() != 2 {
		t.Errorf("round %d in phase %q, want round 2 in %q", g.RoundID(), g.Phase(), PhaseBetting)
	}
	if g.Bet(alice.ID) != nil {
		t.Error("a new round started carrying the previous bet")
	}
}

func TestOnlyOneBetLandsPerBettingWindow(t *testing.T) {
	g := newTestGame(250, epoch)

	if _, err := g.PlaceBet(alice, 5); !errors.Is(err, ErrStakeOutOfRange) {
		t.Errorf("staking below the minimum returned %v, want ErrStakeOutOfRange", err)
	}
	if _, err := g.PlaceBet(alice, 999999); !errors.Is(err, ErrStakeOutOfRange) {
		t.Errorf("staking above the maximum returned %v, want ErrStakeOutOfRange", err)
	}

	where, err := g.PlaceBet(alice, 500)
	if err != nil {
		t.Fatalf("a valid bet returned %v", err)
	}
	if where != PlacedThisRound {
		t.Errorf("a bet inside the window was %v, want PlacedThisRound", where)
	}
	if _, err := g.PlaceBet(alice, 500); !errors.Is(err, ErrAlreadyBet) {
		t.Errorf("betting twice returned %v, want ErrAlreadyBet", err)
	}
}

func TestOneRoundHoldsEveryPlayersStake(t *testing.T) {
	g := newTestGame(250, epoch)

	if _, err := g.PlaceBet(alice, 500); err != nil {
		t.Fatal(err)
	}
	if _, err := g.PlaceBet(bob, 700); err != nil {
		t.Fatalf("a second player could not get into the round: %v", err)
	}
	if _, err := g.PlaceBet(bob, 700); !errors.Is(err, ErrAlreadyBet) {
		t.Errorf("betting twice returned %v, want ErrAlreadyBet", err)
	}

	// The table everyone sees is the same table, whoever is asking.
	for _, viewer := range []Player{alice, bob} {
		snap := g.Snapshot(viewer.ID, epoch)
		if len(snap.Players) != 2 {
			t.Fatalf("%s sees %d players, want 2", viewer.Name, len(snap.Players))
		}
		if snap.Players[0].ID != alice.ID || snap.Players[1].ID != bob.ID {
			t.Errorf("%s sees the table in the wrong order: %+v", viewer.Name, snap.Players)
		}
	}
	// The stake fields on it, though, are the viewer's own.
	if got := g.Snapshot(alice.ID, epoch).Amount; got != 500 {
		t.Errorf("alice's snapshot carries a stake of %d, want 500", got)
	}
	if got := g.Snapshot(bob.ID, epoch).Amount; got != 700 {
		t.Errorf("bob's snapshot carries a stake of %d, want 700", got)
	}

	// One curve, so one exit price and one burst for the pair of them.
	takeoff := epoch.Add(testBetting)
	g.Advance(takeoff)
	at, payout, err := g.CashOut(alice.ID, takeoff.Add(TimeToReach(200)))
	if err != nil {
		t.Fatalf("alice could not cash out: %v", err)
	}
	if at != 200 || payout != 1000 {
		t.Errorf("alice left at x%v for %d, want x2.00 for 1000", at, payout)
	}
	if _, _, err := g.CashOut(bob.ID, takeoff.Add(TimeToReach(200))); err != nil {
		t.Fatalf("bob could not cash out on the same curve: %v", err)
	}

	events := g.Advance(takeoff.Add(TimeToReach(250)))
	burst := eventsOfType(events, EventCrashed)
	if len(burst) != 1 {
		t.Fatalf("the round burst %d times, want once", len(burst))
	}
	if len(burst[0].Settled) != 2 {
		t.Fatalf("the burst settled %d stakes, want both", len(burst[0].Settled))
	}
}

func TestABetMadeInFlightRidesTheNextRound(t *testing.T) {
	g := newTestGameSeq(epoch, 250, 400)

	g.Advance(epoch.Add(testBetting))
	if g.Phase() != PhaseFlying {
		t.Fatalf("phase is %q, want %q", g.Phase(), PhaseFlying)
	}

	where, err := g.PlaceBet(alice, 500)
	if err != nil {
		t.Fatalf("betting in flight returned %v", err)
	}
	if where != QueuedForNext {
		t.Errorf("a bet made in flight was %v, want QueuedForNext", where)
	}
	if g.Bet(alice.ID) != nil {
		t.Error("the stake was seated on the round already in the air")
	}
	if _, err := g.PlaceBet(alice, 500); !errors.Is(err, ErrAlreadyQueued) {
		t.Errorf("queueing twice returned %v, want ErrAlreadyQueued", err)
	}

	// The queued stake has to survive the burst and land on the round after it.
	now := epoch.Add(testBetting).Add(TimeToReach(250))
	g.Advance(now)
	events := g.Advance(now.Add(testPause))
	if g.Phase() != PhaseBetting {
		t.Fatalf("phase is %q after the pause, want %q", g.Phase(), PhaseBetting)
	}
	placed := eventsOfType(events, EventBetPlaced)
	if len(placed) != 1 {
		t.Fatalf("the new round announced %d bets, want 1", len(placed))
	}
	if placed[0].Amount != 500 {
		t.Errorf("the seated stake is %d, want 500", placed[0].Amount)
	}
	// Nothing moved in the ledger here: the stake was paid for when it was
	// taken, and announcing a balance would have the client show it twice.
	if placed[0].Balance != 0 {
		t.Errorf("seating a queued stake reported a balance of %d, want none", placed[0].Balance)
	}
	if bet := g.Bet(alice.ID); bet == nil || bet.Amount != 500 {
		t.Errorf("the new round holds %+v, want a 500 stake", bet)
	}
	if g.QueuedBet(alice.ID) != nil {
		t.Error("the stake is still queued after being seated")
	}
}

func TestSnapshotCarriesAStakeWaitingForTheNextRound(t *testing.T) {
	g := newTestGame(250, epoch)
	g.Advance(epoch.Add(testBetting))
	if _, err := g.PlaceBet(alice, 700); err != nil {
		t.Fatal(err)
	}

	snap := g.Snapshot(alice.ID, epoch.Add(testBetting+time.Second))
	if snap.QueuedAmount != 700 {
		t.Errorf("the snapshot reports %d waiting, want 700", snap.QueuedAmount)
	}
	if snap.Amount != 0 {
		t.Errorf("the snapshot reports a live stake of %d, want none", snap.Amount)
	}
}

func TestCashOutPaysTheCurveAtTheInstantItArrives(t *testing.T) {
	g := newTestGame(500, epoch)
	if _, err := g.PlaceBet(alice, 1000); err != nil {
		t.Fatal(err)
	}

	takeoff := epoch.Add(testBetting)
	g.Advance(takeoff)

	at := takeoff.Add(TimeToReach(200))
	m, payout, err := g.CashOut(alice.ID, at)
	if err != nil {
		t.Fatalf("cashing out mid-flight returned %v", err)
	}
	if m != 200 {
		t.Errorf("settled at %v, want x2.00", m)
	}
	if payout != 2000 {
		t.Errorf("paid %d on a 1000 stake at x2.00, want 2000", payout)
	}

	if _, _, err := g.CashOut(alice.ID, at); !errors.Is(err, ErrAlreadyCashedOut) {
		t.Errorf("cashing out twice returned %v, want ErrAlreadyCashedOut", err)
	}

	// The bet survives to the burst so the client can keep showing the win.
	g.Advance(takeoff.Add(TimeToReach(500)))
	if b := g.Bet(alice.ID); b == nil || b.CashedOutAt != 200 || b.Payout != 2000 {
		t.Errorf("settled bet came out as %+v, want x2.00 paying 2000", b)
	}
}

// The whole game hinges on this: a tap that arrives after the burst loses,
// however good the multiplier looked on the player's screen.
func TestCashOutArrivingAfterTheBurstIsRefused(t *testing.T) {
	g := newTestGame(150, epoch)
	if _, err := g.PlaceBet(alice, 1000); err != nil {
		t.Fatal(err)
	}
	takeoff := epoch.Add(testBetting)
	g.Advance(takeoff)

	if _, _, err := g.CashOut(alice.ID, takeoff.Add(TimeToReach(150))); !errors.Is(err, ErrNotFlying) {
		t.Errorf("cashing out at the burst instant returned %v, want ErrNotFlying", err)
	}
	if _, _, err := g.CashOut(alice.ID, takeoff.Add(time.Minute)); !errors.Is(err, ErrNotFlying) {
		t.Errorf("cashing out long after the burst returned %v, want ErrNotFlying", err)
	}
	if b := g.Bet(alice.ID); b.cashedOut() {
		t.Error("a late tap still settled the bet as a win")
	}
}

func TestCashOutNeedsABetAndAFlight(t *testing.T) {
	g := newTestGame(500, epoch)

	if _, _, err := g.CashOut(alice.ID, epoch); !errors.Is(err, ErrNotFlying) {
		t.Errorf("cashing out during betting returned %v, want ErrNotFlying", err)
	}

	takeoff := epoch.Add(testBetting)
	g.Advance(takeoff)
	if _, _, err := g.CashOut(alice.ID, takeoff.Add(time.Second)); !errors.Is(err, ErrNoBet) {
		t.Errorf("cashing out with no stake returned %v, want ErrNoBet", err)
	}
}

func TestBotExitsAreAnnouncedOnceEachBeforeTheBurst(t *testing.T) {
	g := newTestGame(5000, epoch) // a long round, so most of the table gets out
	takeoff := epoch.Add(testBetting)
	g.Advance(takeoff)

	winners := map[string]Multiplier{}
	for _, b := range g.round.Bots {
		if b.Won() {
			winners[b.ID] = b.CashOutAt
		}
	}
	if len(winners) == 0 {
		t.Fatal("no bot was set to escape a x50 round")
	}

	announced := map[string]int{}
	for step := time.Duration(0); step <= TimeToReach(5000); step += 100 * time.Millisecond {
		now := takeoff.Add(step)
		for _, e := range eventsOfType(g.Advance(now), EventBotCashedOut) {
			announced[e.BotID]++

			want, ok := winners[e.BotID]
			if !ok {
				t.Errorf("bot %s was announced but never meant to escape", e.BotID)
			}
			if e.Multiplier != want {
				t.Errorf("bot %s announced at %v, want its target %v", e.BotID, e.Multiplier, want)
			}
			if e.Multiplier > MultiplierAt(now.Sub(takeoff)) {
				t.Errorf("bot %s escaped at %v before the curve reached it", e.BotID, e.Multiplier)
			}
		}
	}

	for id := range winners {
		switch announced[id] {
		case 1:
		case 0:
			t.Errorf("bot %s escaped but was never announced", id)
		default:
			t.Errorf("bot %s was announced %d times, want once", id, announced[id])
		}
	}
}

func TestHistoryKeepsTheLatestRoundsNewestFirst(t *testing.T) {
	crashes := []Multiplier{100, 120, 140, 160, 180, 200, 220, 240, 260, 280, 300, 320}
	g := newTestGameSeq(epoch, crashes...)

	now := epoch
	for range crashes {
		now = now.Add(testBetting)
		g.Advance(now)
		now = now.Add(TimeToReach(g.round.Crash))
		g.Advance(now)
		now = now.Add(testPause)
		g.Advance(now)
	}

	h := g.History()
	if len(h) != HistoryLen {
		t.Fatalf("history holds %d rounds, want %d", len(h), HistoryLen)
	}
	for i, want := range []Multiplier{320, 300, 280, 260, 240, 220, 200, 180, 160, 140} {
		if h[i] != want {
			t.Errorf("history[%d] = %v, want %v", i, h[i], want)
		}
	}
}

func TestSnapshotDescribesARoundInFlight(t *testing.T) {
	g := newTestGame(500, epoch)
	if _, err := g.PlaceBet(alice, 750); err != nil {
		t.Fatal(err)
	}

	opening := g.Snapshot(alice.ID, epoch.Add(2*time.Second))
	if opening.Phase != PhaseBetting {
		t.Errorf("snapshot phase is %q, want %q", opening.Phase, PhaseBetting)
	}
	if opening.EndsInMS != 5000 {
		t.Errorf("betting window has %dms left, want 5000", opening.EndsInMS)
	}
	if opening.Amount != 750 {
		t.Errorf("snapshot reports a %d stake, want 750", opening.Amount)
	}

	takeoff := epoch.Add(testBetting)
	g.Advance(takeoff)

	inFlight := g.Snapshot(alice.ID, takeoff.Add(TimeToReach(200)))
	if inFlight.Phase != PhaseFlying {
		t.Errorf("snapshot phase is %q, want %q", inFlight.Phase, PhaseFlying)
	}
	if inFlight.Multiplier != 200 {
		t.Errorf("snapshot curve reads %v, want x2.00", inFlight.Multiplier)
	}
	if inFlight.EndsInMS != 0 {
		t.Errorf("a flight reported a countdown of %dms, want none", inFlight.EndsInMS)
	}
}

// A stalled driver must settle the round that was open and then get back in
// step with the clock, rather than wedging or replaying forever.
func TestAdvanceCatchesUpAfterAStall(t *testing.T) {
	g := newTestGame(200, epoch)
	if _, err := g.PlaceBet(alice, 500); err != nil {
		t.Fatal(err)
	}

	// A minute is about the worst a live session can stall before the socket's
	// own keepalive would have torn it down anyway.
	now := epoch.Add(time.Minute)

	crashed := eventsOfType(g.Advance(now), EventCrashed)
	if len(crashed) == 0 {
		t.Fatal("the round that was in flight never burst")
	}
	if crashed[0].Multiplier != 200 {
		t.Errorf("the stalled round burst at %v, want its x2.00 crash point", crashed[0].Multiplier)
	}

	// Keep driving as the ticker would; transitions must stop once it is level
	// with the clock.
	caughtUp := false
	for range 100 {
		if !hasAnyEvent(g.Advance(now), EventTookOff, EventCrashed, EventRoundOpened) {
			caughtUp = true
			break
		}
	}
	if !caughtUp {
		t.Fatal("the game never stopped transitioning, so it never caught up")
	}

	switch g.Phase() {
	case PhaseBetting, PhaseFlying, PhaseCrashed:
	default:
		t.Errorf("game came to rest in phase %q", g.Phase())
	}
}

// Tickers fire late. The round must still last exactly as long as the countdown
// the client was given, or the two drift apart round after round.
func TestPhasesKeepTheirScheduleWhenTheDriverIsLate(t *testing.T) {
	g := newTestGame(400, epoch)

	// Notice the window closing 40ms late.
	g.Advance(epoch.Add(testBetting + 40*time.Millisecond))
	if want := epoch.Add(testBetting); !g.round.TookOffAt.Equal(want) {
		t.Errorf("took off at %v, want the scheduled %v", g.round.TookOffAt, want)
	}

	// Notice the burst 80ms late.
	burst := g.round.TookOffAt.Add(TimeToReach(400))
	g.Advance(burst.Add(80 * time.Millisecond))
	if want := burst.Add(testPause); !g.round.PhaseEnds.Equal(want) {
		t.Errorf("pause ends at %v, want %v", g.round.PhaseEnds, want)
	}
}

// Snapshot feeds reconnects, so it must never carry a bot's unreached target.
func TestSnapshotHidesUnreachedBotTargets(t *testing.T) {
	g := newTestGame(5000, epoch)
	takeoff := epoch.Add(testBetting)
	g.Advance(takeoff)
	g.Advance(takeoff.Add(TimeToReach(150)))

	for _, v := range g.Snapshot(alice.ID, takeoff.Add(TimeToReach(150))).Bots {
		if v.CashedOutAt == 0 {
			continue
		}
		if v.CashedOutAt > 150 {
			t.Errorf("bot %s exposed a %v exit the curve has not reached", v.ID, v.CashedOutAt)
		}
	}
}

// The table has to fill over the countdown rather than appearing all at once,
// and everyone must be seated before betting shuts.
func TestBotsArriveGraduallyAndAreAllSeatedByTakeOff(t *testing.T) {
	g := newTestGame(400, epoch)

	opened := g.Advance(epoch)
	for _, e := range opened {
		if e.Type == EventRoundOpened && len(e.Bots) != 0 {
			t.Errorf("round_opened carried %d bots, want an empty table", len(e.Bots))
		}
	}
	if seen := len(g.Snapshot(alice.ID, epoch).Bots); seen != 0 {
		t.Errorf("snapshot showed %d bots the instant the round opened, want 0", seen)
	}

	seated := map[string]int{}
	countAt := map[time.Duration]int{}
	for step := time.Duration(0); step < testBetting; step += 250 * time.Millisecond {
		for _, e := range eventsOfType(g.Advance(epoch.Add(step)), EventBotJoined) {
			for _, v := range e.Bots {
				seated[v.ID]++
			}
		}
		countAt[step] = len(seated)
	}

	if countAt[0] == len(g.round.Bots) {
		t.Error("the whole table sat down in the opening frame")
	}
	if countAt[2*time.Second] <= countAt[0] {
		t.Error("no further bots arrived over the countdown")
	}

	g.Advance(epoch.Add(testBetting))
	if len(seated) != len(g.round.Bots) {
		// Any straggler would be betting after the window shut.
		t.Errorf("%d of %d bots were seated by take-off, want all", len(seated), len(g.round.Bots))
	}
	for id, times := range seated {
		if times != 1 {
			t.Errorf("bot %s arrived %d times, want once", id, times)
		}
	}
}

func TestSeedHistoryRestoresAnEarlierSession(t *testing.T) {
	g := newTestGame(250, epoch)
	g.SeedHistory([]Multiplier{431, 107, 1200})

	if got := g.History(); len(got) != 3 || got[0] != 431 {
		t.Fatalf("history is %v, want the restored strip newest first", got)
	}

	// A round played now goes in front of what was restored.
	g.Advance(epoch.Add(testBetting))
	g.Advance(epoch.Add(testBetting).Add(TimeToReach(250)))
	if got := g.History(); len(got) != 4 || got[0] != 250 || got[1] != 431 {
		t.Errorf("history is %v, want the new burst ahead of the restored strip", got)
	}
}
