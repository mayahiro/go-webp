package webp

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"math"
)

type alphaPayload struct {
	size       uint64
	compressed bool
	filter     byte
	stream     []byte
}

type alphaPayloadCandidate struct {
	filter byte
	plan   alphaResidualPlan
	code   alphaCode
}

type lossyAlphaConfig struct {
	filters        [4]bool
	tryRLE         bool
	trySpatialRef  bool
	optimalPasses  int
	optimalPixels  int
	optimalFilters int
}

func lossyAlphaConfigForMode(mode Mode) lossyAlphaConfig {
	cfg := lossyAlphaConfig{
		filters:       [4]bool{true, true, true, true},
		tryRLE:        true,
		trySpatialRef: true,
		optimalPasses: 0,
		optimalPixels: 1 << 20,
	}
	switch mode {
	case ModeFast:
		cfg.filters = [4]bool{true, false, false, false}
		cfg.trySpatialRef = false
		cfg.optimalPasses = 0
	case ModeBestCompression:
		cfg.optimalPasses = 1
		cfg.optimalPixels = 4 << 20
		cfg.optimalFilters = 1
	case ModeLowMemory:
		cfg.trySpatialRef = false
		cfg.optimalPasses = 0
	}
	return cfg
}

func makeAlphaPayload(readPixel pixelReader, bounds image.Rectangle, width int, height int, analysis lossyAlphaAnalysis, cfg lossyAlphaConfig) (alphaPayload, error) {
	rawSize := uint64(1) + uint64(width)*uint64(height)
	if rawSize > math.MaxUint32 {
		return alphaPayload{}, fmt.Errorf("webp: encoded image is too large")
	}

	best := alphaPayload{size: rawSize}
	var candidateBuf [16]alphaPayloadCandidate
	candidates := appendAlphaPayloadCandidatesConfig(candidateBuf[:0], analysis, cfg)
	var bestCandidate alphaPayloadCandidate
	hasBestCandidate := false
	for _, candidate := range candidates {
		size := alphaPayloadCandidateSize(candidate)
		if size < best.size {
			best.size = size
			bestCandidate = candidate
			hasBestCandidate = true
		}
	}
	if !hasBestCandidate {
		return best, nil
	}
	stream, err := encodeAlphaVP8LStream(readPixel, bounds, width, height, bestCandidate.filter, bestCandidate.plan, bestCandidate.code)
	if err != nil {
		return alphaPayload{}, err
	}
	best.size = uint64(1 + len(stream))
	best.compressed = true
	best.filter = bestCandidate.filter
	best.stream = stream
	return best, nil
}

func appendAlphaPayloadCandidates(candidates []alphaPayloadCandidate, analysis lossyAlphaAnalysis) []alphaPayloadCandidate {
	return appendAlphaPayloadCandidatesConfig(candidates, analysis, lossyAlphaConfigForMode(ModeDefault))
}

func appendAlphaPayloadCandidatesConfig(candidates []alphaPayloadCandidate, analysis lossyAlphaAnalysis, cfg lossyAlphaConfig) []alphaPayloadCandidate {
	var scratch alphaHuffmanScratch
	for filter, plan := range analysis.residuals {
		if !cfg.filters[filter] {
			continue
		}
		if !plan.encodable() {
			continue
		}
		code, ok := alphaCodeForWithScratch(plan, &scratch)
		if !ok {
			continue
		}
		candidates = append(candidates, alphaPayloadCandidate{filter: byte(filter), plan: plan, code: code})
	}
	if !cfg.tryRLE {
		return candidates
	}
	for filter, plan := range analysis.rleResiduals {
		if !cfg.filters[filter] {
			continue
		}
		if !plan.hasRefs || !plan.encodable() {
			continue
		}
		code, ok := alphaCodeForWithScratch(plan, &scratch)
		if !ok {
			continue
		}
		code.lz77 = true
		candidates = append(candidates, alphaPayloadCandidate{filter: byte(filter), plan: plan, code: code})
	}
	if !cfg.trySpatialRef {
		return candidates
	}
	for filter, plan := range analysis.lz77Residuals {
		if !cfg.filters[filter] {
			continue
		}
		if !plan.hasRefs || !plan.encodable() {
			continue
		}
		code, ok := alphaCodeForWithScratch(plan, &scratch)
		if !ok {
			continue
		}
		code.lz77 = true
		code.rowCopy = true
		candidates = append(candidates, alphaPayloadCandidate{filter: byte(filter), plan: plan, code: code})
	}
	for filter, plan := range analysis.optimalResiduals {
		if !cfg.filters[filter] || len(plan.tokens) == 0 {
			continue
		}
		code, ok := alphaCodeForWithScratch(plan, &scratch)
		if !ok {
			continue
		}
		code.lz77 = true
		candidates = append(candidates, alphaPayloadCandidate{filter: byte(filter), plan: plan, code: code})
	}
	return candidates
}

