package webp

import "testing"

// BenchmarkVP8LOptimalTokens isolates parsing from match search and entropy
// selection so both amortized cost reuse and frequent invalidation are measured.
func BenchmarkVP8LOptimalTokens(b *testing.B) {
	const pixelCount = 65536
	for _, tc := range []struct {
		name        string
		groupSpan   int
		matchStride int
		matchLength int
	}{
		{"StableLong", pixelCount, 1, 128},
		{"StableShort", pixelCount, 1, 4},
		{"Switch8Short", 8, 1, 4},
		{"Switch32Short", 32, 1, 4},
		{"SparseSwitch8", 8, 8, 4},
		{"VerySparseSwitch8", 8, 64, 4},
		{"NoMatches", 8, 0, 4},
	} {
		b.Run(tc.name, func(b *testing.B) {
			pixels := make([]uint32, pixelCount)
			for i := range pixels {
				pixels[i] = 0xffffffff
			}
			groups := vp8lParseTestGroups()[:3]
			graph := vp8lParseTestGraph(len(pixels), func(position int) []vp8lMatch {
				if tc.matchStride == 0 || position < 8 || position%tc.matchStride != 0 || len(pixels)-position < 4 {
					return nil
				}
				return []vp8lMatch{vp8lNewMatch(min(tc.matchLength, len(pixels)-position), 1)}
			})
			groupAt := func(position int) *vp8lCodeGroup {
				return &groups[(position/tc.groupSpan)%len(groups)]
			}
			var workspace vp8lSearchWorkspace
			vp8lOptimalTokensWithGroups(pixels, graph, nil, groupAt, &workspace)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				vp8lOptimalTokensWithGroups(pixels, graph, nil, groupAt, &workspace)
			}
		})
	}
}
