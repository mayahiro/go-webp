package webp

const vp8lInfiniteCost = ^uint64(0) >> 2

type vp8lPrefixCost struct {
	symbol    uint8
	extraBits uint8
}

var vp8lLengthPrefixCosts = func() [vp8lMaxBackwardRefLength + 1]vp8lPrefixCost {
	var costs [vp8lMaxBackwardRefLength + 1]vp8lPrefixCost
	for length := 1; length < len(costs); length++ {
		prefix := vp8lPrefixCode(length)
		costs[length] = vp8lPrefixCost{symbol: uint8(prefix.code), extraBits: prefix.extraBits}
	}
	return costs
}()

func buildVP8LImagePlan(pixels []uint32, width int, height int, budget vp8lBudget) vp8lImagePlan {
	return buildVP8LImagePlanWorkspace(pixels, width, height, budget, nil)
}

func buildVP8LImagePlanWorkspace(pixels []uint32, width int, height int, budget vp8lBudget, workspace *vp8lSearchWorkspace) vp8lImagePlan {
	literal := buildVP8LLiteralImagePlan(pixels, width, height)
	graph := buildVP8LMatchGraphWorkspace(pixels, width, budget, workspace)
	if len(graph.edges) == 0 {
		return literal
	}

	best := literal
	greedyTokens := vp8lGreedyTokens(pixels, graph)
	greedy := vp8lImagePlan{
		width:  width,
		height: height,
		tokens: greedyTokens,
		group:  buildVP8LCodeGroup(greedyTokens, 0),
	}
	if greedy.bitLen(true) < best.bitLen(true) {
		best = greedy
	}

	seed := greedy
	for range budget.optimalPasses {
		tokens := vp8lOptimalTokensWorkspace(pixels, graph, seed.group, nil, workspace)
		candidate := vp8lImagePlan{
			width:  width,
			height: height,
			tokens: tokens,
			group:  buildVP8LCodeGroup(tokens, 0),
		}
		if candidate.bitLen(true) < best.bitLen(true) {
			best = candidate
		}
		seed = candidate
	}
	if budget.tryColorCache {
		best = vp8lChooseColorCachePlan(pixels, graph, best, budget, workspace)
	}
	best = vp8lChooseEntropyPlanWorkspace(best, budget, workspace)
	for range budget.entropyIterations {
		if best.meta == nil {
			break
		}
		var cacheHits []int32
		if best.cacheBits != 0 {
			cacheHits = vp8lColorCacheHitsWorkspace(pixels, best.cacheBits, workspace)
		}
		tokens := vp8lOptimalTokensForImageWorkspace(pixels, graph, best, cacheHits, workspace)
		candidate := vp8lImagePlan{
			width:     width,
			height:    height,
			cacheBits: best.cacheBits,
			tokens:    tokens,
			group:     buildVP8LCodeGroup(tokens, best.cacheBits),
		}
		candidate = vp8lChooseEntropyPlanWorkspace(candidate, budget, workspace)
		if candidate.bitLen(true) < best.bitLen(true) {
			best = candidate
		} else {
			break
		}
	}
	return best
}

