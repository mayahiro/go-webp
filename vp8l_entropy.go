package webp

import (
	"math"
	"slices"
)

const (
	vp8lAlphabetGreen uint32 = iota
	vp8lAlphabetRed
	vp8lAlphabetBlue
	vp8lAlphabetAlpha
	vp8lAlphabetDistance
	vp8lMaxEntropyGroups = 16
)

type vp8lHistogramEntry struct {
	key   uint32
	count uint32
}

type vp8lSparseHistogram []vp8lHistogramEntry

type vp8lDenseHistogram struct {
	green    []uint32
	red      [nLiteralCodes]uint32
	blue     [nLiteralCodes]uint32
	alpha    [nLiteralCodes]uint32
	distance [nDistanceCodes]uint32
}

type vp8lHistogramFeature [12]float64

func vp8lChooseEntropyPlan(base vp8lImagePlan, budget vp8lBudget) vp8lImagePlan {
	return vp8lChooseEntropyPlanWorkspace(base, budget, nil)
}

func vp8lChooseEntropyPlanWorkspace(base vp8lImagePlan, budget vp8lBudget, workspace *vp8lSearchWorkspace) vp8lImagePlan {
	if base.meta != nil || !budget.tryMetaPrefix || len(base.tokens) < 64 {
		return base
	}
	best := base
	bestBits := base.bitLen(true)
	extraBits := vp8lTokenExtraBits(base.tokens)
	var huffmanWorkspace *vp8lHuffmanWorkspace
	if workspace != nil {
		huffmanWorkspace = &workspace.huffman
	}
	for _, prefixBits := range budget.metaPrefixBits {
		tiles, tileWidth, tileHeight := vp8lTileHistogramsWorkspace(base, prefixBits, workspace)
		budget.counters.recordEntropyTiles(len(tiles))
		if len(tiles) < 2 {
			continue
		}
		for groups := 2; groups <= budget.maxEntropyGroups && groups <= len(tiles); groups *= 2 {
			budget.counters.recordEntropyTrial()
			groupMap := vp8lClusterHistogramsWorkspace(tiles, groups, workspace)
			groupMap, codeGroups, groupCosts, greenSize := vp8lRefineHistogramGroups(tiles, groupMap, base.cacheBits, budget.entropyRefinements, huffmanWorkspace)
			budget.counters.recordHuffmanEmissionBuilds(len(codeGroups) * 5)
			if len(codeGroups) < 2 {
				continue
			}
			entropyPixels := make([]uint32, len(groupMap))
			for i, group := range groupMap {
				entropyPixels[i] = 0xff000000 | uint32(group>>8)<<16 | uint32(uint8(group))<<8
			}
			candidate := base
			budget.counters.recordHuffmanEmissionBuilds(5)
			candidate.meta = &vp8lEntropyPlan{
				prefixBits: prefixBits,
				width:      tileWidth,
				height:     tileHeight,
				groupMap:   append([]uint16(nil), groupMap...),
				image:      buildVP8LLiteralImagePlan(entropyPixels, tileWidth, tileHeight),
				groups:     codeGroups,
			}
			candidateBits := vp8lEntropyCandidateBitLen(base.cacheBits, candidate.meta, tiles, extraBits, groupCosts, greenSize)
			if candidateBits < bestBits {
				best = candidate
				bestBits = candidateBits
			}
		}
	}
	return best
}

func vp8lTokenExtraBits(tokens []vp8lToken) uint64 {
	var bits uint64
	for _, token := range tokens {
		if token.kind() != vp8lTokenCopy {
			continue
		}
		bits += uint64(vp8lPrefixCode(token.copyLength()).extraBits)
		bits += uint64(vp8lDistancePrefixCode(token.distanceCode()).extraBits)
	}
	return bits
}

