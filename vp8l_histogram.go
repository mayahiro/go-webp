package webp

import "image"

func makeVP8LTokenMetaPrefixPlan(tokens []vp8lToken, width int, height int, prefixBits uint8, baseAnalysis imageAnalysis) (*vp8lMetaPrefixPlan, bool) {
	prefixWidth, prefixHeight := vp8lMetaPrefixImageDimensions(width, height, prefixBits)
	prefixBlocks := prefixWidth * prefixHeight
	if prefixBlocks < 2 || prefixBlocks > vp8lMaxMetaPrefixBlocks {
		return nil, false
	}

	tileGroups, tileTokens, ok := vp8lBuildLZ77TileHistograms(tokens, width, height, prefixBits, prefixWidth, prefixBlocks, baseAnalysis)
	if !ok {
		return nil, false
	}
	groups, groupImage, groupTokens, ok := vp8lClusterLZ77TileHistograms(tileGroups, tileTokens, prefixWidth, prefixHeight)
	if !ok || len(groups) < 2 {
		return nil, false
	}

	prefixBounds := image.Rect(0, 0, prefixWidth, prefixHeight)
	groupImageAnalysis := analyzeImage(vp8lMetaPrefixImageReader(groupImage, prefixWidth), prefixBounds)
	groupAnalyses := make([]imageAnalysis, len(groups))
	for i := range groups {
		groupAnalyses[i] = groups[i].literalAnalysis
	}
	return &vp8lMetaPrefixPlan{
		prefixBits:    prefixBits,
		width:         prefixWidth,
		height:        prefixHeight,
		image:         groupImage,
		imageAnalysis: groupImageAnalysis,
		groups:        groupAnalyses,
		lz77Groups:    groups,
		groupTokens:   groupTokens,
	}, true
}

func vp8lBuildLZ77TileHistograms(tokens []vp8lToken, width int, height int, prefixBits uint8, prefixWidth int, prefixBlocks int, baseAnalysis imageAnalysis) ([]vp8lLZ77GroupPlan, []int, bool) {
	if width <= 0 || height <= 0 || len(tokens) == 0 {
		return nil, nil, false
	}
	total := width * height
	groups := make([]vp8lLZ77GroupPlan, prefixBlocks)
	groupTokens := make([]int, prefixBlocks)
	observers := make([]vp8lLiteralAnalysisObserver, prefixBlocks)
	initialized := make([]bool, prefixBlocks)
	pos := 0
	for _, token := range tokens {
		if pos >= total || token.colorCache {
			return nil, nil, false
		}
		groupIndex := vp8lMetaPrefixIndex(pos%width, pos/width, prefixBits, prefixWidth)
		if groupIndex < 0 || groupIndex >= len(groups) {
			return nil, nil, false
		}
		if !initialized[groupIndex] {
			observers[groupIndex] = newVP8LLiteralAnalysisObserver(baseAnalysis)
			initialized[groupIndex] = true
		}
		group := &groups[groupIndex]
		groupTokens[groupIndex]++
		if token.copyLength > 0 {
			if token.copyLength < vp8lMinBackwardRefLength || pos+token.copyLength > total {
				return nil, nil, false
			}
			lengthPrefix := vp8lPrefixCode(token.copyLength)
			distancePrefix := vp8lDistancePrefixCode(token.distanceCode)
			group.greenCounts[nLiteralCodes+lengthPrefix.code]++
			group.distanceCounts[distancePrefix.code]++
			pos += token.copyLength
			continue
		}
		observers[groupIndex].observePixel(token.pixel)
		group.greenCounts[token.pixel.G]++
		pos++
	}
	if pos != total {
		return nil, nil, false
	}
	for i := range groups {
		if groupTokens[i] == 0 {
			continue
		}
		if !vp8lFinalizeLZ77Histogram(&groups[i], observers[i].result()) {
			return nil, nil, false
		}
	}
	return groups, groupTokens, true
}

func vp8lClusterLZ77TileHistograms(tileGroups []vp8lLZ77GroupPlan, tileTokens []int, prefixWidth int, prefixHeight int) ([]vp8lLZ77GroupPlan, []uint16, []int, bool) {
	if len(tileGroups) != len(tileTokens) || len(tileGroups) != prefixWidth*prefixHeight {
		return nil, nil, nil, false
	}
	groups := make([]vp8lLZ77GroupPlan, 0, minInt(len(tileGroups), vp8lMaxMetaPrefixGroups))
	groupTokens := make([]int, 0, cap(groups))
	groupImage := make([]uint16, len(tileGroups))
	nonEmpty := make([]bool, len(tileGroups))

	for tileIndex, tokenCount := range tileTokens {
		if tokenCount == 0 {
			continue
		}
		nonEmpty[tileIndex] = true
		tileGroup := tileGroups[tileIndex]
		bestIndex := -1
		bestDelta := int64(0)
		var bestMerged vp8lLZ77GroupPlan
		for i := range groups {
			merged, ok := vp8lMergeLZ77Histograms(groups[i], tileGroup)
			if !ok {
				continue
			}
			delta := vp8lLZ77HistogramMergeDelta(groups[i], tileGroup, merged)
			if bestIndex < 0 || delta < bestDelta {
				bestIndex = i
				bestDelta = delta
				bestMerged = merged
			}
		}

		groupIndex := bestIndex
		if len(groups) < vp8lMaxMetaPrefixGroups && (bestIndex < 0 || bestDelta > 0) {
			groupIndex = len(groups)
			groups = append(groups, tileGroup)
			groupTokens = append(groupTokens, tokenCount)
		} else {
			if bestIndex < 0 {
				return nil, nil, nil, false
			}
			groups[bestIndex] = bestMerged
			groupTokens[bestIndex] += tokenCount
		}
		groupImage[tileIndex] = uint16(groupIndex)
	}
	if len(groups) == 0 {
		return nil, nil, nil, false
	}

	for y := 0; y < prefixHeight; y++ {
		for x := 0; x < prefixWidth; x++ {
			index := y*prefixWidth + x
			if nonEmpty[index] {
				continue
			}
			switch {
			case x > 0:
				groupImage[index] = groupImage[index-1]
			case y > 0:
				groupImage[index] = groupImage[index-prefixWidth]
			default:
				groupImage[index] = 0
			}
		}
	}
	if vp8lMetaPrefixReferencedGroupCount(groupImage) != len(groups) {
		return nil, nil, nil, false
	}
	groups, groupImage, groupTokens = vp8lRefineLZ77HistogramClusters(groups, groupImage, groupTokens, prefixWidth, prefixHeight)
	return groups, groupImage, groupTokens, true
}

