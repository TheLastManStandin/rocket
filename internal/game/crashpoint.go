package game

import (
	"crypto/rand"
	"encoding/binary"
	"math"
)

// DrawCrashPoint picks where a round bursts, using the process-wide secure RNG.
func DrawCrashPoint(houseEdge, maxCrash float64) Multiplier {
	return crashPointFrom(secureFloat(), houseEdge, maxCrash)
}

// crashPointFrom maps a uniform u in [0,1) onto a burst point.
//
// 1/(1-u) has P(X >= x) = 1/x -- the heavy tail a crash game needs: a median
// near x2.00, most rounds cheap, the occasional x60 that keeps players
// watching. The edge is taken by bursting a houseEdge slice at x1.00 outright
// and renormalising the rest, which leaves the tail shape untouched.
//
// Split out from DrawCrashPoint so the mapping can be tested exactly.
func crashPointFrom(u, houseEdge, maxCrash float64) Multiplier {
	if u < houseEdge {
		return One
	}
	v := (u - houseEdge) / (1 - houseEdge)
	m := math.Floor(100 / (1 - v))

	if ceiling := math.Floor(maxCrash * 100); m > ceiling {
		m = ceiling
	}
	if m < float64(One) {
		m = float64(One)
	}
	return Multiplier(m)
}

// secureFloat returns a uniform float64 in [0,1) with a full 53-bit mantissa.
func secureFloat() float64 {
	var b [8]byte
	// crypto/rand.Read never reports an error; it panics if the OS source is
	// unavailable, which is the right outcome for a gambling RNG anyway.
	_, _ = rand.Read(b[:])
	return float64(binary.BigEndian.Uint64(b[:])>>11) / (1 << 53)
}