func vp8lEntropyCandidateBitLen(cacheBits uint8, meta *vp8lEntropyPlan, tiles []vp8lSparseHistogram, extraBits uint64, groupCosts []uint8, greenSize int) uint64 {
	bits := uint64(1)
	if cacheBits != 0 {
		bits += 4
	}
	bits += 1 + 3
	bits += meta.image.bitLen(false)
	for i := range meta.groups {
		bits += meta.groups[i].headerBitLen()
	}
	bits += extraBits
	alphabetSize := vp8lHistogramAlphabetSize(greenSize)
	for tileIndex, histogram := range tiles {
		group := int(meta.groupMap[tileIndex])
		bits += vp8lSparseHistogramTableCost(histogram, groupCosts[group*alphabetSize:(group+1)*alphabetSize], greenSize)
	}
	return bits
}

func vp8lTileHistograms(image vp8lImagePlan, prefixBits uint8) ([]vp8lSparseHistogram, int, int) {
	return vp8lTileHistogramsWorkspace(image, prefixBits, nil)
}

func vp8lTileHistogramsWorkspace(image vp8lImagePlan, prefixBits uint8, workspace *vp8lSearchWorkspace) ([]vp8lSparseHistogram, int, int) {
	tileWidth := vp8lDivRoundUp(image.width, 1<<prefixBits)
	tileHeight := vp8lDivRoundUp(image.height, 1<<prefixBits)
	tileCount := tileWidth * tileHeight
	entryCount := 0
	for _, token := range image.tokens {
		entryCount += vp8lTokenHistogramEntryCount(token)
	}
	entryCounts, offsets, cursors, entries, tiles := workspace.resetEntropyHistograms(tileCount, entryCount)
	greenSize := nLiteralCodes + nLengthCodes
	if image.cacheBits != 0 {
		greenSize += 1 << image.cacheBits
	}
	alphabetSize := greenSize + 3*nLiteralCodes + nDistanceCodes
	denseCounts := workspace.resetEntropyDenseCounts(alphabetSize)
	position := 0
	for _, token := range image.tokens {
		x := position % image.width
		y := position / image.width
		tileIndex := (y>>prefixBits)*tileWidth + (x >> prefixBits)
		entryCounts[tileIndex] += vp8lTokenHistogramEntryCount(token)
		position += vp8lTokenPixelLength(token)
	}
	for tile := range tileCount {
		offsets[tile+1] = offsets[tile] + entryCounts[tile]
		cursors[tile] = offsets[tile]
	}
	position = 0
	for _, token := range image.tokens {
		x := position % image.width
		y := position / image.width
		tileIndex := (y>>prefixBits)*tileWidth + (x >> prefixBits)
		written := vp8lWriteTokenHistogramEntries(entries[cursors[tileIndex]:], token)
		cursors[tileIndex] += written
		position += vp8lTokenPixelLength(token)
	}
	for tile := range tileCount {
		start, end := offsets[tile], offsets[tile+1]
		for _, entry := range entries[start:end] {
			denseCounts[vp8lHistogramDenseIndex(entry.key, greenSize)] += entry.count
		}
		write := start
		for index, count := range denseCounts {
			if count == 0 {
				continue
			}
			entries[write] = vp8lHistogramEntry{key: vp8lHistogramKey(index, greenSize), count: count}
			write++
			denseCounts[index] = 0
		}
		tiles[tile] = entries[start:write]
	}
	return tiles, tileWidth, tileHeight
}

func vp8lHistogramDenseIndex(key uint32, greenSize int) int {
	alphabet := int(key >> 16)
	symbol := int(key & 0xffff)
	if alphabet == int(vp8lAlphabetGreen) {
		return symbol
	}
	return greenSize + (alphabet-1)*nLiteralCodes + symbol
}

func vp8lHistogramKey(index int, greenSize int) uint32 {
	if index < greenSize {
		return uint32(index)
	}
	index -= greenSize
	alphabet := 1 + index/nLiteralCodes
	symbol := index % nLiteralCodes
	if alphabet == int(vp8lAlphabetDistance) {
		symbol = index - 3*nLiteralCodes
	}
	return uint32(alphabet)<<16 | uint32(symbol)
}

