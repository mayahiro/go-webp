package webp

import (
	"image"
	"image/color"
)

type vp8lColorCacheStatistics struct {
	literalAnalysis imageAnalysis
	greenCounts     []uint32
	cacheHits       int
	tokenCount      int
}

func vp8lBuildColorCacheStatistics(readPixel pixelReader, bounds image.Rectangle, width int) [vp8lMaxColorCacheBits + 1]vp8lColorCacheStatistics {
	var results [vp8lMaxColorCacheBits + 1]vp8lColorCacheStatistics
	var caches [vp8lMaxColorCacheBits + 1][]color.NRGBA
	var firstLiteral [vp8lMaxColorCacheBits + 1]bool
	for bits := uint8(vp8lMinColorCacheBits); bits <= vp8lMaxColorCacheBits; bits++ {
		results[bits].greenCounts = make([]uint32, nLiteralCodes+nLengthCodes+1<<bits)
		caches[bits] = make([]color.NRGBA, 1<<bits)
		firstLiteral[bits] = true
	}
	total := bounds.Dx() * bounds.Dy()
	for pos := 0; pos < total; pos++ {
		pixel := vp8lPixelAt(readPixel, bounds, width, pos)
		for bits := uint8(vp8lMinColorCacheBits); bits <= vp8lMaxColorCacheBits; bits++ {
			result := &results[bits]
			index := vp8lColorCacheIndex(pixel, bits)
			result.tokenCount++
			if caches[bits][index] == pixel {
				result.greenCounts[nLiteralCodes+nLengthCodes+index]++
				result.cacheHits++
				continue
			}
			observeVP8LLiteral(&result.literalAnalysis, &firstLiteral[bits], pixel)
			result.greenCounts[pixel.G]++
			caches[bits][index] = pixel
		}
	}
	for bits := uint8(vp8lMinColorCacheBits); bits <= vp8lMaxColorCacheBits; bits++ {
		if firstLiteral[bits] {
			results[bits].literalAnalysis = emptyVP8LLiteralAnalysis()
		} else {
			results[bits].literalAnalysis.finalizeChannels()
		}
	}
	return results
}

func vp8lBuildLZ77ColorCacheStatistics(readPixel pixelReader, bounds image.Rectangle, width int, lz77Tokens []vp8lToken, baseAnalysis imageAnalysis) [vp8lMaxColorCacheBits + 1]vp8lColorCacheStatistics {
	var results [vp8lMaxColorCacheBits + 1]vp8lColorCacheStatistics
	var caches [vp8lMaxColorCacheBits + 1][]color.NRGBA
	var observers [vp8lMaxColorCacheBits + 1]vp8lLiteralAnalysisObserver
	var hasLiteral [vp8lMaxColorCacheBits + 1]bool
	for bits := uint8(vp8lMinColorCacheBits); bits <= vp8lMaxColorCacheBits; bits++ {
		results[bits].greenCounts = make([]uint32, nLiteralCodes+nLengthCodes+1<<bits)
		caches[bits] = make([]color.NRGBA, 1<<bits)
		observers[bits] = newVP8LLiteralAnalysisObserver(baseAnalysis)
	}
	pos := 0
	for _, token := range lz77Tokens {
		if token.copyLength > 0 {
			lengthPrefix := vp8lPrefixCode(token.copyLength)
			for bits := uint8(vp8lMinColorCacheBits); bits <= vp8lMaxColorCacheBits; bits++ {
				results[bits].greenCounts[nLiteralCodes+lengthPrefix.code]++
				results[bits].tokenCount++
			}
			for i := 0; i < token.copyLength; i++ {
				pixel := vp8lPixelAt(readPixel, bounds, width, pos+i)
				for bits := uint8(vp8lMinColorCacheBits); bits <= vp8lMaxColorCacheBits; bits++ {
					caches[bits][vp8lColorCacheIndex(pixel, bits)] = pixel
				}
			}
			pos += token.copyLength
			continue
		}
		pixel := vp8lPixelAt(readPixel, bounds, width, pos)
		for bits := uint8(vp8lMinColorCacheBits); bits <= vp8lMaxColorCacheBits; bits++ {
			result := &results[bits]
			result.tokenCount++
			index := vp8lColorCacheIndex(pixel, bits)
			if caches[bits][index] == pixel {
				result.greenCounts[nLiteralCodes+nLengthCodes+index]++
				result.cacheHits++
				continue
			}
			hasLiteral[bits] = true
			observers[bits].observePixel(pixel)
			result.greenCounts[pixel.G]++
			caches[bits][index] = pixel
		}
		pos++
	}
	for bits := uint8(vp8lMinColorCacheBits); bits <= vp8lMaxColorCacheBits; bits++ {
		if hasLiteral[bits] {
			results[bits].literalAnalysis = observers[bits].result()
		} else {
			results[bits].literalAnalysis = emptyVP8LLiteralAnalysis()
		}
	}
	return results
}

type vp8lLZ77ColorCacheStatistics struct {
	literalAnalysis imageAnalysis
	greenCounts     []uint32
	distanceCounts  [nDistanceCodes]uint32
	copyCount       int
}