func vp8lChooseColorCachePlan(pixels []uint32, graph vp8lMatchGraph, best vp8lImagePlan, budget vp8lBudget, workspace *vp8lSearchWorkspace) vp8lImagePlan {
	seedTokens := best.tokens
	type cacheCandidate struct {
		plan        vp8lImagePlan
		bits        uint64
		literalSeed bool
	}
	shortlist := make([]cacheCandidate, 0, 2)
	bestBits := best.bitLen(true)
	addShortlist := func(candidate cacheCandidate) {
		if candidate.bits > bestBits+bestBits/8+256 {
			return
		}
		shortlist = append(shortlist, candidate)
		for i := len(shortlist) - 1; i > 0 && shortlist[i].bits < shortlist[i-1].bits; i-- {
			shortlist[i], shortlist[i-1] = shortlist[i-1], shortlist[i]
		}
		if len(shortlist) > 2 {
			shortlist = shortlist[:2]
		}
	}
	for cacheBits := uint8(1); cacheBits <= vp8lMaxColorCacheBits; cacheBits++ {
		hits := vp8lColorCacheHitsWorkspace(pixels, cacheBits, workspace)
		hitCount := 0
		for _, hit := range hits {
			if hit >= 0 {
				hitCount++
			}
		}
		if hitCount < 8 {
			continue
		}
		cachedGroup, cachedDataBits := vp8lCacheAppliedGroupAndDataBits(seedTokens, hits, cacheBits)
		candidate := vp8lImagePlan{
			width:     best.width,
			height:    best.height,
			cacheBits: cacheBits,
			group:     cachedGroup,
		}
		counter := vp8lBitCounter()
		counter.writeBits(1, 1)
		counter.writeBits(uint32(cacheBits), 4)
		counter.writeBits(0, 1)
		cachedGroup.writeHeaders(counter)
		addShortlist(cacheCandidate{plan: candidate, bits: counter.bitLen + cachedDataBits})

		literalGroup, literalDataBits := vp8lLiteralCacheGroupAndDataBits(pixels, hits, cacheBits)
		literalPlan := vp8lImagePlan{
			width:     best.width,
			height:    best.height,
			cacheBits: cacheBits,
			group:     literalGroup,
		}
		counter = vp8lBitCounter()
		counter.writeBits(1, 1)
		counter.writeBits(uint32(cacheBits), 4)
		counter.writeBits(0, 1)
		literalGroup.writeHeaders(counter)
		addShortlist(cacheCandidate{plan: literalPlan, bits: counter.bitLen + literalDataBits, literalSeed: true})
	}
	for _, shortlisted := range shortlist {
		candidate := shortlisted.plan
		hits := vp8lColorCacheHitsWorkspace(pixels, candidate.cacheBits, workspace)
		if shortlisted.literalSeed {
			candidate.tokens = vp8lLiteralCacheTokens(pixels, hits)
		} else {
			candidate.tokens = vp8lApplyCacheToTokens(seedTokens, hits)
		}
		incumbent := candidate
		for range maxInt(1, budget.optimalPasses) {
			optimized := vp8lOptimalTokensWorkspace(pixels, graph, candidate.group, hits, workspace)
			candidate = vp8lImagePlan{
				width:     best.width,
				height:    best.height,
				cacheBits: candidate.cacheBits,
				tokens:    optimized,
				group:     buildVP8LCodeGroup(optimized, candidate.cacheBits),
			}
			if candidate.bitLen(true) < incumbent.bitLen(true) {
				incumbent = candidate
			}
		}
		if incumbent.bitLen(true) < best.bitLen(true) {
			best = incumbent
		}
	}
	return best
}