func vp8lTokenHistogramEntryCount(token vp8lToken) int {
	switch token.kind() {
	case vp8lTokenLiteral:
		return 4
	case vp8lTokenCopy:
		return 2
	case vp8lTokenCache:
		return 1
	}
	return 0
}

func vp8lTokenPixelLength(token vp8lToken) int {
	if token.kind() == vp8lTokenCopy {
		return token.copyLength()
	}
	return 1
}

func vp8lWriteTokenHistogramEntries(entries []vp8lHistogramEntry, token vp8lToken) int {
	entry := func(index int, alphabet uint32, symbol int) {
		entries[index] = vp8lHistogramEntry{key: alphabet<<16 | uint32(symbol), count: 1}
	}
	switch token.kind() {
	case vp8lTokenLiteral:
		pixel := token.literal()
		entry(0, vp8lAlphabetGreen, int(uint8(pixel>>8)))
		entry(1, vp8lAlphabetRed, int(uint8(pixel>>16)))
		entry(2, vp8lAlphabetBlue, int(uint8(pixel)))
		entry(3, vp8lAlphabetAlpha, int(uint8(pixel>>24)))
		return 4
	case vp8lTokenCopy:
		lengthPrefix := vp8lPrefixCode(token.copyLength())
		distancePrefix := vp8lDistancePrefixCode(token.distanceCode())
		entry(0, vp8lAlphabetGreen, nLiteralCodes+lengthPrefix.code)
		entry(1, vp8lAlphabetDistance, distancePrefix.code)
		return 2
	case vp8lTokenCache:
		entry(0, vp8lAlphabetGreen, nLiteralCodes+nLengthCodes+token.cacheIndex())
		return 1
	}
	return 0
}

func vp8lClusterHistograms(tiles []vp8lSparseHistogram, requestedGroups int) []uint16 {
	return vp8lClusterHistogramsWorkspace(tiles, requestedGroups, nil)
}

func vp8lClusterHistogramsWorkspace(tiles []vp8lSparseHistogram, requestedGroups int, workspace *vp8lSearchWorkspace) []uint16 {
	features, nonEmpty, assignments := workspace.resetEntropyClustering(len(tiles))
	for i, tile := range tiles {
		features[i] = vp8lHistogramFeatures(tile)
		if len(tile) != 0 {
			nonEmpty = append(nonEmpty, i)
		}
	}
	if len(nonEmpty) == 0 {
		return make([]uint16, len(tiles))
	}
	groupCount := minInt(requestedGroups, len(nonEmpty))
	centroids := make([]vp8lHistogramFeature, groupCount)
	centroids[0] = features[nonEmpty[0]]
	for group := 1; group < groupCount; group++ {
		bestIndex := nonEmpty[0]
		bestDistance := -1.0
		for _, tileIndex := range nonEmpty {
			distance := math.Inf(1)
			for previous := 0; previous < group; previous++ {
				distance = math.Min(distance, vp8lFeatureDistance(features[tileIndex], centroids[previous]))
			}
			if distance > bestDistance {
				bestDistance = distance
				bestIndex = tileIndex
			}
		}
		centroids[group] = features[bestIndex]
	}
	for range 4 {
		var sums [16]vp8lHistogramFeature
		var counts [16]int
		for i, feature := range features {
			if len(tiles[i]) == 0 {
				if i > 0 {
					assignments[i] = assignments[i-1]
				}
				continue
			}
			bestGroup := 0
			bestDistance := vp8lFeatureDistance(feature, centroids[0])
			for group := 1; group < groupCount; group++ {
				distance := vp8lFeatureDistance(feature, centroids[group])
				if distance < bestDistance {
					bestDistance = distance
					bestGroup = group
				}
			}
			assignments[i] = uint16(bestGroup)
			counts[bestGroup]++
			for dimension, value := range feature {
				sums[bestGroup][dimension] += value
			}
		}
		for group := 0; group < groupCount; group++ {
			if counts[group] == 0 {
				continue
			}
			for dimension := range centroids[group] {
				centroids[group][dimension] = sums[group][dimension] / float64(counts[group])
			}
		}
	}
	return assignments
}

