package webp

import (
	"image"
	"sort"
)

type vp8lRankedCandidate struct {
	index int
	bits  uint64
}

func vp8lFinalizeEncodingPlanV2(readPixel pixelReader, bounds image.Rectangle, width int, height int, literalPlan vp8lEncodingPlan, candidates *[vp8lMaxEncodingPlanCandidates]vp8lEncodingPlan, candidateCount int, literalBestIndex int, best vp8lEncodingPlan, bestBits uint64, cfg vp8lEncodingConfig) vp8lEncodingPlan {
	if candidateCount == 0 {
		return literalPlan
	}

	literalRanking := vp8lRankLiteralCandidates(candidates, candidateCount, width, height)
	if cfg.tryLZ77 {
		screeningIndices := vp8lSelectDiverseCandidateIndices(literalRanking, candidates, cfg.finalLZ77Candidates)
		screeningIndices = vp8lAppendRankedCandidates(screeningIndices, literalRanking, candidates, func(plan vp8lEncodingPlan) bool { return plan.baselineCandidate })
		lz77Ranking := vp8lRankGreedyLZ77Candidates(readPixel, bounds, width, height, candidates, screeningIndices, bestBits, cfg)
		indices := vp8lTopCandidateIndices(lz77Ranking, cfg.finalLZ77Candidates)
		indices = vp8lAppendFirstRankedCandidate(indices, lz77Ranking, candidates, func(plan vp8lEncodingPlan) bool { return plan.colorIndexing })
		indices = vp8lAppendRankedCandidates(indices, lz77Ranking, candidates, func(plan vp8lEncodingPlan) bool { return plan.baselineCandidate })
		for _, index := range indices {
			best, bestBits = vp8lConsiderCandidateLZ77Config(readPixel, bounds, width, height, candidates[index], best, bestBits, cfg)
		}
	}

	literalIndices := vp8lSelectDiverseCandidateIndices(literalRanking, candidates, cfg.finalLiteralCandidates)
	literalIndices = vp8lAppendRankedCandidates(literalIndices, literalRanking, candidates, func(plan vp8lEncodingPlan) bool { return plan.baselineCandidate })
	if !vp8lContainsCandidateIndex(literalIndices, literalBestIndex) {
		literalIndices = append(literalIndices, literalBestIndex)
	}
	bestColorCache := vp8lEncodingPlan{}
	bestColorCacheBits := ^uint64(0)
	colorCacheConfig := cfg
	colorCacheConfig.tryMetaPrefix = false
	colorCacheLimit := bestBits + bestBits/4 + 4096
	if colorCacheLimit < bestBits {
		colorCacheLimit = ^uint64(0)
	}
	for _, index := range literalIndices {
		candidate := candidates[index]
		if cfg.tryMetaPrefix {
			best, bestBits = vp8lConsiderCandidateMetaPrefix(readPixel, bounds, width, height, candidate, best, bestBits)
		}
		if !cfg.tryColorCache || candidate.colorIndexing || candidate.analysis.allChannelsConstant() {
			continue
		}
		if colorCachePlan, ok := makeVP8LColorCachePlanConfig(readPixel, bounds, width, height, candidate, colorCacheLimit, colorCacheConfig); ok {
			candidateBits := vp8lPayloadBits(width, height, colorCachePlan)
			if candidateBits < bestColorCacheBits {
				bestColorCache = colorCachePlan
				bestColorCacheBits = candidateBits
			}
			if candidateBits < bestBits {
				best = colorCachePlan
				bestBits = candidateBits
			}
		}
	}
	if cfg.tryFinalColorCacheMetaPrefix && bestColorCache.colorCache != nil && len(bestColorCache.colorCache.tokens) <= vp8lMaxMetaPrefixColorCacheTokens {
		mainWidth, mainHeight := vp8lPlanImageDimensions(width, height, bestColorCache)
		mainBounds := image.Rect(0, 0, mainWidth, mainHeight)
		if !bestColorCache.colorIndexing {
			mainBounds = bounds
		}
		read := vp8lPlanPixelReader(readPixel, bounds, width, height, bestColorCache)
		if metaPrefixPlan, ok := makeVP8LMetaPrefixColorCachePlan(read, mainBounds, mainWidth, mainHeight, bestColorCache, bestColorCache.colorCache.tokens, bestBits); ok {
			best = metaPrefixPlan
		}
	}
	return best
}

