package webp

import "testing"

func TestVP8TrellisIncrementalTransformErrorMatchesFullScore(t *testing.T) {
	state := uint32(1)
	probs := vp8DefaultTokenProbs
	for test := 0; test < 256; test++ {
		var transformed [16]int
		for i := range transformed {
			state = state*1664525 + 1013904223
			transformed[i] = int(state>>20) - 2048
		}
		plane := test % 4
		context := uint8(test % 3)
		start := test & 1
		for passes := 1; passes <= 2; passes++ {
			got := quantizeTransformedVP8BlockRDBiasedPasses(transformed, 20, 24, 114, 116, plane, context, start, 37, &probs, passes)
			want := quantizeTransformedVP8BlockRDBiasedReference(transformed, 20, 24, 114, 116, plane, context, start, 37, &probs, passes)
			if got != want {
				t.Fatalf("case %d passes %d = %v, want %v", test, passes, got, want)
			}
		}
	}
}

func quantizeTransformedVP8BlockRDBiasedReference(transformed [16]int, dcQ int, acQ int, dcBias int, acBias int, plane int, context uint8, start int, lambda int64, tokenProbs *vp8TokenProbs, maxPasses int) vp8QuantizedBlock {
	coeff := quantizeTransformedVP8BlockFromBiased(transformed, dcQ, acQ, dcBias, acBias, start)
	bestScore := vp8TrellisBlockScore(transformed, coeff, dcQ, acQ, plane, context, start, lambda, tokenProbs)
	for pass := 0; pass < maxPasses; pass++ {
		changed := false
		for n := 15; n >= start; n-- {
			index := int(vp8Zigzag[n])
			level := coeff[index]
			if level == 0 {
				continue
			}
			towardZero := level - 1
			if level < 0 {
				towardZero = level + 1
			}
			bestLevel := level
			for _, candidate := range [2]int16{towardZero, 0} {
				if candidate == level || candidate == bestLevel {
					continue
				}
				coeff[index] = candidate
				score := vp8TrellisBlockScore(transformed, coeff, dcQ, acQ, plane, context, start, lambda, tokenProbs)
				if score < bestScore || score == bestScore && absInt(int(candidate)) < absInt(int(bestLevel)) {
					bestScore = score
					bestLevel = candidate
				}
			}
			coeff[index] = bestLevel
			changed = changed || bestLevel != level
		}
		if !changed {
			break
		}
	}
	return coeff
}