func vp8lCacheAppliedGroupAndDataBits(tokens []vp8lToken, hits []int32, cacheBits uint8) (vp8lCodeGroup, uint64) {
	greenCounts := make([]uint32, nLiteralCodes+nLengthCodes+1<<cacheBits)
	var redCounts [nLiteralCodes]uint32
	var blueCounts [nLiteralCodes]uint32
	var alphaCounts [nLiteralCodes]uint32
	var distanceCounts [nDistanceCodes]uint32
	var extraBits uint64
	position := 0
	for _, token := range tokens {
		switch token.kind() {
		case vp8lTokenLiteral:
			if hits[position] >= 0 {
				greenCounts[nLiteralCodes+nLengthCodes+int(hits[position])]++
			} else {
				pixel := token.literal()
				greenCounts[uint8(pixel>>8)]++
				redCounts[uint8(pixel>>16)]++
				blueCounts[uint8(pixel)]++
				alphaCounts[uint8(pixel>>24)]++
			}
			position++
		case vp8lTokenCopy:
			lengthPrefix := vp8lPrefixCode(token.copyLength())
			distancePrefix := vp8lDistancePrefixCode(token.distanceCode())
			greenCounts[nLiteralCodes+lengthPrefix.code]++
			distanceCounts[distancePrefix.code]++
			extraBits += uint64(lengthPrefix.extraBits + distancePrefix.extraBits)
			position += token.copyLength()
		case vp8lTokenCache:
			greenCounts[nLiteralCodes+nLengthCodes+token.cacheIndex()]++
			position++
		}
	}
	group := vp8lCodeGroup{
		green:    buildVP8LHuffmanTree(greenCounts),
		red:      buildVP8LHuffmanTree(redCounts[:]),
		blue:     buildVP8LHuffmanTree(blueCounts[:]),
		alpha:    buildVP8LHuffmanTree(alphaCounts[:]),
		distance: buildVP8LHuffmanTree(distanceCounts[:]),
	}
	dataBits := vp8lTreeDataBits(greenCounts, &group.green) +
		vp8lTreeDataBits(redCounts[:], &group.red) +
		vp8lTreeDataBits(blueCounts[:], &group.blue) +
		vp8lTreeDataBits(alphaCounts[:], &group.alpha) +
		vp8lTreeDataBits(distanceCounts[:], &group.distance) + extraBits
	return group, dataBits
}

func vp8lLiteralCacheGroupAndDataBits(pixels []uint32, hits []int32, cacheBits uint8) (vp8lCodeGroup, uint64) {
	greenCounts := make([]uint32, nLiteralCodes+nLengthCodes+1<<cacheBits)
	var redCounts [nLiteralCodes]uint32
	var blueCounts [nLiteralCodes]uint32
	var alphaCounts [nLiteralCodes]uint32
	var distanceCounts [nDistanceCodes]uint32
	for position, pixel := range pixels {
		if hits[position] >= 0 {
			greenCounts[nLiteralCodes+nLengthCodes+int(hits[position])]++
			continue
		}
		greenCounts[uint8(pixel>>8)]++
		redCounts[uint8(pixel>>16)]++
		blueCounts[uint8(pixel)]++
		alphaCounts[uint8(pixel>>24)]++
	}
	group := vp8lCodeGroup{
		green:    buildVP8LHuffmanTree(greenCounts),
		red:      buildVP8LHuffmanTree(redCounts[:]),
		blue:     buildVP8LHuffmanTree(blueCounts[:]),
		alpha:    buildVP8LHuffmanTree(alphaCounts[:]),
		distance: buildVP8LHuffmanTree(distanceCounts[:]),
	}
	dataBits := vp8lTreeDataBits(greenCounts, &group.green) +
		vp8lTreeDataBits(redCounts[:], &group.red) +
		vp8lTreeDataBits(blueCounts[:], &group.blue) +
		vp8lTreeDataBits(alphaCounts[:], &group.alpha)
	return group, dataBits
}

func vp8lLiteralCacheTokens(pixels []uint32, hits []int32) []vp8lToken {
	tokens := make([]vp8lToken, len(pixels))
	for position, pixel := range pixels {
		if hits[position] >= 0 {
			tokens[position] = vp8lCacheToken(int(hits[position]))
		} else {
			tokens[position] = vp8lLiteralToken(pixel)
		}
	}
	return tokens
}

func vp8lColorCacheHits(pixels []uint32, bits uint8) []int32 {
	return vp8lColorCacheHitsWorkspace(pixels, bits, nil)
}

func vp8lColorCacheHitsWorkspace(pixels []uint32, bits uint8, workspace *vp8lSearchWorkspace) []int32 {
	size := 1 << bits
	values, valid, hits := workspace.resetColorCache(len(pixels), size)
	clear(valid)
	for i := range hits {
		hits[i] = -1
	}
	for position, pixel := range pixels {
		index := int((uint32(0x1e35a7bd) * pixel) >> (32 - bits))
		if valid[index] && values[index] == pixel {
			hits[position] = int32(index)
		}
		values[index] = pixel
		valid[index] = true
	}
	return hits
}

