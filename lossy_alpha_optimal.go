package webp

import (
	"image"
	"math"
)

const alphaOptimalSpatialDistanceCount = 16

type alphaOptimalCostModel struct {
	code alphaCode
}

type alphaOptimalWorkspace struct {
	costs         []uint64
	previous      []int32
	lengths       []uint16
	distanceCodes []uint8
	spatial       []uint16
	runs          []uint16
}

type alphaSpatialCandidate struct {
	offset       int
	distanceCode uint8
}

func optimizeLossyAlphaPlans(readPixel pixelReader, bounds image.Rectangle, width int, height int, cfg lossyAlphaConfig, analysis *lossyAlphaAnalysis) {
	var attempted [4]bool
	for attempt := 0; attempt < cfg.optimalFilters; attempt++ {
		bestFilter := -1
		bestSize := uint64(math.MaxUint64)
		for filter, seed := range analysis.lz77Residuals {
			if !cfg.filters[filter] || attempted[filter] || !seed.hasRefs || !seed.encodable() {
				continue
			}
			code, ok := alphaCodeFor(seed)
			if !ok {
				continue
			}
			code.lz77 = true
			code.rowCopy = true
			if size := alphaVP8LStreamSize(seed, code); size < bestSize {
				bestFilter = filter
				bestSize = size
			}
		}
		if bestFilter < 0 {
			break
		}
		attempted[bestFilter] = true
		seed := analysis.lz77Residuals[bestFilter]
		plan, ok := optimizeAlphaPlan(readPixel, bounds, width, height, byte(bestFilter), seed, cfg.optimalPasses)
		if !ok {
			continue
		}
		analysis.optimalResiduals[bestFilter] = plan
	}
}

func optimizeAlphaPlan(readPixel pixelReader, bounds image.Rectangle, width int, height int, filter byte, seed alphaResidualPlan, passes int) (alphaResidualPlan, bool) {
	seedCode, ok := alphaCodeFor(seed)
	if !ok {
		return alphaResidualPlan{}, false
	}
	seedCode.lz77 = true
	seedCode.rowCopy = true
	bestSize := alphaVP8LStreamSize(seed, seedCode)
	best := alphaResidualPlan{}
	model := alphaOptimalCostModel{code: seedCode}
	var previousTokens []alphaToken
	for pass := 0; pass < passes; pass++ {
		tokens, ok := buildAlphaOptimalTokens(readPixel, bounds, width, height, filter, model)
		if !ok || alphaTokensEqual(tokens, previousTokens) {
			break
		}
		plan := alphaPlanForTokens(tokens)
		if !plan.hasRefs || !plan.encodable() {
			break
		}
		code, ok := alphaCodeFor(plan)
		if !ok {
			break
		}
		code.lz77 = true
		if size := alphaVP8LStreamSize(plan, code); size < bestSize {
			bestSize = size
			best = plan
		}
		model.code = code
		previousTokens = tokens
	}
	return best, len(best.tokens) != 0
}

func buildAlphaOptimalTokens(readPixel pixelReader, bounds image.Rectangle, width int, height int, filter byte, model alphaOptimalCostModel) ([]alphaToken, bool) {
	if width == 0 || height == 0 {
		return nil, false
	}
	previousAlpha, currentAlpha := makeAlphaRowPair(width)
	previousResidual, currentResidual := makeAlphaRowPair(width)
	workspace := newAlphaOptimalWorkspace(width)
	tokens := make([]alphaToken, 0, width*height/4)
	for y := 0; y < height; y++ {
		left := uint8(0)
		for x := 0; x < width; x++ {
			alpha := readPixel(bounds.Min.X+x, bounds.Min.Y+y).A
			above := uint8(0)
			if y > 0 {
				above = previousAlpha[x]
			}
			upperLeft := uint8(0)
			if x > 0 && y > 0 {
				upperLeft = previousAlpha[x-1]
			}
			currentResidual[x] = alpha - alphaPredictor(filter, x, y, left, above, upperLeft)
			currentAlpha[x] = alpha
			left = alpha
		}
		rowTokens, ok := alphaOptimalParseRow(currentResidual, previousResidual, y > 0, model, &workspace)
		if !ok {
			return nil, false
		}
		tokens = append(tokens, rowTokens...)
		previousAlpha, currentAlpha = currentAlpha, previousAlpha
		previousResidual, currentResidual = currentResidual, previousResidual
	}
	return tokens, true
}

func newAlphaOptimalWorkspace(width int) alphaOptimalWorkspace {
	return alphaOptimalWorkspace{
		costs:         make([]uint64, width+1),
		previous:      make([]int32, width+1),
		lengths:       make([]uint16, width+1),
		distanceCodes: make([]uint8, width+1),
		spatial:       make([]uint16, alphaOptimalSpatialDistanceCount*width),
		runs:          make([]uint16, width),
	}
}

