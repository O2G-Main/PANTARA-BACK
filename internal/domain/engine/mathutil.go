package engine

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Clamp01 restricts v into the closed interval [0, 1].
func Clamp01(v float64) float64 {
	return Clamp(v, 0, 1)
}

// Clamp restricts v into [lo, hi].
func Clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Round2 matches JavaScript Math.round(v * 100) / 100 semantics.
func Round2(v float64) float64 {
	return math.Floor(v*100+0.5) / 100
}

// RoundIntPct converts a 0..1 ratio to integer percentage using Math.round semantics.
func RoundIntPct(v float64) int {
	return int(math.Floor(v*100 + 0.5))
}

func fmtNumber(v float64, decimals int) string {
	pow := math.Pow(10, float64(decimals))
	rounded := math.Floor(v*pow+0.5) / pow
	s := strconv.FormatFloat(rounded, 'f', decimals, 64)
	if decimals > 0 {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	return strings.ReplaceAll(s, ".", ",")
}

func fmtRoundedInt(v float64) string {
	return fmt.Sprintf("%d", int(math.Floor(v+0.5)))
}