func vp8lHistogramFeatures(histogram vp8lSparseHistogram) vp8lHistogramFeature {
	var feature vp8lHistogramFeature
	var totals [5]uint64
	var sums [5]float64
	var literals, copies, caches uint64
	for _, entry := range histogram {
		alphabet := entry.key >> 16
		symbol := int(entry.key & 0xffff)
		totals[alphabet] += uint64(entry.count)
		if alphabet == vp8lAlphabetGreen {
			switch {
			case symbol < nLiteralCodes:
				literals += uint64(entry.count)
			case symbol < nLiteralCodes+nLengthCodes:
				copies += uint64(entry.count)
			default:
				caches += uint64(entry.count)
			}
		}
	}
	for _, entry := range histogram {
		alphabet := entry.key >> 16
		if totals[alphabet] != 0 && entry.count != 0 {
			probability := float64(entry.count) / float64(totals[alphabet])
			sums[alphabet] -= float64(entry.count) * math.Log2(probability)
		}
	}
	greenTotal := totals[vp8lAlphabetGreen]
	if greenTotal != 0 {
		feature[0] = float64(literals) / float64(greenTotal)
		feature[1] = float64(copies) / float64(greenTotal)
		feature[2] = float64(caches) / float64(greenTotal)
	}
	for alphabet := range totals {
		if totals[alphabet] != 0 {
			feature[3+alphabet] = sums[alphabet] / float64(totals[alphabet])
		}
	}
	for channel := uint32(0); channel < 4; channel++ {
		if totals[channel] != 0 {
			feature[8+channel] = float64(vp8lSparseHistogramCount(histogram, channel<<16)) / float64(totals[channel])
		}
	}
	return feature
}

func vp8lSparseHistogramCount(histogram vp8lSparseHistogram, key uint32) uint32 {
	index, found := slices.BinarySearchFunc(histogram, key, func(entry vp8lHistogramEntry, target uint32) int {
		switch {
		case entry.key < target:
			return -1
		case entry.key > target:
			return 1
		default:
			return 0
		}
	})
	if !found {
		return 0
	}
	return histogram[index].count
}

func vp8lFeatureDistance(a vp8lHistogramFeature, b vp8lHistogramFeature) float64 {
	var distance float64
	for i := range a {
		delta := a[i] - b[i]
		distance += delta * delta
	}
	return distance
}