func alphaOptimalParseRow(current []uint8, previous []uint8, hasPrevious bool, model alphaOptimalCostModel, workspace *alphaOptimalWorkspace) ([]alphaToken, bool) {
	width := len(current)
	for i := 0; i <= width; i++ {
		workspace.costs[i] = math.MaxUint64
		workspace.previous[i] = -1
		workspace.lengths[i] = 0
		workspace.distanceCodes[i] = 0
	}
	workspace.costs[0] = 0
	spatialCandidates, spatialCount := alphaSpatialCandidates()
	if hasPrevious {
		alphaFillSpatialMatches(current, previous, spatialCandidates[:spatialCount], workspace.spatial)
	}
	alphaFillRunMatches(current, previous, hasPrevious, workspace.runs)

	relax := func(start int, length int, distanceCode int, edgeCost uint64) {
		end := start + length
		if end > width || workspace.costs[start] == math.MaxUint64 {
			return
		}
		cost := workspace.costs[start] + edgeCost
		if cost < workspace.costs[end] || cost == workspace.costs[end] && uint16(length) > workspace.lengths[end] {
			workspace.costs[end] = cost
			workspace.previous[end] = int32(start)
			workspace.lengths[end] = uint16(length)
			workspace.distanceCodes[end] = uint8(distanceCode)
		}
	}

	for x := 0; x < width; x++ {
		relax(x, 1, 0, model.literalCost(current[x]))
		var matchLengths [alphaOptimalSpatialDistanceCount + 1]uint16
		var matchDistances [alphaOptimalSpatialDistanceCount + 1]uint8
		matchCount := 0
		if runLength := workspace.runs[x]; runLength >= alphaMinBackwardRefLength {
			matchLengths[matchCount] = runLength
			matchDistances[matchCount] = alphaDistancePrevious
			matchCount++
		}
		if hasPrevious {
			for candidate := 0; candidate < spatialCount; candidate++ {
				matchLength := workspace.spatial[candidate*width+x]
				if matchLength >= alphaMinBackwardRefLength {
					matchLengths[matchCount] = matchLength
					matchDistances[matchCount] = spatialCandidates[candidate].distanceCode
					matchCount++
				}
			}
		}
		for match := 0; match < matchCount; match++ {
			if alphaMatchIsDominated(matchLengths[:matchCount], matchDistances[:matchCount], match, model) {
				continue
			}
			alphaRelaxMatchLengths(x, int(matchLengths[match]), int(matchDistances[match]), model, relax)
		}
	}
	if workspace.previous[width] < 0 {
		return nil, false
	}

	tokens := make([]alphaToken, 0, width/4)
	for end := width; end > 0; {
		start := int(workspace.previous[end])
		length := int(workspace.lengths[end])
		if start < 0 || length <= 0 || start+length != end {
			return nil, false
		}
		if length == 1 {
			tokens = append(tokens, alphaToken{literal: current[start]})
		} else {
			tokens = append(tokens, alphaToken{length: uint16(length), distanceCode: workspace.distanceCodes[end]})
		}
		end = start
	}
	for left, right := 0, len(tokens)-1; left < right; left, right = left+1, right-1 {
		tokens[left], tokens[right] = tokens[right], tokens[left]
	}
	return tokens, true
}

func alphaRelaxMatchLengths(start int, maxLength int, distanceCode int, model alphaOptimalCostModel, relax func(int, int, int, uint64)) {
	if maxLength > alphaMaxBackwardRefLength {
		maxLength = alphaMaxBackwardRefLength
	}
	var lengths [64]uint16
	count := alphaOptimalMatchLengths(maxLength, &lengths)
	for _, matchLength := range lengths[:count] {
		length := int(matchLength)
		relax(start, length, distanceCode, model.copyCost(length, distanceCode))
	}
}

