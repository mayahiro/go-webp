package webp

import (
	"image"
	"image/color"
	"math"
)

type vp8lLZ77Statistics struct {
	literalAnalysis imageAnalysis
	greenCounts     [nLiteralCodes + nLengthCodes]uint32
	distanceCounts  [nDistanceCodes]uint32
	copyCount       int
}

func vp8lPlanWithLZ77(base vp8lEncodingPlan, tokens []vp8lToken, stats vp8lLZ77Statistics) (vp8lEncodingPlan, bool) {
	if stats.copyCount == 0 {
		return vp8lEncodingPlan{}, false
	}
	greenLengths, ok := huffmanCodeLengths(stats.greenCounts)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	distanceN, distanceSymbols, distanceLengths, distanceCodes, distanceNormal, ok := vp8lDistanceCodeFor(stats.distanceCounts)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	base.lz77 = true
	base.lz77Tokens = tokens
	base.lz77LiteralAnalysis = stats.literalAnalysis
	base.lz77GreenCounts = stats.greenCounts
	base.lz77GreenLengths = greenLengths
	base.lz77GreenCodes = canonicalCodes(greenLengths)
	base.lz77DistanceCounts = stats.distanceCounts
	base.lz77DistanceN = distanceN
	base.lz77DistanceSymbols = distanceSymbols
	base.lz77DistanceLengths = distanceLengths
	base.lz77DistanceCodes = distanceCodes
	base.lz77DistanceNormal = distanceNormal
	return base, true
}

func vp8lAnalyzeLZ77Tokens(tokens []vp8lToken, baseAnalysis imageAnalysis) (vp8lLZ77Statistics, bool) {
	observer := newVP8LLiteralAnalysisObserver(baseAnalysis)
	stats := vp8lLZ77Statistics{}
	literalCount := 0
	for _, token := range tokens {
		if token.colorCache {
			return vp8lLZ77Statistics{}, false
		}
		if token.copyLength > 0 {
			if token.copyLength < vp8lMinBackwardRefLength {
				return vp8lLZ77Statistics{}, false
			}
			lengthPrefix := vp8lPrefixCode(token.copyLength)
			distancePrefix := vp8lDistancePrefixCode(token.distanceCode)
			stats.greenCounts[nLiteralCodes+lengthPrefix.code]++
			stats.distanceCounts[distancePrefix.code]++
			stats.copyCount++
			continue
		}
		observer.observePixel(token.pixel)
		stats.greenCounts[token.pixel.G]++
		literalCount++
	}
	if literalCount == 0 {
		stats.literalAnalysis = emptyVP8LLiteralAnalysis()
	} else {
		stats.literalAnalysis = observer.result()
	}
	return stats, true
}

func vp8lLZ77TokenCount(counts [nLiteralCodes + nLengthCodes]uint32) int {
	total := 0
	for _, count := range counts {
		total += int(count)
	}
	return total
}

func vp8lOptimizeLZ77Plan(readPixel pixelReader, bounds image.Rectangle, mainWidth int, width int, height int, literalBase vp8lEncodingPlan, seed vp8lEncodingPlan, candidateCount int, passes int) (vp8lEncodingPlan, bool) {
	return vp8lOptimizeLZ77PlanWorkspace(readPixel, bounds, mainWidth, width, height, literalBase, seed, candidateCount, passes, nil, nil, vp8lMatchGraph{})
}

