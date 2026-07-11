package webp

import "image"

const vp8lMaxColorCacheHistogramMerges = 8

func vp8lRefineMetaPrefixColorCacheGroups(metaPrefix *vp8lMetaPrefixPlan, lz77 bool) {
	if metaPrefix == nil || len(metaPrefix.colorCacheGroups) < 2 {
		return
	}
	groups := metaPrefix.colorCacheGroups
	groupImage := metaPrefix.image
	groupTokens := metaPrefix.groupTokens
	groupPixels := metaPrefix.groupPixels
	prefixAnalysis := metaPrefix.imageAnalysis
	imageCounts := vp8lMetaPrefixGroupCounts(groupImage, len(groups))
	scratch := newVP8LColorCacheMergeCostScratch(len(groups[0].counts))
	imageBits := scratch.metaPrefixImageBits(imageCounts)
	cost := imageBits + vp8lColorCacheGroupsBits(groups, lz77)

	for range vp8lMaxColorCacheHistogramMerges {
		bestLeft, bestRight := -1, -1
		bestCost := cost
		var bestImageBits uint64
		for left := 0; left < len(groups)-1; left++ {
			leftBits := vp8lColorCacheGroupTreeAndDataBits(groups[left], len(groups[left].counts), lz77)
			for right := left + 1; right < len(groups); right++ {
				mergedBits, ok := scratch.mergedGroupBits(groups[left], groups[right], lz77)
				if !ok {
					continue
				}
				rightBits := vp8lColorCacheGroupTreeAndDataBits(groups[right], len(groups[right].counts), lz77)
				trialImageBits := scratch.mergedMetaPrefixImageBits(imageCounts, left, right)
				trialCost := cost - imageBits - leftBits - rightBits + trialImageBits + mergedBits
				if trialCost >= bestCost {
					continue
				}
				bestLeft = left
				bestRight = right
				bestCost = trialCost
				bestImageBits = trialImageBits
			}
		}
		if bestLeft < 0 {
			break
		}
		bestMerged, ok := vp8lMergeColorCacheGroups(groups[bestLeft], groups[bestRight])
		if !ok {
			break
		}
		groups = vp8lMergedColorCacheGroups(groups, bestLeft, bestRight, bestMerged)
		groupImage = vp8lMergedHistogramImage(groupImage, bestLeft, bestRight)
		prefixAnalysis = analyzeImage(
			vp8lMetaPrefixImageReader(groupImage, metaPrefix.width),
			image.Rect(0, 0, metaPrefix.width, metaPrefix.height),
		)
		imageCounts[bestLeft] += imageCounts[bestRight]
		imageCounts = append(imageCounts[:bestRight], imageCounts[bestRight+1:]...)
		imageBits = bestImageBits
		cost = bestCost
		if len(groupTokens) == len(groups)+1 {
			groupTokens[bestLeft] += groupTokens[bestRight]
			groupTokens = append(groupTokens[:bestRight], groupTokens[bestRight+1:]...)
		}
		if len(groupPixels) == len(groups)+1 {
			groupPixels[bestLeft] += groupPixels[bestRight]
			groupPixels = append(groupPixels[:bestRight], groupPixels[bestRight+1:]...)
		}
	}

	metaPrefix.image = groupImage
	metaPrefix.imageAnalysis = prefixAnalysis
	metaPrefix.colorCacheGroups = groups
	metaPrefix.groupTokens = groupTokens
	metaPrefix.groupPixels = groupPixels
	metaPrefix.groups = make([]imageAnalysis, len(groups))
	for i := range groups {
		metaPrefix.groups[i] = groups[i].literalAnalysis
	}
}

type vp8lColorCacheMergeCostScratch struct {
	greenCounts  []uint32
	greenLengths []uint8
	greenNodes   []huffmanNode
	greenActive  []int
	imageCounts  [nLiteralCodes + nLengthCodes]uint32
	imageLengths [nLiteralCodes + nLengthCodes]uint8
	huffman      alphaHuffmanScratch
}

func newVP8LColorCacheMergeCostScratch(greenLimit int) vp8lColorCacheMergeCostScratch {
	return vp8lColorCacheMergeCostScratch{
		greenCounts:  make([]uint32, greenLimit),
		greenLengths: make([]uint8, greenLimit),
		greenNodes:   make([]huffmanNode, 0, greenLimit*2-1),
		greenActive:  make([]int, 0, greenLimit),
	}
}

