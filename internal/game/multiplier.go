// Package game owns the crash round: how fast the curve climbs, where it
// bursts, and which fake players sit at the table. Nothing here trusts the
// client.
package game

import (
	"math"
	"strconv"
	"time"
)

// Multiplier counts hundredths, so 250 means x2.50.
//
// Money settles on comparisons against this value, and a float x2.50 that is
// really 2.4999999 pays out the wrong side of a cash-out. Integers make the
// boundary exact.
type Multiplier int64

const One Multiplier = 100

func (m Multiplier) Float() float64 { return float64(m) / 100 }

// String formats for display: "2.50", matching the chips in the history strip.
func (m Multiplier) String() string { return strconv.FormatFloat(m.Float(), 'f', 2, 64) }

// Payout is the stake multiplied out, rounded down so the house never pays a
// fraction it did not take.
func (m Multiplier) Payout(stake int64) int64 { return stake * int64(m) / 100 }

// growthLambda drives m(t) = e^(lambda*t). ln(2)/5 puts x2.00 at five seconds,
// which lands a x66 round around half a minute -- the pacing the original runs.
const growthLambda = math.Ln2 / 5

// multiplierCeiling saturates the curve well below the point where hundredths
// stop fitting in an int64, which happens after roughly four and a half minutes
// of flight. Letting exp run past that wraps the multiplier negative, and a
// negative curve reads as "still below the crash point" -- a round whose driver
// stalled (a suspended host, a frozen container, a long pause) would then fly
// forever and never settle the stake.
const multiplierCeiling Multiplier = 1 << 50

// MultiplierAt is where the curve stands after elapsed time in flight.
func MultiplierAt(elapsed time.Duration) Multiplier {
	if elapsed <= 0 {
		return One
	}
	m := math.Exp(growthLambda*elapsed.Seconds()) * 100
	// Written as a negated comparison so +Inf and NaN saturate too.
	if !(m < float64(multiplierCeiling)) {
		return multiplierCeiling
	}
	if m < float64(One) {
		return One
	}
	return Multiplier(math.Floor(m))
}

// TimeToReach inverts MultiplierAt: how long the curve takes to climb to m.
// The engine uses it to schedule the burst and every bot cash-out up front.
func TimeToReach(m Multiplier) time.Duration {
	if m <= One {
		return 0
	}
	// log, the scaling, and exp each round, so the analytic inverse can land a
	// few nanoseconds before the instant the curve actually reads m -- long
	// enough for MultiplierAt to floor to m-1 and burst a round on a curve
	// showing one hundredth below its own crash point. Rather than trust the
	// float, walk forward until MultiplierAt agrees. The gap is nanoseconds and
	// a hundredth of growth takes milliseconds, so this converges at once and
	// cannot overshoot into m+1.
	d := time.Duration(math.Ceil(math.Log(m.Float()) / growthLambda * float64(time.Second)))
	for step := time.Duration(1); MultiplierAt(d) < m; step *= 2 {
		d += step
	}
	return d
}