func vp8lAnalyzeLZ77ColorCacheTokens(tokens []vp8lToken, baseAnalysis imageAnalysis, bits uint8) (vp8lLZ77ColorCacheStatistics, bool) {
	stats := vp8lLZ77ColorCacheStatistics{
		greenCounts: make([]uint32, nLiteralCodes+nLengthCodes+1<<bits),
	}
	observer := newVP8LLiteralAnalysisObserver(baseAnalysis)
	hasLiteral := false
	for _, token := range tokens {
		switch {
		case token.copyLength > 0:
			if token.copyLength < vp8lMinBackwardRefLength {
				return vp8lLZ77ColorCacheStatistics{}, false
			}
			lengthPrefix := vp8lPrefixCode(token.copyLength)
			distancePrefix := vp8lDistancePrefixCode(token.distanceCode)
			stats.greenCounts[nLiteralCodes+lengthPrefix.code]++
			stats.distanceCounts[distancePrefix.code]++
			stats.copyCount++
		case token.colorCache:
			if token.cacheIndex < 0 || token.cacheIndex >= 1<<bits {
				return vp8lLZ77ColorCacheStatistics{}, false
			}
			stats.greenCounts[nLiteralCodes+nLengthCodes+token.cacheIndex]++
		default:
			hasLiteral = true
			observer.observePixel(token.pixel)
			stats.greenCounts[token.pixel.G]++
		}
	}
	if hasLiteral {
		stats.literalAnalysis = observer.result()
	} else {
		stats.literalAnalysis = emptyVP8LLiteralAnalysis()
	}
	return stats, true
}

func vp8lPlanWithLZ77ColorCache(base vp8lEncodingPlan, tokens []vp8lToken, bits uint8, stats vp8lLZ77ColorCacheStatistics) (vp8lEncodingPlan, bool) {
	if stats.copyCount == 0 {
		return vp8lEncodingPlan{}, false
	}
	greenLengths, ok := huffmanColorCacheCodeLengths(stats.greenCounts)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	distanceN, distanceSymbols, distanceLengths, distanceCodes, distanceNormal, ok := vp8lDistanceCodeFor(stats.distanceCounts)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	base.lz77 = true
	base.lz77Tokens = nil
	base.lz77DistanceCounts = stats.distanceCounts
	base.lz77DistanceN = distanceN
	base.lz77DistanceSymbols = distanceSymbols
	base.lz77DistanceLengths = distanceLengths
	base.lz77DistanceCodes = distanceCodes
	base.lz77DistanceNormal = distanceNormal
	base.colorCache = &vp8lColorCachePlan{
		bits:     bits,
		tokens:   tokens,
		analysis: stats.literalAnalysis,
		counts:   stats.greenCounts,
		lengths:  greenLengths,
		codes:    canonicalColorCacheCodes(greenLengths),
	}
	return base, true
}

func vp8lPrepareColorCacheIndices(source vp8lLZ77Source, bits uint8, workspace *vp8lLZ77Workspace) []int32 {
	indices := workspace.resetColorCacheIndices(source.total)
	cache := make([]uint32, 1<<bits)
	for pos := 0; pos < source.total; pos++ {
		pixel := vp8lPixelAt(source.readPixel, source.bounds, source.width, pos)
		packed := vp8lPackPixel(pixel)
		index := vp8lColorCacheIndex(pixel, bits)
		if cache[index] == packed {
			indices[pos] = int32(index)
		}
		cache[index] = packed
	}
	return indices
}

func vp8lLZ77CostModelForColorCachePlan(plan vp8lEncodingPlan, cacheIndices []int32) vp8lLZ77CostModel {
	model := vp8lLZ77CostModelForPlan(plan)
	model.literalAnalysis = plan.colorCache.analysis
	model.colorCacheCounts = plan.colorCache.counts
	model.colorCacheLengths = plan.colorCache.lengths
	model.colorCacheIndices = cacheIndices
	return model
}

func vp8lOptimizeLZ77ColorCachePlan(source vp8lLZ77Source, width int, height int, literalBase vp8lEncodingPlan, seed vp8lEncodingPlan, candidateCount int, passes int, workspace *vp8lLZ77Workspace, tokenPassOffset int) (vp8lEncodingPlan, bool) {
	if seed.colorCache == nil || passes <= 0 {
		return seed, false
	}
	cacheIndices := vp8lPrepareColorCacheIndices(source, seed.colorCache.bits, workspace)
	best := seed
	bestBits := vp8lPayloadBits(width, height, seed)
	model := seed
	improved := false
	maxMatchLength := vp8lOptimalMatchLengthLimit(literalBase)
	paretoMatches := literalBase.colorIndexing && len(literalBase.colorTable) <= 8
	for pass := 0; pass < passes; pass++ {
		tokens, ok := vp8lBuildOptimalLZ77Workspace(
			source.readPixel,
			source.bounds,
			source.width,
			candidateCount,
			maxMatchLength,
			paretoMatches,
			vp8lLZ77CostModelForColorCachePlan(model, cacheIndices),
			workspace,
			tokenPassOffset+pass,
			len(model.colorCache.tokens),
			source.packed,
			source.matchGraph,
		)
		if !ok || vp8lLZ77TokensEqual(tokens, model.colorCache.tokens) {
			break
		}
		stats, ok := vp8lAnalyzeLZ77ColorCacheTokens(tokens, literalBase.analysis, seed.colorCache.bits)
		if !ok {
			break
		}
		candidate, ok := vp8lPlanWithLZ77ColorCache(literalBase, tokens, seed.colorCache.bits, stats)
		if !ok {
			break
		}
		candidateBits := vp8lPayloadBits(width, height, candidate)
		if candidateBits < bestBits {
			best = candidate
			bestBits = candidateBits
			improved = true
		}
		model = candidate
	}
	if improved {
		cache := *best.colorCache
		cache.tokens = append([]vp8lToken(nil), cache.tokens...)
		best.colorCache = &cache
	}
	return best, improved
}