func vp8lApplyCacheToTokens(tokens []vp8lToken, hits []int32) []vp8lToken {
	result := make([]vp8lToken, 0, len(tokens))
	position := 0
	for _, token := range tokens {
		switch token.kind() {
		case vp8lTokenLiteral:
			if hits[position] >= 0 {
				result = append(result, vp8lCacheToken(int(hits[position])))
			} else {
				result = append(result, token)
			}
			position++
		case vp8lTokenCopy:
			result = append(result, token)
			position += token.copyLength()
		default:
			result = append(result, token)
			position++
		}
	}
	return result
}

func (image *vp8lImagePlan) bitLen(allowMetaPrefix bool) uint64 {
	counter := vp8lBitCounter()
	image.writeTo(counter, allowMetaPrefix)
	return counter.bitLen
}

func vp8lGreedyTokens(pixels []uint32, graph vp8lMatchGraph) []vp8lToken {
	tokens := make([]vp8lToken, 0, len(pixels))
	for position := 0; position < len(pixels); {
		matches := graph.at(position)
		if len(matches) == 0 {
			tokens = append(tokens, vp8lLiteralToken(pixels[position]))
			position++
			continue
		}
		best := matches[0]
		for _, match := range matches[1:] {
			if match.length > best.length || match.length == best.length && vp8lMatchScore(match) > vp8lMatchScore(best) {
				best = match
			}
		}
		tokens = append(tokens, vp8lCopyToken(int(best.length), int(best.distanceCode)))
		position += int(best.length)
	}
	return tokens
}

func vp8lOptimalTokens(pixels []uint32, graph vp8lMatchGraph, group vp8lCodeGroup, cacheHits []int32) []vp8lToken {
	return vp8lOptimalTokensWorkspace(pixels, graph, group, cacheHits, nil)
}

func vp8lOptimalTokensForImage(pixels []uint32, graph vp8lMatchGraph, image vp8lImagePlan, cacheHits []int32) []vp8lToken {
	return vp8lOptimalTokensForImageWorkspace(pixels, graph, image, cacheHits, nil)
}

func vp8lOptimalTokensWorkspace(pixels []uint32, graph vp8lMatchGraph, group vp8lCodeGroup, cacheHits []int32, workspace *vp8lSearchWorkspace) []vp8lToken {
	return vp8lOptimalTokensWithGroups(pixels, graph, cacheHits, func(int) *vp8lCodeGroup { return &group }, workspace)
}

func vp8lOptimalTokensForImageWorkspace(pixels []uint32, graph vp8lMatchGraph, image vp8lImagePlan, cacheHits []int32, workspace *vp8lSearchWorkspace) []vp8lToken {
	return vp8lOptimalTokensWithGroups(pixels, graph, cacheHits, image.codeGroupAt, workspace)
}