func alphaPayloadCandidateSize(candidate alphaPayloadCandidate) uint64 {
	return 1 + alphaVP8LStreamSize(candidate.plan, candidate.code)
}

func encodeAlphaVP8LStream(readPixel pixelReader, bounds image.Rectangle, width int, height int, filter byte, plan alphaResidualPlan, code alphaCode) ([]byte, error) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	bits := newBitWriter(bw)
	writeAlphaVP8LImageStream(bits, readPixel, bounds, width, height, filter, plan, code)
	if err := bits.flush(); err != nil {
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeAlphaResidualBits(bits *bitWriter, readPixel pixelReader, bounds image.Rectangle, width int, height int, filter byte, code alphaCode) {
	previous, current := makeAlphaRowPair(width)
	for y := 0; y < height; y++ {
		left := uint8(0)
		for x := 0; x < width; x++ {
			alpha := readPixel(bounds.Min.X+x, bounds.Min.Y+y).A
			above := uint8(0)
			if y > 0 {
				above = previous[x]
			}
			upperLeft := uint8(0)
			if x > 0 && y > 0 {
				upperLeft = previous[x-1]
			}
			writeAlphaSymbol(bits, code, int(alpha-alphaPredictor(filter, x, y, left, above, upperLeft)))
			current[x] = alpha
			left = alpha
		}
		previous, current = current, previous
	}
}

func writeAlphaRLEBits(bits *bitWriter, readPixel pixelReader, bounds image.Rectangle, width int, height int, filter byte, code alphaCode) {
	var run alphaRun
	previous, current := makeAlphaRowPair(width)
	for y := 0; y < height; y++ {
		left := uint8(0)
		for x := 0; x < width; x++ {
			alpha := readPixel(bounds.Min.X+x, bounds.Min.Y+y).A
			above := uint8(0)
			if y > 0 {
				above = previous[x]
			}
			upperLeft := uint8(0)
			if x > 0 && y > 0 {
				upperLeft = previous[x-1]
			}
			run.write(bits, code, alpha-alphaPredictor(filter, x, y, left, above, upperLeft))
			current[x] = alpha
			left = alpha
		}
		previous, current = current, previous
	}
	run.flush(bits, code)
}

func writeAlphaLZ77Bits(bits *bitWriter, readPixel pixelReader, bounds image.Rectangle, width int, height int, filter byte, code alphaCode) {
	var run alphaRun
	previous, current := makeAlphaRowPair(width)
	previousResidual, currentResidual := makeAlphaRowPair(width)
	for y := 0; y < height; y++ {
		left := uint8(0)
		for x := 0; x < width; x++ {
			alpha := readPixel(bounds.Min.X+x, bounds.Min.Y+y).A
			above := uint8(0)
			if y > 0 {
				above = previous[x]
			}
			upperLeft := uint8(0)
			if x > 0 && y > 0 {
				upperLeft = previous[x-1]
			}
			currentResidual[x] = alpha - alphaPredictor(filter, x, y, left, above, upperLeft)
			current[x] = alpha
			left = alpha
		}
		run.writeRow(bits, code, currentResidual, previousResidual, y > 0)
		previous, current = current, previous
		previousResidual, currentResidual = currentResidual, previousResidual
	}
	run.flush(bits, code)
}

func writeAlphaSymbol(bits *bitWriter, code alphaCode, symbol int) {
	countLossyAlphaSymbol(symbol)
	if code.normal {
		if code.n == 1 {
			return
		}
		length := code.lengths[symbol]
		bits.writeBits(uint32(reverseBits(code.codes[symbol], length)), length)
		return
	}
	switch code.n {
	case 1:
		return
	case 2:
		if symbol == int(code.symbols[0]) {
			bits.writeBits(0, 1)
		} else {
			bits.writeBits(1, 1)
		}
	default:
		length := code.lengths[symbol]
		bits.writeBits(uint32(reverseBits(code.codes[symbol], length)), length)
	}
}

func writeAlphaDistanceSymbol(bits *bitWriter, code alphaCode, symbol uint8) {
	if code.distanceNormal {
		length := code.distanceLengths[symbol]
		bits.writeBits(uint32(reverseBits(code.distanceCodes[symbol], length)), length)
		return
	}
	switch code.distanceN {
	case 1:
		return
	case 2:
		if symbol == code.distanceSymbols[0] {
			bits.writeBits(0, 1)
		} else {
			bits.writeBits(1, 1)
		}
	}
}

func writeAlphaLZ77Copy(bits *bitWriter, code alphaCode, length int, distanceCode int) {
	for length > 0 {
		countLossyAlphaCopy()
		n := length
		if n > alphaMaxBackwardRefLength {
			n = alphaMaxBackwardRefLength
		}
		prefix := vp8lPrefixCode(n)
		writeAlphaSymbol(bits, code, nLiteralCodes+prefix.code)
		bits.writeBits(prefix.extra, prefix.extraBits)
		distancePrefix := vp8lDistancePrefixCode(distanceCode)
		writeAlphaDistanceSymbol(bits, code, uint8(distancePrefix.code))
		bits.writeBits(distancePrefix.extra, distancePrefix.extraBits)
		length -= n
	}
}

func writeAlphaChunk(w *bufio.Writer, readPixel pixelReader, bounds image.Rectangle, payload alphaPayload) error {
	if err := writeChunkHeader(w, "ALPH", uint32(payload.size)); err != nil {
		return err
	}
	if payload.compressed {
		header := alphCompressionVP8L | payload.filter<<2
		if err := w.WriteByte(header); err != nil {
			return err
		}
		if _, err := w.Write(payload.stream); err != nil {
			return err
		}
		return writeChunkPadding(w, payload.size)
	}

	if err := w.WriteByte(alphCompressionNone); err != nil {
		return err
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if err := w.WriteByte(readPixel(x, y).A); err != nil {
				return err
			}
		}
	}
	return writeChunkPadding(w, payload.size)
}