func vp8lRankLiteralCandidates(candidates *[vp8lMaxEncodingPlanCandidates]vp8lEncodingPlan, candidateCount int, width int, height int) []vp8lRankedCandidate {
	ranking := make([]vp8lRankedCandidate, candidateCount)
	for i := 0; i < candidateCount; i++ {
		ranking[i] = vp8lRankedCandidate{index: i, bits: vp8lPayloadBits(width, height, candidates[i])}
	}
	vp8lSortRankedCandidates(ranking)
	return ranking
}

func vp8lRankGreedyLZ77Candidates(readPixel pixelReader, bounds image.Rectangle, width int, height int, candidates *[vp8lMaxEncodingPlanCandidates]vp8lEncodingPlan, indices []int, bestBits uint64, cfg vp8lEncodingConfig) []vp8lRankedCandidate {
	screening := cfg
	screening.optimalLZ77Passes = 0
	screening.tryColorCache = false
	screening.tryLZ77ColorCache = false
	screening.tryLZ77MetaPrefix = false
	screening.tryLZ77TokenMetaPrefix = false
	ranking := make([]vp8lRankedCandidate, 0, len(indices))
	for _, index := range indices {
		candidate := candidates[index]
		candidateBits := vp8lPayloadBits(width, height, candidate)
		if !vp8lShouldTryCandidateLZ77(candidate, candidateBits, bestBits) {
			continue
		}
		lz77Plan, ok := makeVP8LLZ77PlanConfig(readPixel, bounds, width, height, candidate, ^uint64(0), screening)
		if !ok {
			continue
		}
		ranking = append(ranking, vp8lRankedCandidate{index: index, bits: vp8lPayloadBits(width, height, lz77Plan)})
	}
	vp8lSortRankedCandidates(ranking)
	return ranking
}

func vp8lSortRankedCandidates(ranking []vp8lRankedCandidate) {
	sort.SliceStable(ranking, func(i int, j int) bool {
		if ranking[i].bits != ranking[j].bits {
			return ranking[i].bits < ranking[j].bits
		}
		return ranking[i].index < ranking[j].index
	})
}

func vp8lTopCandidateIndices(ranking []vp8lRankedCandidate, limit int) []int {
	if limit < 1 {
		limit = 1
	}
	if limit > len(ranking) {
		limit = len(ranking)
	}
	indices := make([]int, limit)
	for i := range indices {
		indices[i] = ranking[i].index
	}
	return indices
}

func vp8lAppendFirstRankedCandidate(indices []int, ranking []vp8lRankedCandidate, candidates *[vp8lMaxEncodingPlanCandidates]vp8lEncodingPlan, matches func(vp8lEncodingPlan) bool) []int {
	for _, ranked := range ranking {
		if !matches(candidates[ranked.index]) || vp8lContainsCandidateIndex(indices, ranked.index) {
			continue
		}
		return append(indices, ranked.index)
	}
	return indices
}

func vp8lAppendRankedCandidates(indices []int, ranking []vp8lRankedCandidate, candidates *[vp8lMaxEncodingPlanCandidates]vp8lEncodingPlan, matches func(vp8lEncodingPlan) bool) []int {
	for _, ranked := range ranking {
		if !matches(candidates[ranked.index]) || vp8lContainsCandidateIndex(indices, ranked.index) {
			continue
		}
		indices = append(indices, ranked.index)
	}
	return indices
}

func vp8lSelectDiverseCandidateIndices(ranking []vp8lRankedCandidate, candidates *[vp8lMaxEncodingPlanCandidates]vp8lEncodingPlan, limit int) []int {
	if limit < 1 {
		limit = 1
	}
	indices := make([]int, 0, limit+3)
	for _, ranked := range ranking {
		if len(indices) == limit {
			break
		}
		indices = append(indices, ranked.index)
	}
	appendFirst := func(matches func(vp8lEncodingPlan) bool) {
		for _, ranked := range ranking {
			if !matches(candidates[ranked.index]) || vp8lContainsCandidateIndex(indices, ranked.index) {
				continue
			}
			indices = append(indices, ranked.index)
			return
		}
	}
	appendFirst(func(plan vp8lEncodingPlan) bool { return !vp8lPlanUsesTransform(plan) })
	appendFirst(func(plan vp8lEncodingPlan) bool { return plan.predictor })
	appendFirst(func(plan vp8lEncodingPlan) bool { return plan.colorTransform })
	appendFirst(func(plan vp8lEncodingPlan) bool { return plan.subtractGreen })
	appendFirst(func(plan vp8lEncodingPlan) bool { return plan.colorIndexing })
	return indices
}

func vp8lContainsCandidateIndex(indices []int, target int) bool {
	for _, index := range indices {
		if index == target {
			return true
		}
	}
	return false
}