func vp8lRefineLZ77HistogramClusters(groups []vp8lLZ77GroupPlan, groupImage []uint16, groupTokens []int, width int, height int) ([]vp8lLZ77GroupPlan, []uint16, []int) {
	if len(groups) < 2 || len(groups) != len(groupTokens) || len(groupImage) != width*height {
		return groups, groupImage, groupTokens
	}
	bestCost := vp8lLZ77HistogramClusterCost(groups, groupImage, width, height)
	for len(groups) > 1 {
		bestLeft := -1
		bestRight := -1
		candidateCost := bestCost
		var bestMerged vp8lLZ77GroupPlan
		for left := 0; left < len(groups)-1; left++ {
			for right := left + 1; right < len(groups); right++ {
				merged, ok := vp8lMergeLZ77Histograms(groups[left], groups[right])
				if !ok {
					continue
				}
				trialGroups := vp8lMergedHistogramGroups(groups, left, right, merged)
				trialImage := vp8lMergedHistogramImage(groupImage, left, right)
				cost := vp8lLZ77HistogramClusterCost(trialGroups, trialImage, width, height)
				if cost < candidateCost {
					candidateCost = cost
					bestLeft = left
					bestRight = right
					bestMerged = merged
				}
			}
		}
		if bestLeft < 0 {
			break
		}
		groups = vp8lMergedHistogramGroups(groups, bestLeft, bestRight, bestMerged)
		groupImage = vp8lMergedHistogramImage(groupImage, bestLeft, bestRight)
		groupTokens[bestLeft] += groupTokens[bestRight]
		groupTokens = append(groupTokens[:bestRight], groupTokens[bestRight+1:]...)
		bestCost = candidateCost
	}
	return groups, groupImage, groupTokens
}

func vp8lLZ77HistogramClusterCost(groups []vp8lLZ77GroupPlan, groupImage []uint16, width int, height int) uint64 {
	bits := vp8lImageDataBits(width, height, analyzeImage(vp8lMetaPrefixImageReader(groupImage, width), image.Rect(0, 0, width, height)), false)
	for _, group := range groups {
		bits += vp8lLZ77GroupTreeAndDataBits(group)
	}
	return bits
}

func vp8lMergedHistogramGroups(groups []vp8lLZ77GroupPlan, left int, right int, merged vp8lLZ77GroupPlan) []vp8lLZ77GroupPlan {
	result := make([]vp8lLZ77GroupPlan, 0, len(groups)-1)
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

func vp8lMergedHistogramImage(groupImage []uint16, left int, right int) []uint16 {
	result := make([]uint16, len(groupImage))
	for i, group := range groupImage {
		id := int(group)
		if id == right {
			id = left
		} else if id > right {
			id--
		}
		result[i] = uint16(id)
	}
	return result
}

func vp8lMergeLZ77Histograms(a vp8lLZ77GroupPlan, b vp8lLZ77GroupPlan) (vp8lLZ77GroupPlan, bool) {
	var merged vp8lLZ77GroupPlan
	for i := range merged.greenCounts {
		merged.greenCounts[i] = a.greenCounts[i] + b.greenCounts[i]
	}
	for i := range merged.distanceCounts {
		merged.distanceCounts[i] = a.distanceCounts[i] + b.distanceCounts[i]
	}
	return merged, vp8lFinalizeLZ77Histogram(&merged, a.literalAnalysis.merge(b.literalAnalysis))
}

func vp8lFinalizeLZ77Histogram(group *vp8lLZ77GroupPlan, literalAnalysis imageAnalysis) bool {
	greenLengths, ok := huffmanCodeLengths(group.greenCounts)
	if !ok {
		return false
	}
	group.literalAnalysis = literalAnalysis
	group.greenLengths = greenLengths
	group.greenCodes = canonicalCodes(greenLengths)
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

func vp8lLZ77HistogramMergeDelta(a vp8lLZ77GroupPlan, b vp8lLZ77GroupPlan, merged vp8lLZ77GroupPlan) int64 {
	before := vp8lLZ77GroupTreeAndDataBits(a) + vp8lLZ77GroupTreeAndDataBits(b)
	after := vp8lLZ77GroupTreeAndDataBits(merged)
	if after >= before {
		return int64(after - before)
	}
	return -int64(before - after)
}