func vp8lOptimalTokensWithGroups(pixels []uint32, graph vp8lMatchGraph, cacheHits []int32, groupAt func(int) *vp8lCodeGroup, workspace *vp8lSearchWorkspace) []vp8lToken {
	costs, previous, selected := workspace.resetDP(len(pixels))
	costs[0] = 0
	previous[0] = 0
	for i := 1; i < len(costs); i++ {
		costs[i] = vp8lInfiniteCost
		previous[i] = -1
	}
	for position, pixel := range pixels {
		if costs[position] == vp8lInfiniteCost {
			continue
		}
		group := groupAt(position)
		literal := vp8lLiteralToken(pixel)
		vp8lRelaxToken(costs, previous, selected, position, position+1, costs[position]+group.literalCost(pixel), literal)
		if len(cacheHits) != 0 && cacheHits[position] >= 0 {
			cache := vp8lCacheToken(int(cacheHits[position]))
			cacheCost := group.green.symbolCost(nLiteralCodes+nLengthCodes+int(cacheHits[position]), 12)
			vp8lRelaxToken(costs, previous, selected, position, position+1, costs[position]+cacheCost, cache)
		}
		for _, match := range graph.at(position) {
			var lengths [32]uint16
			lengthCount := vp8lCandidateMatchLengths(int(match.length), &lengths)
			for _, rawLength := range lengths[:lengthCount] {
				length := int(rawLength)
				end := position + length
				copyCost := group.copyCost(length, match)
				vp8lRelaxToken(costs, previous, selected, position, end, costs[position]+copyCost, vp8lCopyToken(length, int(match.distanceCode)))
			}
		}
	}
	if previous[len(pixels)] < 0 {
		return vp8lGreedyTokens(pixels, graph)
	}
	tokens := make([]vp8lToken, 0, len(pixels))
	for position := len(pixels); position > 0; position = int(previous[position]) {
		tokens = append(tokens, selected[position])
	}
	for left, right := 0, len(tokens)-1; left < right; left, right = left+1, right-1 {
		tokens[left], tokens[right] = tokens[right], tokens[left]
	}
	return tokens
}

func vp8lRelaxToken(costs []uint64, previous []int32, selected []vp8lToken, start int, end int, cost uint64, token vp8lToken) {
	if end >= len(costs) || cost >= costs[end] {
		return
	}
	costs[end] = cost
	previous[end] = int32(start)
	selected[end] = token
}

func vp8lCandidateMatchLengths(maxLength int, result *[32]uint16) int {
	count := 0
	for length := vp8lMinBackwardRefLength; length <= minInt(maxLength, 16); length++ {
		result[count] = uint16(length)
		count++
	}
	for _, length := range [...]int{24, 32, 48, 64, 96, 128, 192, 256, 384, 512, 768, 1024, 1536, 2048, 3072, 4096} {
		if length > maxLength {
			break
		}
		result[count] = uint16(length)
		count++
	}
	if maxLength >= vp8lMinBackwardRefLength && (count == 0 || int(result[count-1]) != maxLength) {
		result[count] = uint16(maxLength)
		count++
	}
	return count
}

func (group *vp8lCodeGroup) literalCost(pixel uint32) uint64 {
	return group.green.symbolCost(int(uint8(pixel>>8)), 9) +
		group.red.symbolCost(int(uint8(pixel>>16)), 9) +
		group.blue.symbolCost(int(uint8(pixel)), 9) +
		group.alpha.symbolCost(int(uint8(pixel>>24)), 9)
}

func (group *vp8lCodeGroup) copyCost(length int, match vp8lMatch) uint64 {
	lengthPrefix := vp8lLengthPrefixCosts[length]
	return group.green.symbolCost(nLiteralCodes+int(lengthPrefix.symbol), 10) + uint64(lengthPrefix.extraBits) +
		group.distance.symbolCost(int(match.distanceSymbol), 7) + uint64(match.distanceExtraBits)
}

func (tree *vp8lHuffmanTree) symbolCost(symbol int, fallback uint64) uint64 {
	switch tree.kind {
	case vp8lTreeSimple:
		if tree.symbolCount < 2 {
			if symbol == int(tree.symbols[0]) {
				return 0
			}
			return fallback
		}
		if symbol == int(tree.symbols[0]) || symbol == int(tree.symbols[1]) {
			return 1
		}
		return fallback
	case vp8lTreeNormal:
		if symbol >= 0 && symbol < len(tree.lengths) && tree.lengths[symbol] != 0 {
			return uint64(tree.lengths[symbol])
		}
		return fallback
	case vp8lTreeFull8:
		if symbol < nLiteralCodes {
			return 8
		}
		return fallback
	default:
		return fallback
	}
}