type lossyAlphaAnalysis struct {
	hasAlpha         bool
	residuals        [4]alphaResidualPlan
	rleResiduals     [4]alphaResidualPlan
	lz77Residuals    [4]alphaResidualPlan
	optimalResiduals [4]alphaResidualPlan
}

type alphaResidualPlan struct {
	n               int
	symbols         [2]uint16
	counts          [nLiteralCodes + nLengthCodes]uint32
	distanceN       int
	distanceSymbols [2]uint8
	distanceCounts  [nDistanceCodes]uint32
	hasRefs         bool
	run             alphaRun
	tokens          []alphaToken
}

type alphaToken struct {
	length       uint16
	distanceCode uint8
	literal      uint8
}

type alphaRun struct {
	active bool
	value  uint8
	length int
}

func makeAlphaRowPair(width int) ([]uint8, []uint8) {
	rows := make([]uint8, width*2)
	return rows[:width], rows[width:]
}

func makeAlphaFilterRows(previous [][]uint8, current [][]uint8, width int) {
	rows := make([]uint8, len(previous)*width*2)
	for i := range previous {
		start := i * width * 2
		previous[i] = rows[start : start+width]
		current[i] = rows[start+width : start+width*2]
	}
}

func analyzeLossyAlpha(readPixel pixelReader, bounds image.Rectangle, width int, height int) lossyAlphaAnalysis {
	return analyzeLossyAlphaConfig(readPixel, bounds, width, height, lossyAlphaConfigForMode(ModeDefault))
}