func (s *vp8lColorCacheMergeCostScratch) mergedGroupBits(a vp8lColorCacheGroupPlan, b vp8lColorCacheGroupPlan, lz77 bool) (uint64, bool) {
	if len(a.counts) == 0 || len(a.counts) != len(b.counts) || len(a.counts) != len(s.greenCounts) {
		return 0, false
	}
	for i := range s.greenCounts {
		s.greenCounts[i] = a.counts[i] + b.counts[i]
	}
	if !huffmanCodeLengthsIntoWorkspace(
		s.greenLengths,
		s.greenCounts,
		s.greenNodes[:0],
		s.greenActive[:0],
		15,
		true,
	) {
		return 0, false
	}

	bits := alphaNormalTreeBits(s.greenLengths)
	literalCount := 0
	for symbol, count := range s.greenCounts {
		if count == 0 {
			continue
		}
		bits += uint64(count) * uint64(s.greenLengths[symbol])
		if symbol < nLiteralCodes {
			literalCount += int(count)
		} else if symbol < nLiteralCodes+nLengthCodes {
			bits += uint64(count) * uint64(vp8lLengthPrefixExtraBits(symbol-nLiteralCodes))
		}
	}
	aAnalysis := vp8lColorCacheLiteralAnalysisWithTokenCount(a)
	bAnalysis := vp8lColorCacheLiteralAnalysisWithTokenCount(b)
	literalAnalysis := aAnalysis.merge(bAnalysis)
	for channel := 1; channel < len(literalAnalysis.channels); channel++ {
		bits += channelTreeAndDataBits(literalAnalysis.channels[channel], nLiteralCodes, literalCount)
	}

	if !lz77 {
		return bits + simpleTreeBits(0), true
	}
	var distanceCounts [nDistanceCodes]uint32
	for i := range distanceCounts {
		distanceCounts[i] = a.distanceCounts[i] + b.distanceCounts[i]
	}
	distanceN, distanceSymbols, distanceLengths, _, distanceNormal, ok := vp8lDistanceCodeFor(distanceCounts)
	if !ok && !vp8lDistanceCountsEmpty(distanceCounts) {
		return 0, false
	}
	distanceGroup := vp8lColorCacheGroupPlan{
		distanceCounts:  distanceCounts,
		distanceN:       distanceN,
		distanceSymbols: distanceSymbols,
		distanceLengths: distanceLengths,
		distanceNormal:  distanceNormal,
	}
	return bits + vp8lColorCacheGroupDistanceTreeBits(distanceGroup) + vp8lColorCacheGroupDistanceDataBits(distanceGroup), true
}

func (s *vp8lColorCacheMergeCostScratch) metaPrefixImageBits(groupCounts []uint32) uint64 {
	clear(s.imageCounts[:])
	for group, count := range groupCounts {
		s.imageCounts[group] = count
	}
	return s.metaPrefixImageBitsForCounts()
}

func (s *vp8lColorCacheMergeCostScratch) mergedMetaPrefixImageBits(groupCounts []uint32, left int, right int) uint64 {
	clear(s.imageCounts[:])
	for group, count := range groupCounts {
		switch {
		case group == right:
			s.imageCounts[left] += count
		case group > right:
			s.imageCounts[group-1] += count
		default:
			s.imageCounts[group] += count
		}
	}
	return s.metaPrefixImageBitsForCounts()
}

func (s *vp8lColorCacheMergeCostScratch) metaPrefixImageBitsForCounts() uint64 {
	greenBits := vp8lTreeAndDataBitsForCounts(s.imageCounts[:], s.imageLengths[:], &s.huffman)
	return 1 + greenBits + simpleTreeBits(0) + simpleTreeBits(0) + simpleTreeBits(255) + simpleTreeBits(0)
}