func alphaOptimalMatchLengths(maxLength int, lengths *[64]uint16) int {
	count := 0
	last := 0
	emit := func(length int) {
		if length < alphaMinBackwardRefLength || length > maxLength || length == last {
			return
		}
		last = length
		lengths[count] = uint16(length)
		count++
	}
	for length := alphaMinBackwardRefLength; length <= minInt(maxLength, 32); length++ {
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

func alphaSpatialCandidates() ([alphaOptimalSpatialDistanceCount]alphaSpatialCandidate, int) {
	var candidates [alphaOptimalSpatialDistanceCount]alphaSpatialCandidate
	count := 0
	for i, offset := range vp8lDistanceMap {
		if offset.y != 1 {
			continue
		}
		if count == len(candidates) {
			break
		}
		candidates[count] = alphaSpatialCandidate{offset: offset.x, distanceCode: uint8(i + 1)}
		count++
	}
	return candidates, count
}

func alphaFillRunMatches(current []uint8, previous []uint8, hasPrevious bool, matches []uint16) {
	for x := len(current) - 1; x >= 0; x-- {
		if x == 0 {
			if !hasPrevious || current[x] != previous[len(previous)-1] {
				matches[x] = 0
				continue
			}
		} else if current[x] != current[x-1] {
			matches[x] = 0
			continue
		}
		length := 1
		if x+1 < len(current) && current[x+1] == current[x] {
			length += int(matches[x+1])
		}
		if length > alphaMaxBackwardRefLength {
			length = alphaMaxBackwardRefLength
		}
		matches[x] = uint16(length)
	}
}

func alphaMatchIsDominated(lengths []uint16, distances []uint8, candidate int, model alphaOptimalCostModel) bool {
	cost := model.distanceCost(int(distances[candidate]))
	for other := range lengths {
		if other == candidate || lengths[other] < lengths[candidate] {
			continue
		}
		otherCost := model.distanceCost(int(distances[other]))
		if otherCost < cost || otherCost == cost && lengths[other] > lengths[candidate] {
			return true
		}
	}
	return false
}

func alphaFillSpatialMatches(current []uint8, previous []uint8, candidates []alphaSpatialCandidate, matches []uint16) {
	width := len(current)
	for candidateIndex, candidate := range candidates {
		row := matches[candidateIndex*width : (candidateIndex+1)*width]
		for x := width - 1; x >= 0; x-- {
			previousX := x - candidate.offset
			if previousX < 0 || previousX >= width || current[x] != previous[previousX] {
				row[x] = 0
				continue
			}
			length := 1
			if x+1 < width && previousX+1 < width {
				length += int(row[x+1])
			}
			if length > alphaMaxBackwardRefLength {
				length = alphaMaxBackwardRefLength
			}
			row[x] = uint16(length)
		}
	}
}

func (m alphaOptimalCostModel) literalCost(symbol uint8) uint64 {
	return alphaSymbolCost(m.code, int(symbol))
}

func (m alphaOptimalCostModel) copyCost(length int, distanceCode int) uint64 {
	lengthPrefix := vp8lPrefixCode(length)
	return alphaSymbolCost(m.code, nLiteralCodes+lengthPrefix.code) +
		uint64(lengthPrefix.extraBits) +
		m.distanceCost(distanceCode)
}

func (m alphaOptimalCostModel) distanceCost(distanceCode int) uint64 {
	distancePrefix := vp8lDistancePrefixCode(distanceCode)
	return alphaDistanceSymbolCost(m.code, distancePrefix.code) + uint64(distancePrefix.extraBits)
}

func alphaSymbolCost(code alphaCode, symbol int) uint64 {
	if code.normal {
		if length := code.lengths[symbol]; length != 0 {
			return uint64(length)
		}
		return 8
	}
	switch code.n {
	case 1:
		if symbol == int(code.symbols[0]) {
			return 0
		}
	case 2:
		if symbol == int(code.symbols[0]) || symbol == int(code.symbols[1]) {
			return 1
		}
	}
	return 8
}

func alphaDistanceSymbolCost(code alphaCode, symbol int) uint64 {
	if code.distanceNormal {
		if length := code.distanceLengths[symbol]; length != 0 {
			return uint64(length)
		}
		return 6
	}
	switch code.distanceN {
	case 1:
		if symbol == int(code.distanceSymbols[0]) {
			return 0
		}
	case 2:
		if symbol == int(code.distanceSymbols[0]) || symbol == int(code.distanceSymbols[1]) {
			return 1
		}
	}
	return 6
}

func alphaPlanForTokens(tokens []alphaToken) alphaResidualPlan {
	plan := alphaResidualPlan{tokens: tokens}
	for _, token := range tokens {
		if token.length == 0 {
			plan.observe(int(token.literal))
			continue
		}
		plan.observeCopy(int(token.length), int(token.distanceCode))
	}
	return plan
}

func writeAlphaTokens(bits *bitWriter, code alphaCode, tokens []alphaToken) {
	for _, token := range tokens {
		if token.length == 0 {
			writeAlphaSymbol(bits, code, int(token.literal))
			continue
		}
		writeAlphaLZ77Copy(bits, code, int(token.length), int(token.distanceCode))
	}
}

func alphaTokensEqual(a []alphaToken, b []alphaToken) bool {
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