func analyzeLossyAlphaConfig(readPixel pixelReader, bounds image.Rectangle, width int, height int, cfg lossyAlphaConfig) lossyAlphaAnalysis {
	countLossyAlphaFilters(cfg.filters)
	var analysis lossyAlphaAnalysis
	previous, current := makeAlphaRowPair(width)
	var previousResiduals [4][]uint8
	var currentResiduals [4][]uint8
	if cfg.trySpatialRef {
		makeAlphaFilterRows(previousResiduals[:], currentResiduals[:], width)
	}
	for y := 0; y < height; y++ {
		left := uint8(0)
		for x := 0; x < width; x++ {
			alpha := readPixel(bounds.Min.X+x, bounds.Min.Y+y).A
			if alpha != 255 {
				analysis.hasAlpha = true
			}
			above := uint8(0)
			if y > 0 {
				above = previous[x]
			}
			upperLeft := uint8(0)
			if x > 0 && y > 0 {
				upperLeft = previous[x-1]
			}
			none := alpha
			horizontal := alpha - alphaPredictor(alphFilterHorizontal, x, y, left, above, upperLeft)
			vertical := alpha - alphaPredictor(alphFilterVertical, x, y, left, above, upperLeft)
			gradient := alpha - alphaPredictor(alphFilterGradient, x, y, left, above, upperLeft)
			residuals := [4]uint8{none, horizontal, vertical, gradient}
			for filter, residual := range residuals {
				if !cfg.filters[filter] {
					continue
				}
				analysis.residuals[filter].observe(int(residual))
				if cfg.tryRLE {
					analysis.rleResiduals[filter].observeRLE(residual)
				}
				if cfg.trySpatialRef {
					currentResiduals[filter][x] = residual
				}
			}
			current[x] = alpha
			left = alpha
		}
		if cfg.trySpatialRef {
			for filter := range currentResiduals {
				if !cfg.filters[filter] {
					continue
				}
				analysis.lz77Residuals[filter].observeLZ77Row(currentResiduals[filter], previousResiduals[filter], y > 0)
				previousResiduals[filter], currentResiduals[filter] = currentResiduals[filter], previousResiduals[filter]
			}
		}
		previous, current = current, previous
	}
	for i := range cfg.filters {
		if !cfg.filters[i] {
			continue
		}
		if cfg.tryRLE {
			analysis.rleResiduals[i].flushRLE()
		}
		if cfg.trySpatialRef {
			analysis.lz77Residuals[i].flushRLE()
		}
	}
	if cfg.optimalPasses > 0 && uint64(width)*uint64(height) <= uint64(cfg.optimalPixels) {
		optimizeLossyAlphaPlans(readPixel, bounds, width, height, cfg, &analysis)
	}
	return analysis
}

func (p *alphaResidualPlan) observe(symbol int) {
	if p.counts[symbol] == 0 {
		if p.n < len(p.symbols) {
			p.symbols[p.n] = uint16(symbol)
		}
		p.n++
	}
	p.counts[symbol]++
}

func (p *alphaResidualPlan) observeLZ77Row(current []uint8, previous []uint8, hasPrevious bool) {
	for x := 0; x < len(current); {
		match := alphaBestSpatialMatch(current, previous, x, hasPrevious)
		if match.length >= alphaMinBackwardRefLength {
			p.flushRLE()
			p.observeCopy(match.length, match.distanceCode)
			x += match.length
			continue
		}
		p.observeRLE(current[x])
		x++
	}
}

type alphaSpatialMatch struct {
	length       int
	distanceCode int
}

func alphaBestSpatialMatch(current []uint8, previous []uint8, start int, hasPrevious bool) alphaSpatialMatch {
	if !hasPrevious {
		return alphaSpatialMatch{}
	}
	best := alphaSpatialMatch{}
	for i, offset := range vp8lDistanceMap {
		if offset.y != 1 {
			continue
		}
		previousStart := start - offset.x
		if previousStart < 0 || previousStart >= len(previous) {
			continue
		}
		match := alphaMatchLength(current, previous, start, previousStart)
		distanceCode := i + 1
		if match > best.length || match == best.length && distanceCode < best.distanceCode {
			best = alphaSpatialMatch{length: match, distanceCode: distanceCode}
		}
	}
	return best
}

func alphaMatchLength(current []uint8, previous []uint8, currentStart int, previousStart int) int {
	n := 0
	for currentStart+n < len(current) && previousStart+n < len(previous) && current[currentStart+n] == previous[previousStart+n] {
		n++
	}
	return n
}

func (p *alphaResidualPlan) observeRLE(value uint8) {
	if !p.run.active {
		p.run = alphaRun{active: true, value: value, length: 1}
		return
	}
	if p.run.value == value {
		p.run.length++
		return
	}
	p.flushRLE()
	p.run = alphaRun{active: true, value: value, length: 1}
}

func (p *alphaResidualPlan) flushRLE() {
	if !p.run.active {
		return
	}
	p.observe(int(p.run.value))
	remaining := p.run.length - 1
	if remaining >= alphaMinBackwardRefLength {
		p.observeCopy(remaining, alphaDistancePrevious)
	} else {
		for ; remaining > 0; remaining-- {
			p.observe(int(p.run.value))
		}
	}
	p.run = alphaRun{}
}

func (p *alphaResidualPlan) observeCopy(length int, distanceCode int) {
	for length > 0 {
		n := length
		if n > alphaMaxBackwardRefLength {
			n = alphaMaxBackwardRefLength
		}
		p.observe(nLiteralCodes + vp8lPrefixCode(n).code)
		distancePrefix := vp8lDistancePrefixCode(distanceCode)
		p.observeDistance(uint8(distancePrefix.code))
		p.hasRefs = true
		length -= n
	}
}