func vp8lRefineHistogramGroups(tiles []vp8lSparseHistogram, assignments []uint16, cacheBits uint8, iterations int, workspace *vp8lHuffmanWorkspace) ([]uint16, []vp8lCodeGroup, []uint8, int) {
	assignments = append([]uint16(nil), assignments...)
	var groups []vp8lCodeGroup
	var groupCosts []uint8
	greenSize := 0
	for iteration := 0; iteration <= iterations; iteration++ {
		assignments, groups = vp8lBuildHistogramGroups(tiles, assignments, cacheBits, workspace)
		groupCosts, greenSize = vp8lBuildCodeGroupCosts(groups, cacheBits, workspace)
		if len(groups) < 2 || iteration == iterations {
			break
		}
		changed := false
		for tileIndex, tile := range tiles {
			if len(tile) == 0 {
				continue
			}
			bestGroup := int(assignments[tileIndex])
			alphabetSize := vp8lHistogramAlphabetSize(greenSize)
			bestCost := vp8lSparseHistogramTableCost(tile, groupCosts[bestGroup*alphabetSize:(bestGroup+1)*alphabetSize], greenSize)
			for group := range groups {
				cost := vp8lSparseHistogramTableCost(tile, groupCosts[group*alphabetSize:(group+1)*alphabetSize], greenSize)
				if cost < bestCost {
					bestCost = cost
					bestGroup = group
				}
			}
			if uint16(bestGroup) != assignments[tileIndex] {
				assignments[tileIndex] = uint16(bestGroup)
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return assignments, groups, groupCosts, greenSize
}

func vp8lRefineFinalEntropyGroupsWorkspace(base vp8lImagePlan, budget vp8lBudget, workspace *vp8lSearchWorkspace) vp8lImagePlan {
	if base.meta == nil || len(base.meta.groups) < 2 || len(base.meta.groups) > vp8lMaxEntropyGroups {
		return base
	}
	tiles, tileWidth, tileHeight := vp8lTileHistogramsWorkspace(base, base.meta.prefixBits, workspace)
	var huffmanWorkspace *vp8lHuffmanWorkspace
	if workspace != nil {
		huffmanWorkspace = &workspace.huffman
	}
	groupCosts, greenSize := vp8lBuildCodeGroupCosts(base.meta.groups, base.cacheBits, huffmanWorkspace)
	alphabetSize := vp8lHistogramAlphabetSize(greenSize)
	assignments := append([]uint16(nil), base.meta.groupMap...)
	var changedGroups [vp8lMaxEntropyGroups]bool
	changed := false
	for tileIndex, tile := range tiles {
		if len(tile) == 0 {
			continue
		}
		bestGroup := int(assignments[tileIndex])
		bestCost := vp8lSparseHistogramTableCost(tile, groupCosts[bestGroup*alphabetSize:(bestGroup+1)*alphabetSize], greenSize)
		for group := range base.meta.groups {
			cost := vp8lSparseHistogramTableCost(tile, groupCosts[group*alphabetSize:(group+1)*alphabetSize], greenSize)
			if cost < bestCost {
				bestCost = cost
				bestGroup = group
			}
		}
		if uint16(bestGroup) != assignments[tileIndex] {
			changedGroups[assignments[tileIndex]] = true
			changedGroups[bestGroup] = true
			assignments[tileIndex] = uint16(bestGroup)
			changed = true
		}
	}
	if !changed {
		return base
	}
	groupMap, codeGroups, rebuiltGroups := vp8lBuildRefinedHistogramGroups(tiles, assignments, base.meta.groups, changedGroups, base.cacheBits, huffmanWorkspace)
	groupCosts, greenSize = vp8lBuildCodeGroupCosts(codeGroups, base.cacheBits, huffmanWorkspace)
	entropyPixels := make([]uint32, len(groupMap))
	for i, group := range groupMap {
		entropyPixels[i] = 0xff000000 | uint32(group>>8)<<16 | uint32(uint8(group))<<8
	}
	candidate := base
	candidate.meta = &vp8lEntropyPlan{
		prefixBits: base.meta.prefixBits,
		width:      tileWidth,
		height:     tileHeight,
		groupMap:   append([]uint16(nil), groupMap...),
		image:      buildVP8LLiteralImagePlan(entropyPixels, tileWidth, tileHeight),
		groups:     codeGroups,
	}
	budget.counters.recordEntropyTrial()
	budget.counters.recordEntropyTiles(len(tiles))
	budget.counters.recordHuffmanEmissionBuilds(rebuiltGroups*5 + 5)
	candidateBits := vp8lEntropyCandidateBitLen(base.cacheBits, candidate.meta, tiles, vp8lTokenExtraBits(base.tokens), groupCosts, greenSize)
	if candidateBits < base.bitLen(true) {
		return candidate
	}
	return base
}

func vp8lBuildRefinedHistogramGroups(tiles []vp8lSparseHistogram, assignments []uint16, incumbent []vp8lCodeGroup, changed [vp8lMaxEntropyGroups]bool, cacheBits uint8, workspace *vp8lHuffmanWorkspace) ([]uint16, []vp8lCodeGroup, int) {
	var used map[uint16]uint16
	var compacted []uint16
	if workspace == nil {
		used = make(map[uint16]uint16)
		compacted = make([]uint16, len(assignments))
	} else {
		if workspace.groupRemap == nil {
			workspace.groupRemap = make(map[uint16]uint16)
		} else {
			clear(workspace.groupRemap)
		}
		used = workspace.groupRemap
		if cap(workspace.compactedGroups) < len(assignments) {
			workspace.compactedGroups = make([]uint16, len(assignments))
		} else {
			workspace.compactedGroups = workspace.compactedGroups[:len(assignments)]
		}
		compacted = workspace.compactedGroups
	}
	var incumbentByGroup [vp8lMaxEntropyGroups]uint16
	for i, group := range assignments {
		mapped, exists := used[group]
		if !exists {
			mapped = uint16(len(used))
			used[group] = mapped
			incumbentByGroup[mapped] = group
		}
		compacted[i] = mapped
	}
	histograms := vp8lResetDenseHistograms(len(used), cacheBits, workspace)
	for tileIndex, tile := range tiles {
		incumbentGroup := assignments[tileIndex]
		if changed[incumbentGroup] {
			histograms[compacted[tileIndex]].addSparse(tile)
		}
	}
	groups := make([]vp8lCodeGroup, len(used))
	rebuilt := 0
	for group := range groups {
		incumbentGroup := incumbentByGroup[group]
		if changed[incumbentGroup] {
			groups[group] = histograms[group].codeGroup(workspace)
			rebuilt++
		} else {
			groups[group] = incumbent[incumbentGroup]
		}
	}
	return compacted, groups, rebuilt
}

func vp8lBuildCodeGroupCosts(groups []vp8lCodeGroup, cacheBits uint8, workspace *vp8lHuffmanWorkspace) ([]uint8, int) {
	greenSize := nLiteralCodes + nLengthCodes
	if cacheBits != 0 {
		greenSize += 1 << cacheBits
	}
	alphabetSize := vp8lHistogramAlphabetSize(greenSize)
	length := len(groups) * alphabetSize
	var costs []uint8
	if workspace == nil {
		costs = make([]uint8, length)
	} else if cap(workspace.groupCosts) < length {
		workspace.groupCosts = make([]uint8, length)
		costs = workspace.groupCosts
	} else {
		workspace.groupCosts = workspace.groupCosts[:length]
		costs = workspace.groupCosts
	}
	for groupIndex := range groups {
		group := &groups[groupIndex]
		table := costs[groupIndex*alphabetSize : (groupIndex+1)*alphabetSize]
		vp8lFillTreeCosts(table[:greenSize], &group.green)
		offset := greenSize
		vp8lFillTreeCosts(table[offset:offset+nLiteralCodes], &group.red)
		offset += nLiteralCodes
		vp8lFillTreeCosts(table[offset:offset+nLiteralCodes], &group.blue)
		offset += nLiteralCodes
		vp8lFillTreeCosts(table[offset:offset+nLiteralCodes], &group.alpha)
		offset += nLiteralCodes
		vp8lFillTreeCosts(table[offset:offset+nDistanceCodes], &group.distance)
	}
	return costs[:length], greenSize
}

func vp8lFillTreeCosts(costs []uint8, tree *vp8lHuffmanTree) {
	for symbol := range costs {
		costs[symbol] = uint8(tree.symbolCost(symbol, 16))
	}
}

func vp8lHistogramAlphabetSize(greenSize int) int {
	return greenSize + 3*nLiteralCodes + nDistanceCodes
}

func vp8lSparseHistogramTableCost(histogram vp8lSparseHistogram, costs []uint8, greenSize int) uint64 {
	var cost uint64
	for _, entry := range histogram {
		index := vp8lHistogramDenseIndex(entry.key, greenSize)
		cost += uint64(costs[index]) * uint64(entry.count)
	}
	return cost
}

func vp8lBuildHistogramGroups(tiles []vp8lSparseHistogram, assignments []uint16, cacheBits uint8, workspace *vp8lHuffmanWorkspace) ([]uint16, []vp8lCodeGroup) {
	var used map[uint16]uint16
	var compacted []uint16
	if workspace == nil {
		used = make(map[uint16]uint16)
		compacted = make([]uint16, len(assignments))
	} else {
		if workspace.groupRemap == nil {
			workspace.groupRemap = make(map[uint16]uint16)
		} else {
			clear(workspace.groupRemap)
		}
		used = workspace.groupRemap
		if cap(workspace.compactedGroups) < len(assignments) {
			workspace.compactedGroups = make([]uint16, len(assignments))
		} else {
			workspace.compactedGroups = workspace.compactedGroups[:len(assignments)]
		}
		compacted = workspace.compactedGroups
	}
	for i, group := range assignments {
		mapped, exists := used[group]
		if !exists {
			mapped = uint16(len(used))
			used[group] = mapped
		}
		compacted[i] = mapped
	}
	histograms := vp8lResetDenseHistograms(len(used), cacheBits, workspace)
	for tileIndex, tile := range tiles {
		histograms[compacted[tileIndex]].addSparse(tile)
	}
	groups := make([]vp8lCodeGroup, len(histograms))
	for i := range histograms {
		groups[i] = histograms[i].codeGroup(workspace)
	}
	return compacted, groups
}

func vp8lResetDenseHistograms(count int, cacheBits uint8, workspace *vp8lHuffmanWorkspace) []vp8lDenseHistogram {
	if workspace == nil {
		histograms := make([]vp8lDenseHistogram, count)
		for i := range histograms {
			histograms[i] = newVP8LDenseHistogram(cacheBits)
		}
		return histograms
	}
	if cap(workspace.denseHistograms) < count {
		workspace.denseHistograms = make([]vp8lDenseHistogram, count)
	} else {
		workspace.denseHistograms = workspace.denseHistograms[:count]
	}
	greenSize := nLiteralCodes + nLengthCodes
	if cacheBits != 0 {
		greenSize += 1 << cacheBits
	}
	for i := range workspace.denseHistograms {
		histogram := &workspace.denseHistograms[i]
		histogram.green = vp8lResizeUint32s(histogram.green, greenSize)
		clear(histogram.green)
		clear(histogram.red[:])
		clear(histogram.blue[:])
		clear(histogram.alpha[:])
		clear(histogram.distance[:])
	}
	return workspace.denseHistograms
}

func newVP8LDenseHistogram(cacheBits uint8) vp8lDenseHistogram {
	greenSize := nLiteralCodes + nLengthCodes
	if cacheBits != 0 {
		greenSize += 1 << cacheBits
	}
	return vp8lDenseHistogram{green: make([]uint32, greenSize)}
}

func (histogram *vp8lDenseHistogram) addSparse(sparse vp8lSparseHistogram) {
	for _, entry := range sparse {
		alphabet := entry.key >> 16
		symbol := int(entry.key & 0xffff)
		switch alphabet {
		case vp8lAlphabetGreen:
			histogram.green[symbol] += entry.count
		case vp8lAlphabetRed:
			histogram.red[symbol] += entry.count
		case vp8lAlphabetBlue:
			histogram.blue[symbol] += entry.count
		case vp8lAlphabetAlpha:
			histogram.alpha[symbol] += entry.count
		case vp8lAlphabetDistance:
			histogram.distance[symbol] += entry.count
		}
	}
}

func (histogram *vp8lDenseHistogram) codeGroup(workspace *vp8lHuffmanWorkspace) vp8lCodeGroup {
	return vp8lCodeGroup{
		green:    buildVP8LHuffmanTreeWorkspace(histogram.green, workspace),
		red:      buildVP8LHuffmanTreeWorkspace(histogram.red[:], workspace),
		blue:     buildVP8LHuffmanTreeWorkspace(histogram.blue[:], workspace),
		alpha:    buildVP8LHuffmanTreeWorkspace(histogram.alpha[:], workspace),
		distance: buildVP8LHuffmanTreeWorkspace(histogram.distance[:], workspace),
	}
}
