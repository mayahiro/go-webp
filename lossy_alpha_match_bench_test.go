package webp

import "testing"

var alphaMatchBenchmarkResult alphaSpatialMatch

func BenchmarkAlphaBestSpatialMatch(b *testing.B) {
	for _, tc := range []struct {
		name        string
		width       int
		pattern     string
		hasPrevious bool
	}{
		{"Noise128", 128, "noise", true},
		{"Narrow1", 1, "flat", true},
		{"Narrow3", 3, "noise", true},
		{"Narrow9", 9, "noise", true},
		{"Flat512", 512, "flat", true},
		{"Shift512", 512, "shift", true},
		{"NoPrevious", 128, "noise", false},
	} {
		b.Run(tc.name, func(b *testing.B) {
			current, previous := make([]byte, tc.width), make([]byte, tc.width)
			state := uint32(12345)
			next := func() byte {
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				return byte(state)
			}
			for i := range current {
				current[i], previous[i] = next(), next()
			}
			switch tc.pattern {
			case "flat":
				for i := range current {
					current[i], previous[i] = 7, 7
				}
			case "shift":
				copy(current[1:], previous[:len(previous)-1])
			}
			starts := [...]int{0, tc.width / 2, tc.width - 1}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for _, start := range starts {
					alphaMatchBenchmarkResult = alphaBestSpatialMatch(current, previous, start, tc.hasPrevious)
				}
			}
		})
	}
}