func (p *alphaResidualPlan) observeDistance(symbol uint8) {
	if p.distanceCounts[symbol] == 0 {
		if p.distanceN < len(p.distanceSymbols) {
			p.distanceSymbols[p.distanceN] = symbol
		}
		p.distanceN++
	}
	p.distanceCounts[symbol]++
}

func (r *alphaRun) writeRow(bits *bitWriter, code alphaCode, current []uint8, previous []uint8, hasPrevious bool) {
	for x := 0; x < len(current); {
		match := alphaBestSpatialMatch(current, previous, x, hasPrevious)
		if match.length >= alphaMinBackwardRefLength {
			r.flush(bits, code)
			writeAlphaLZ77Copy(bits, code, match.length, match.distanceCode)
			x += match.length
			continue
		}
		r.write(bits, code, current[x])
		x++
	}
}

func (r *alphaRun) write(bits *bitWriter, code alphaCode, value uint8) {
	if !r.active {
		*r = alphaRun{active: true, value: value, length: 1}
		return
	}
	if r.value == value {
		r.length++
		return
	}
	r.flush(bits, code)
	*r = alphaRun{active: true, value: value, length: 1}
}

func (r *alphaRun) flush(bits *bitWriter, code alphaCode) {
	if !r.active {
		return
	}
	writeAlphaSymbol(bits, code, int(r.value))
	remaining := r.length - 1
	if remaining >= alphaMinBackwardRefLength {
		writeAlphaLZ77Copy(bits, code, remaining, alphaDistancePrevious)
	} else {
		for ; remaining > 0; remaining-- {
			writeAlphaSymbol(bits, code, int(r.value))
		}
	}
	*r = alphaRun{}
}

type vp8lPrefix struct {
	code      int
	extraBits uint8
	extra     uint32
}

func vp8lPrefixCode(value int) vp8lPrefix {
	return vp8lPrefixCodeForCodeCount(value, nLengthCodes)
}

func vp8lDistancePrefixCode(value int) vp8lPrefix {
	return vp8lPrefixCodeForCodeCount(value, nDistanceCodes)
}

func vp8lPrefixCodeForCodeCount(value int, codeCount int) vp8lPrefix {
	if value <= 4 {
		return vp8lPrefix{code: value - 1}
	}
	for code := 4; code < codeCount; code++ {
		extraBits := vp8lPrefixExtraBits(code)
		offset := (2 + code&1) << extraBits
		minValue := offset + 1
		maxValue := offset + 1<<extraBits
		if value >= minValue && value <= maxValue {
			return vp8lPrefix{
				code:      code,
				extraBits: extraBits,
				extra:     uint32(value - minValue),
			}
		}
	}
	code := codeCount - 1
	extraBits := vp8lPrefixExtraBits(code)
	offset := (2 + code&1) << extraBits
	minValue := offset + 1
	return vp8lPrefix{
		code:      code,
		extraBits: extraBits,
		extra:     uint32(value - minValue),
	}
}

func (p alphaResidualPlan) encodable() bool {
	return p.n > 0
}

func alphaPredictor(filter byte, x int, y int, left uint8, above uint8, upperLeft uint8) uint8 {
	switch filter {
	case alphFilterHorizontal:
		return horizontalAlphaPredictor(x, y, left, above)
	case alphFilterVertical:
		return verticalAlphaPredictor(x, y, left, above)
	case alphFilterGradient:
		return gradientAlphaPredictor(x, y, left, above, upperLeft)
	default:
		return 0
	}
}

func horizontalAlphaPredictor(x int, y int, left uint8, above uint8) uint8 {
	if x > 0 {
		return left
	}
	if y > 0 {
		return above
	}
	return 0
}

func verticalAlphaPredictor(x int, y int, left uint8, above uint8) uint8 {
	if y > 0 {
		return above
	}
	if x > 0 {
		return left
	}
	return 0
}

func gradientAlphaPredictor(x int, y int, left uint8, above uint8, upperLeft uint8) uint8 {
	switch {
	case x == 0 && y == 0:
		return 0
	case x == 0:
		return above
	case y == 0:
		return left
	default:
		return uint8(clipInt(int(left)+int(above)-int(upperLeft), 0, 255))
	}
}

func riffChunkSize(payloadSize uint64) uint64 {
	return 8 + payloadSize + payloadSize&1
}

func writeChunkPadding(w *bufio.Writer, payloadSize uint64) error {
	if payloadSize&1 == 0 {
		return nil
	}
	return w.WriteByte(0)
}
