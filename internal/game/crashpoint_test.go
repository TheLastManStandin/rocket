package game

import (
	"math"
	"testing"
	"time"
)

func TestCrashPointMapsUniformOntoTheCurve(t *testing.T) {
	const edge = 0.04

	tests := []struct {
		name string
		u    float64
		want Multiplier
	}{
		{"zero busts instantly", 0, One},
		{"just inside the edge busts", edge - 1e-9, One},
		{"edge boundary is the first live round", edge, One},
		{"halfway is the median", 0.52, 200}, // v = 0.5  -> 100/0.5
		{"three quarters", 0.76, 400},        // v = 0.75 -> 100/0.25
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := crashPointFrom(tt.u, edge, 1000); got != tt.want {
				t.Errorf("crashPointFrom(%v) = %v, want %v", tt.u, got, tt.want)
			}
		})
	}

	// Deep in the tail the inputs stop being binary-exact, so allow the last
	// hundredth either way rather than pretending the arithmetic is clean.
	if got := crashPointFrom(0.988, edge, 1000); got < 7999 || got > 8000 {
		t.Errorf("crashPointFrom(0.988) = %v, want about x80.00", got)
	}
}

func TestCrashPointRespectsTheCeiling(t *testing.T) {
	got := crashPointFrom(0.999999999, 0.04, 500)
	if want := Multiplier(50000); got != want {
		t.Errorf("crashPointFrom near 1 = %v, want the %v ceiling", got, want)
	}
}

// The tail is the whole feel of the game, so pin its shape: P(X >= x) tracks
// (1-edge)/x.
func TestCrashPointDistributionHoldsItsShape(t *testing.T) {
	const (
		edge    = 0.04
		samples = 200_000
	)

	atLeast := map[Multiplier]int{200: 0, 400: 0, 1000: 0}
	for range samples {
		m := DrawCrashPoint(edge, 1000)
		for threshold := range atLeast {
			if m >= threshold {
				atLeast[threshold]++
			}
		}
	}
	for threshold, count := range atLeast {
		got := float64(count) / samples
		want := (1 - edge) / threshold.Float()
		if math.Abs(got-want) > 0.01 {
			t.Errorf("P(X >= %v) = %.4f, want about %.4f", threshold, got, want)
		}
	}
}

// The edge only means something as return-to-player: a player who always cashes
// out at the same target should get back (1 - edge) of everything they stake,
// whatever target they pick.
func TestCrashPointReturnsTheAdvertisedRTP(t *testing.T) {
	const (
		edge    = 0.04
		samples = 200_000
	)

	// Stake something realistic: Payout floors, so a stake of 1 loses the
	// fractional part of every win and understates the return badly.
	const stake = 1000

	for _, target := range []Multiplier{150, 200, 500, 1000} {
		var returned int64
		for range samples {
			if DrawCrashPoint(edge, 1000) >= target {
				returned += target.Payout(stake)
			}
		}
		rtp := float64(returned) / float64(samples*stake)
		if math.Abs(rtp-(1-edge)) > 0.02 {
			t.Errorf("cashing out at %v returned %.4f of stake, want about %.2f", target, rtp, 1-edge)
		}
	}
}

func TestCurveClimbsAndInverts(t *testing.T) {
	if got := MultiplierAt(0); got != One {
		t.Errorf("MultiplierAt(0) = %v, want %v", got, One)
	}
	if got := MultiplierAt(-time.Second); got != One {
		t.Errorf("MultiplierAt(negative) = %v, want %v", got, One)
	}
	if got := MultiplierAt(5 * time.Second); got != 200 {
		t.Errorf("MultiplierAt(5s) = %v, want x2.00", got)
	}
	if got := MultiplierAt(10 * time.Second); got != 400 {
		t.Errorf("MultiplierAt(10s) = %v, want x4.00", got)
	}
	if got := TimeToReach(One); got != 0 {
		t.Errorf("TimeToReach(x1.00) = %v, want 0", got)
	}

	// The scheduled burst has to land on the instant the curve actually reads
	// its own crash point. Sweep the whole live range rather than spot-check.
	for m := One; m <= 20000; m++ {
		if got := MultiplierAt(TimeToReach(m)); got != m {
			t.Fatalf("MultiplierAt(TimeToReach(%v)) = %v, want %v", m, got, m)
		}
	}
}

func TestPayoutRoundsDown(t *testing.T) {
	if got := Multiplier(233).Payout(500); got != 1165 {
		t.Errorf("x2.33 on 500 = %d, want 1165", got)
	}
	if got := Multiplier(133).Payout(7); got != 9 { // 9.31 -> 9
		t.Errorf("x1.33 on 7 = %d, want 9", got)
	}
}

// A stalled driver hands MultiplierAt an enormous elapsed time. It has to
// saturate: an overflowed, negative multiplier reads as below every crash
// point, so the round would never burst and the stake would never settle.
func TestMultiplierAtSaturatesInsteadOfOverflowing(t *testing.T) {
	for _, elapsed := range []time.Duration{
		5 * time.Minute,
		time.Hour,
		24 * time.Hour,
		1 << 62, // close to the largest Duration there is
	} {
		got := MultiplierAt(elapsed)
		if got < One {
			t.Errorf("MultiplierAt(%v) = %v, want a value at or above x1.00", elapsed, got)
		}
		if got != multiplierCeiling {
			t.Errorf("MultiplierAt(%v) = %v, want the %v ceiling", elapsed, got, multiplierCeiling)
		}
	}
}