func vp8lOptimizeLZ77PlanWorkspace(readPixel pixelReader, bounds image.Rectangle, mainWidth int, width int, height int, literalBase vp8lEncodingPlan, seed vp8lEncodingPlan, candidateCount int, passes int, workspace *vp8lLZ77Workspace, packed []uint32, matchGraph vp8lMatchGraph) (vp8lEncodingPlan, bool) {
	if workspace == nil {
		workspace = &vp8lLZ77Workspace{}
	}
	best := seed
	bestBits := vp8lPayloadBits(width, height, seed)
	model := seed
	improved := false
	maxMatchLength := vp8lOptimalMatchLengthLimit(literalBase)
	paretoMatches := literalBase.colorIndexing && len(literalBase.colorTable) <= 8
	for pass := 0; pass < passes; pass++ {
		tokens, ok := vp8lBuildOptimalLZ77Workspace(readPixel, bounds, mainWidth, candidateCount, maxMatchLength, paretoMatches, vp8lLZ77CostModelForPlan(model), workspace, pass, len(model.lz77Tokens), packed, matchGraph)
		if !ok || vp8lLZ77TokensEqual(tokens, model.lz77Tokens) {
			break
		}
		stats, ok := vp8lAnalyzeLZ77Tokens(tokens, literalBase.analysis)
		if !ok {
			break
		}
		candidate, ok := vp8lPlanWithLZ77(literalBase, tokens, stats)
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
	return best, improved
}

func vp8lOptimalMatchLengthLimit(plan vp8lEncodingPlan) int {
	if plan.colorIndexing && len(plan.colorTable) <= 8 {
		return 512
	}
	return vp8lMaxBackwardRefLength
}

type vp8lLZ77CostModel struct {
	literalAnalysis   imageAnalysis
	greenCounts       [nLiteralCodes + nLengthCodes]uint32
	greenLengths      [nLiteralCodes + nLengthCodes]uint8
	distanceCounts    [nDistanceCodes]uint32
	distanceN         int
	distanceLengths   [nDistanceCodes]uint8
	distanceNormal    bool
	colorCacheCounts  []uint32
	colorCacheLengths []uint8
	colorCacheIndices []int32
}

func vp8lLZ77CostModelForPlan(plan vp8lEncodingPlan) vp8lLZ77CostModel {
	return vp8lLZ77CostModel{
		literalAnalysis: plan.lz77LiteralAnalysis,
		greenCounts:     plan.lz77GreenCounts,
		greenLengths:    plan.lz77GreenLengths,
		distanceCounts:  plan.lz77DistanceCounts,
		distanceN:       plan.lz77DistanceN,
		distanceLengths: plan.lz77DistanceLengths,
		distanceNormal:  plan.lz77DistanceNormal,
	}
}

func (m vp8lLZ77CostModel) literalCost(pixel color.NRGBA) uint64 {
	cost, _ := m.literalCostAt(-1, pixel)
	return cost
}

func (m vp8lLZ77CostModel) literalCostAt(pos int, pixel color.NRGBA) (uint64, int) {
	if pos >= 0 && pos < len(m.colorCacheIndices) {
		if cacheIndex := int(m.colorCacheIndices[pos]); cacheIndex >= 0 {
			return m.greenSymbolCost(nLiteralCodes + nLengthCodes + cacheIndex), cacheIndex
		}
	}
	cost := m.greenSymbolCost(int(pixel.G))
	cost += vp8lChannelSymbolCost(m.literalAnalysis.channels[1], pixel.R)
	cost += vp8lChannelSymbolCost(m.literalAnalysis.channels[2], pixel.B)
	cost += vp8lChannelSymbolCost(m.literalAnalysis.channels[3], pixel.A)
	return cost, -1
}

func (m vp8lLZ77CostModel) copyCost(length int, distanceCode int) uint64 {
	return m.lengthCost(length) + m.distanceCost(distanceCode)
}

func (m vp8lLZ77CostModel) lengthCost(length int) uint64 {
	lengthPrefix := vp8lPrefixCode(length)
	lengthSymbol := nLiteralCodes + lengthPrefix.code
	cost := m.greenSymbolCost(lengthSymbol)
	cost += uint64(lengthPrefix.extraBits)
	return cost
}

func (m vp8lLZ77CostModel) greenSymbolCost(symbol int) uint64 {
	if symbol >= 0 && symbol < len(m.colorCacheCounts) && symbol < len(m.colorCacheLengths) {
		return vp8lLZ77SymbolCost(m.colorCacheCounts[symbol], m.colorCacheLengths[symbol], 8)
	}
	if symbol >= 0 && symbol < len(m.greenCounts) {
		return vp8lLZ77SymbolCost(m.greenCounts[symbol], m.greenLengths[symbol], 8)
	}
	return 8
}

func (m vp8lLZ77CostModel) distanceCost(distanceCode int) uint64 {
	distancePrefix := vp8lDistancePrefixCode(distanceCode)
	cost := uint64(0)
	switch {
	case m.distanceCounts[distancePrefix.code] == 0:
		cost += 6
	case m.distanceNormal:
		cost += uint64(m.distanceLengths[distancePrefix.code])
	case m.distanceN == 2:
		cost++
	}
	cost += uint64(distancePrefix.extraBits)
	return cost
}

func vp8lLZ77SymbolCost(count uint32, length uint8, fallback uint64) uint64 {
	if count == 0 {
		return fallback
	}
	return uint64(length)
}

func vp8lChannelSymbolCost(channel channelPlan, symbol uint8) uint64 {
	if channel.constant {
		if channel.value == symbol {
			return 0
		}
		return 8
	}
	if channel.n == 2 {
		if channel.symbols[0] == symbol || channel.symbols[1] == symbol {
			return 1
		}
		return 8
	}
	if !channelUseNormal(channel, nLiteralCodes) {
		return 8
	}
	if channel.histogram != nil {
		if length := channel.histogram.lengths[symbol]; length != 0 {
			return uint64(length)
		}
		return 8
	}
	for i := 0; i < channel.n; i++ {
		if channel.symbols[i] == symbol {
			return uint64(channel.lengths[i])
		}
	}
	return 8
}

func vp8lBuildOptimalLZ77(readPixel pixelReader, bounds image.Rectangle, width int, candidateCount int, maxMatchLength int, paretoMatches bool, model vp8lLZ77CostModel) ([]vp8lToken, bool) {
	return vp8lBuildOptimalLZ77Workspace(readPixel, bounds, width, candidateCount, maxMatchLength, paretoMatches, model, nil, 0, 0, nil, vp8lMatchGraph{})
}

func vp8lBuildOptimalLZ77Workspace(readPixel pixelReader, bounds image.Rectangle, width int, candidateCount int, maxMatchLength int, paretoMatches bool, model vp8lLZ77CostModel, workspace *vp8lLZ77Workspace, pass int, tokenCapacity int, packed []uint32, matchGraph vp8lMatchGraph) ([]vp8lToken, bool) {
	total := bounds.Dx() * bounds.Dy()
	if total < vp8lMinBackwardRefLength*2 {
		return nil, false
	}
	if workspace == nil {
		workspace = &vp8lLZ77Workspace{}
	}
	pixels := packed
	if len(pixels) != total {
		workspace.packedPixels = resizeVP8LUint32s(workspace.packedPixels, total)
		pixels = workspace.packedPixels
		for pos := range pixels {
			pixels[pos] = vp8lPackPixel(vp8lPixelAt(readPixel, bounds, width, pos))
		}
	}
	costs, previous, lengths, distanceCodes, selectedCache := workspace.resizeOptimal(total)
	candidateCount = clipInt(candidateCount, vp8lMinHashCandidates, vp8lMaxHashCandidates)
	useMatchGraph := !paretoMatches && matchGraph.available() && matchGraph.supports(candidateCount)
	costs[0] = 0
	previous[0] = 0
	lengths[0] = 0
	distanceCodes[0] = 0
	selectedCache[0] = -1
	for i := 1; i <= total; i++ {
		costs[i] = math.MaxUint64
		previous[i] = -1
		lengths[i] = 0
		distanceCodes[i] = 0
		selectedCache[i] = -1
	}
	if !useMatchGraph {
		workspace.resetHashTables(candidateCount)
	}
	primaryHashTable := &workspace.hashTables.primary
	extraHashTable := &workspace.hashTables.extra
	var lengthCosts [vp8lMaxBackwardRefLength + 1]uint16
	for length := vp8lMinBackwardRefLength; length <= vp8lMaxBackwardRefLength; length++ {
		lengthCosts[length] = uint16(model.lengthCost(length))
	}

	relax := func(pos int, length int, distanceCode int, cacheIndex int, edgeCost uint64) {
		end := pos + length
		if end > total || costs[pos] == math.MaxUint64 {
			return
		}
		cost := costs[pos] + edgeCost
		if cost < costs[end] || cost == costs[end] && uint16(length) > lengths[end] {
			costs[end] = cost
			previous[end] = int32(pos)
			lengths[end] = uint16(length)
			distanceCodes[end] = int32(distanceCode)
			selectedCache[end] = int32(cacheIndex)
		}
	}

	for pos := 0; pos < total; pos++ {
		pixel := vp8lUnpackPixel(pixels[pos])
		literalCost, cacheIndex := model.literalCostAt(pos, pixel)
		relax(pos, 1, 0, cacheIndex, literalCost)
		if pos+vp8lMinBackwardRefLength <= total {
			var matches [vp8lMaxHashCandidates]vp8lMatch
			matchCount := 0
			if useMatchGraph {
				matches, matchCount = matchGraph.optimalMatches(pos, candidateCount, maxMatchLength)
			} else {
				hash := vp8lOptimalHashAt(pixels, pos)
				candidates := vp8lHashCandidatesFor(primaryHashTable[hash], extraHashTable[hash], candidateCount)
				matches, matchCount = vp8lOptimalMatchCandidates(candidates, candidateCount, pixels, width, pos, maxMatchLength, paretoMatches, model)
			}
			for _, match := range matches[:matchCount] {
				var matchLengths [64]uint16
				lengthCount := vp8lOptimalMatchLengths(match.length, &matchLengths)
				distanceCost := model.distanceCost(match.distanceCode)
				for _, matchLength := range matchLengths[:lengthCount] {
					length := int(matchLength)
					relax(pos, length, match.distanceCode, -1, uint64(lengthCosts[length])+distanceCost)
				}
			}
			if !useMatchGraph {
				vp8lInsertOptimalHash(primaryHashTable, extraHashTable, candidateCount, pixels, pos)
			}
		}
	}
	if previous[total] < 0 {
		return nil, false
	}

	if tokenCapacity == 0 {
		tokenCapacity = total / 4
	}
	tokens := workspace.resetOptimalTokens(pass, tokenCapacity)
	for pos := total; pos > 0; {
		start := int(previous[pos])
		length := int(lengths[pos])
		if start < 0 || length <= 0 || start+length != pos {
			return nil, false
		}
		if length == 1 {
			if cacheIndex := int(selectedCache[pos]); cacheIndex >= 0 {
				tokens = append(tokens, vp8lToken{cacheIndex: cacheIndex, colorCache: true})
			} else {
				tokens = append(tokens, vp8lToken{pixel: vp8lUnpackPixel(pixels[start])})
			}
		} else {
			tokens = append(tokens, vp8lToken{copyLength: length, distanceCode: int(distanceCodes[pos])})
		}
		pos = start
	}
	for left, right := 0, len(tokens)-1; left < right; left, right = left+1, right-1 {
		tokens[left], tokens[right] = tokens[right], tokens[left]
	}
	workspace.keepOptimalTokens(pass, tokens)
	return tokens, true
}

func vp8lPackPixel(pixel color.NRGBA) uint32 {
	return uint32(pixel.R) | uint32(pixel.G)<<8 | uint32(pixel.B)<<16 | uint32(pixel.A)<<24
}

func vp8lUnpackPixel(pixel uint32) color.NRGBA {
	return color.NRGBA{
		R: uint8(pixel),
		G: uint8(pixel >> 8),
		B: uint8(pixel >> 16),
		A: uint8(pixel >> 24),
	}
}

func vp8lOptimalHashAt(pixels []uint32, pos int) int {
	a := pixels[pos]
	b := pixels[pos+1]
	c := pixels[pos+2]
	h := uint32(uint8(a))*0x1e35a7bd ^ uint32(uint8(a>>8))*0x85ebca6b ^ uint32(uint8(a>>16))*0xc2b2ae35 ^ uint32(uint8(a>>24))*0x27d4eb2d
	h ^= uint32(uint8(b))<<3 ^ uint32(uint8(b>>8))<<11 ^ uint32(uint8(b>>16))<<19 ^ uint32(uint8(b>>24))<<27
	h ^= uint32(uint8(c))*0x9e3779b1 ^ uint32(uint8(c>>8))*0x7f4a7c15 ^ uint32(uint8(c>>16))*0x94d049bb ^ uint32(uint8(c>>24))*0x2545f491
	return vp8lHashIndex(h)
}

func vp8lInsertOptimalHash(primary *[vp8lHashSize][vp8lMinHashCandidates]int32, extra *[vp8lHashSize][vp8lMinHashCandidates]int32, candidateCount int, pixels []uint32, pos int) {
	if pos+vp8lMinBackwardRefLength > len(pixels) {
		return
	}
	hash := vp8lOptimalHashAt(pixels, pos)
	primaryBucket := &primary[hash]
	if candidateCount > vp8lMinHashCandidates {
		extraBucket := &extra[hash]
		extraCount := candidateCount - vp8lMinHashCandidates
		copy(extraBucket[1:extraCount], extraBucket[:extraCount-1])
		extraBucket[0] = primaryBucket[len(primaryBucket)-1]
	}
	copy(primaryBucket[1:], primaryBucket[:len(primaryBucket)-1])
	primaryBucket[0] = int32(pos)
}

func vp8lOptimalMatchCandidates(candidates vp8lHashCandidateList, candidateCount int, pixels []uint32, width int, pos int, maxMatchLength int, paretoMatches bool, model vp8lLZ77CostModel) ([vp8lMaxHashCandidates]vp8lMatch, int) {
	var matches [vp8lMaxHashCandidates]vp8lMatch
	count := 0
	maxLength := minInt(len(pixels)-pos, minInt(maxMatchLength, vp8lMaxBackwardRefLength))
	for i := 0; i < candidateCount; i++ {
		matchPos := int(candidates[i])
		if matchPos < 0 || matchPos >= pos {
			continue
		}
		distance := pos - matchPos
		distanceCode, ok := vp8lDistanceCodeForPositionDistance(distance, width)
		if !ok {
			continue
		}
		length := 0
		for length < maxLength && pixels[matchPos+length] == pixels[pos+length] {
			length++
		}
		if length < vp8lMinBackwardRefLength {
			continue
		}
		duplicate := false
		for j := 0; j < count; j++ {
			if matches[j].distanceCode == distanceCode {
				duplicate = true
				break
			}
		}
		if !duplicate {
			matches[count] = vp8lMatch{length: length, distance: distance, distanceCode: distanceCode}
			count++
		}
	}
	return vp8lFilterOptimalMatches(matches, count, paretoMatches, model)
}

func vp8lFilterOptimalMatches(matches [vp8lMaxHashCandidates]vp8lMatch, count int, paretoMatches bool, model vp8lLZ77CostModel) ([vp8lMaxHashCandidates]vp8lMatch, int) {
	if !paretoMatches {
		best := vp8lMatch{}
		for i := 0; i < count; i++ {
			if matches[i].length > best.length || matches[i].length == best.length && matches[i].distance < best.distance {
				best = matches[i]
			}
		}
		if best.length == 0 {
			return matches, 0
		}
		matches[0] = best
		return matches, 1
	}
	for i := 0; i < count; i++ {
		if matches[i].length == 0 {
			continue
		}
		cost := model.distanceCost(matches[i].distanceCode)
		for j := 0; j < count; j++ {
			if i == j || matches[j].length == 0 {
				continue
			}
			otherCost := model.distanceCost(matches[j].distanceCode)
			if matches[j].length >= matches[i].length && otherCost <= cost && (matches[j].length > matches[i].length || otherCost < cost) {
				matches[i].length = 0
				break
			}
		}
	}
	write := 0
	for i := 0; i < count; i++ {
		if matches[i].length == 0 {
			continue
		}
		matches[write] = matches[i]
		write++
	}
	return matches, write
}

func vp8lOptimalMatchLengths(maxLength int, lengths *[64]uint16) int {
	count := 0
	last := 0
	emit := func(length int) {
		if length < vp8lMinBackwardRefLength || length > maxLength || length == last {
			return
		}
		last = length
		lengths[count] = uint16(length)
		count++
	}
	for length := vp8lMinBackwardRefLength; length <= minInt(maxLength, 32); length++ {
		emit(length)
	}
	for code := 4; code < nLengthCodes; code++ {
		extraBits := vp8lPrefixExtraBits(code)
		minLength := ((2 + code&1) << extraBits) + 1
		maxForCode := minLength + (1 << extraBits) - 1
		if maxForCode <= 32 {
			continue
		}
		emit(minLength)
		emit(minInt(maxForCode, maxLength))
		if maxForCode >= maxLength {
			break
		}
	}
	emit(maxLength)
	return count
}

func vp8lLZ77TokensEqual(a []vp8lToken, b []vp8lToken) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