func vp8lTreeAndDataBitsForCounts(counts []uint32, lengths []uint8, scratch *alphaHuffmanScratch) uint64 {
	total := uint64(0)
	symbolCount := 0
	firstSymbol := 0
	for symbol, count := range counts {
		if count == 0 {
			continue
		}
		if symbolCount == 0 {
			firstSymbol = symbol
		}
		symbolCount++
		total += uint64(count)
	}
	switch symbolCount {
	case 0:
		return simpleTreeBits(0)
	case 1:
		return simpleTreeBits(uint8(firstSymbol))
	case 2:
		return alphaTwoSymbolTreeBits(uint8(firstSymbol)) + total
	}
	if !huffmanCodeLengthsIntoScratch(lengths, counts, scratch) {
		return full8TreeBits(len(counts)) + total*8
	}
	normalBits := alphaNormalTreeBits(lengths)
	for symbol, count := range counts {
		normalBits += uint64(count) * uint64(lengths[symbol])
	}
	fullBits := full8TreeBits(len(counts)) + total*8
	if normalBits < fullBits {
		return normalBits
	}
	return fullBits
}

func vp8lMetaPrefixGroupCounts(groupImage []uint16, groupCount int) []uint32 {
	counts := make([]uint32, groupCount)
	for _, group := range groupImage {
		counts[group]++
	}
	return counts
}

func vp8lColorCacheGroupsBits(groups []vp8lColorCacheGroupPlan, lz77 bool) uint64 {
	var bits uint64
	for _, group := range groups {
		bits += vp8lColorCacheGroupTreeAndDataBits(group, len(group.counts), lz77)
	}
	return bits
}

func vp8lMergeColorCacheGroups(a vp8lColorCacheGroupPlan, b vp8lColorCacheGroupPlan) (vp8lColorCacheGroupPlan, bool) {
	if len(a.counts) == 0 || len(a.counts) != len(b.counts) {
		return vp8lColorCacheGroupPlan{}, false
	}
	merged := vp8lColorCacheGroupPlan{
		counts: make([]uint32, len(a.counts)),
	}
	for i := range merged.counts {
		merged.counts[i] = a.counts[i] + b.counts[i]
	}
	for i := range merged.distanceCounts {
		merged.distanceCounts[i] = a.distanceCounts[i] + b.distanceCounts[i]
	}
	aAnalysis := vp8lColorCacheLiteralAnalysisWithTokenCount(a)
	bAnalysis := vp8lColorCacheLiteralAnalysisWithTokenCount(b)
	if !vp8lFinalizeColorCacheGroup(&merged, aAnalysis.merge(bAnalysis)) {
		return vp8lColorCacheGroupPlan{}, false
	}
	return merged, true
}

func vp8lColorCacheLiteralAnalysisWithTokenCount(group vp8lColorCacheGroupPlan) imageAnalysis {
	analysis := group.literalAnalysis
	literalCount := vp8lColorCacheGroupLiteralTokenCount(group)
	for channel := 1; channel < len(analysis.channels); channel++ {
		if !analysis.channels[channel].constant {
			continue
		}
		analysis.channels[channel].counts[0] = uint32(literalCount)
		analysis.channels[channel].total = literalCount
	}
	return analysis
}

func vp8lFinalizeColorCacheGroup(group *vp8lColorCacheGroupPlan, literalAnalysis imageAnalysis) bool {
	lengths, ok := huffmanColorCacheCodeLengths(group.counts)
	if !ok {
		return false
	}
	group.literalAnalysis = literalAnalysis
	group.lengths = lengths
	group.codes = canonicalColorCacheCodes(lengths)
	if vp8lDistanceCountsEmpty(group.distanceCounts) {
		return true
	}
	distanceN, distanceSymbols, distanceLengths, distanceCodes, distanceNormal, ok := vp8lDistanceCodeFor(group.distanceCounts)
	if !ok {
		return false
	}
	group.distanceN = distanceN
	group.distanceSymbols = distanceSymbols
	group.distanceLengths = distanceLengths
	group.distanceCodes = distanceCodes
	group.distanceNormal = distanceNormal
	return true
}

func vp8lMergedColorCacheGroups(groups []vp8lColorCacheGroupPlan, left int, right int, merged vp8lColorCacheGroupPlan) []vp8lColorCacheGroupPlan {
	result := make([]vp8lColorCacheGroupPlan, 0, len(groups)-1)
	for i, group := range groups {
		switch i {
		case left:
			result = append(result, merged)
		case right:
		default:
			result = append(result, group)
		}
	}
	return result
}
