package webp

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"sort"
)

const (
	defaultLossyQuality   = 100
	vp8FirstPartitionMax  = 1<<19 - 1
	vp8xAlphaFlag         = 0x10
	vp8xPayloadSize       = 10
	vp8WorkspaceTopMinMBW = 16
	vp8ChromaCacheMinDim  = 256

	alphCompressionNone  = 0
	alphCompressionVP8L  = 1
	alphFilterNone       = 0
	alphFilterHorizontal = 1
	alphFilterVertical   = 2
	alphFilterGradient   = 3

	alphaMinBackwardRefLength = 4
	alphaMaxBackwardRefLength = 4096
	alphaDistanceAbove        = 0
	alphaDistancePrevious     = 1
	alphaDistanceTopLeft      = 2
	alphaDistanceTopRight     = 3

	alphaCodeLengthCodeCount      = 19
	alphaCodeLengthCodeMaxLength  = 7
	alphaCodeLengthCodeKraft      = 1 << alphaCodeLengthCodeMaxLength
	alphaCodeLengthRepeatPrevious = 16
	alphaCodeLengthRepeatZero     = 17
	alphaCodeLengthRepeatZeroBig  = 18
)

func encodeLossy(w io.Writer, m image.Image, bounds image.Rectangle, width int, height int, quality int, mode Mode) error {
	if width > maxVP8Dimension || height > maxVP8Dimension {
		return fmt.Errorf("webp: image dimensions %dx%d exceed VP8 limit %dx%d", width, height, maxVP8Dimension, maxVP8Dimension)
	}

	readPixel := pixelReaderFor(m)
	readLuma := lumaReaderFor(m)
	readChroma := chromaReaderFor(m)
	alphaConfig := lossyAlphaConfigForMode(mode)
	var alphaAnalysis lossyAlphaAnalysis
	if !lossyStandardImageOpaque(m) {
		alphaAnalysis = analyzeLossyAlphaConfig(readPixel, bounds, width, height, alphaConfig)
	}
	lossyConfig := vp8LossyConfigForModeQuality(mode, quality)
	frame, err := encodeVP8KeyFrameConfig(readLuma, readChroma, bounds, width, height, lossyConfig)
	if err != nil {
		return err
	}
	if alphaAnalysis.hasAlpha {
		return writeLossyExtended(w, readPixel, bounds, width, height, frame, alphaAnalysis, alphaConfig)
	}
	return writeLossySimple(w, frame)
}

func lossyStandardImageOpaque(m image.Image) bool {
	switch img := m.(type) {
	case *image.NRGBA:
		return img.Opaque()
	case *image.RGBA:
		return img.Opaque()
	case *image.Gray:
		return img.Opaque()
	case *image.YCbCr:
		return img.Opaque()
	case *image.Paletted:
		return img.Opaque()
	case *image.Uniform:
		return img.Opaque()
	default:
		return false
	}
}

func writeLossySimple(w io.Writer, frame []byte) error {
	payloadSize := uint64(len(frame))
	riffSize := uint64(4) + riffChunkSize(payloadSize)
	if riffSize > math.MaxUint32 {
		return fmt.Errorf("webp: encoded image is too large")
	}

	bw := bufio.NewWriter(w)
	if err := writeWebPHeader(bw, "VP8 ", uint32(riffSize), uint32(payloadSize)); err != nil {
		return err
	}
	if _, err := bw.Write(frame); err != nil {
		return err
	}
	if err := writeChunkPadding(bw, payloadSize); err != nil {
		return err
	}
	return bw.Flush()
}

func writeLossyExtended(w io.Writer, readPixel pixelReader, bounds image.Rectangle, width int, height int, frame []byte, alphaAnalysis lossyAlphaAnalysis, alphaConfig lossyAlphaConfig) error {
	framePayloadSize := uint64(len(frame))
	alphaPayload, err := makeAlphaPayload(readPixel, bounds, width, height, alphaAnalysis, alphaConfig)
	if err != nil {
		return err
	}
	if framePayloadSize > math.MaxUint32 || alphaPayload.size > math.MaxUint32 {
		return fmt.Errorf("webp: encoded image is too large")
	}

	riffSize := uint64(4) + riffChunkSize(vp8xPayloadSize) + riffChunkSize(alphaPayload.size) + riffChunkSize(framePayloadSize)
	if riffSize > math.MaxUint32 {
		return fmt.Errorf("webp: encoded image is too large")
	}

	bw := bufio.NewWriter(w)
	if err := writeRIFFHeader(bw, uint32(riffSize)); err != nil {
		return err
	}
	if err := writeVP8XChunk(bw, width, height); err != nil {
		return err
	}
	if err := writeAlphaChunk(bw, readPixel, bounds, alphaPayload); err != nil {
		return err
	}
	if err := writeChunkHeader(bw, "VP8 ", uint32(framePayloadSize)); err != nil {
		return err
	}
	if _, err := bw.Write(frame); err != nil {
		return err
	}
	if err := writeChunkPadding(bw, framePayloadSize); err != nil {
		return err
	}
	return bw.Flush()
}

func writeVP8XChunk(w *bufio.Writer, width int, height int) error {
	if err := writeChunkHeader(w, "VP8X", vp8xPayloadSize); err != nil {
		return err
	}
	if err := w.WriteByte(vp8xAlphaFlag); err != nil {
		return err
	}
	if err := w.WriteByte(0); err != nil {
		return err
	}
	if err := w.WriteByte(0); err != nil {
		return err
	}
	if err := w.WriteByte(0); err != nil {
		return err
	}
	if err := writeUint24LE(w, uint32(width-1)); err != nil {
		return err
	}
	return writeUint24LE(w, uint32(height-1))
}

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
	filters       [4]bool
	tryRLE        bool
	trySpatialRef bool
}

func lossyAlphaConfigForMode(mode Mode) lossyAlphaConfig {
	cfg := lossyAlphaConfig{
		filters:       [4]bool{true, true, true, true},
		tryRLE:        true,
		trySpatialRef: true,
	}
	switch mode {
	case ModeFast:
		cfg.filters = [4]bool{true, false, false, false}
		cfg.trySpatialRef = false
	case ModeLowMemory:
		cfg.trySpatialRef = false
	}
	return cfg
}

func makeAlphaPayload(readPixel pixelReader, bounds image.Rectangle, width int, height int, analysis lossyAlphaAnalysis, cfg lossyAlphaConfig) (alphaPayload, error) {
	rawSize := uint64(1) + uint64(width)*uint64(height)
	if rawSize > math.MaxUint32 {
		return alphaPayload{}, fmt.Errorf("webp: encoded image is too large")
	}

	best := alphaPayload{size: rawSize}
	var candidateBuf [12]alphaPayloadCandidate
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
	stream, err := encodeAlphaVP8LStream(readPixel, bounds, width, height, bestCandidate.filter, bestCandidate.code)
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
	return candidates
}

func alphaPayloadCandidateSize(candidate alphaPayloadCandidate) uint64 {
	return 1 + alphaVP8LStreamSize(candidate.plan, candidate.code)
}

func encodeAlphaVP8LStream(readPixel pixelReader, bounds image.Rectangle, width int, height int, filter byte, code alphaCode) ([]byte, error) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	bits := newBitWriter(bw)
	writeAlphaVP8LImageStream(bits, readPixel, bounds, width, height, filter, code)
	if err := bits.flush(); err != nil {
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeAlphaVP8LImageStream(bits *bitWriter, readPixel pixelReader, bounds image.Rectangle, width int, height int, filter byte, code alphaCode) {
	bits.writeBits(0, 1) // no transforms
	bits.writeBits(0, 1) // no color cache
	bits.writeBits(0, 1) // no meta prefix image

	writeAlphaGreenTree(bits, code)
	writeSimpleTree(bits, 0)
	writeSimpleTree(bits, 0)
	writeSimpleTree(bits, 0)
	if code.lz77 {
		writeAlphaDistanceTree(bits, code)
	} else {
		writeSimpleTree(bits, 0)
	}

	if code.lz77 && code.rowCopy {
		writeAlphaLZ77Bits(bits, readPixel, bounds, width, height, filter, code)
	} else if code.lz77 {
		writeAlphaRLEBits(bits, readPixel, bounds, width, height, filter, code)
	} else if code.n != 1 {
		writeAlphaResidualBits(bits, readPixel, bounds, width, height, filter, code)
	}
}

func writeAlphaGreenTree(bits *bitWriter, code alphaCode) {
	if code.normal {
		writeAlphaNormalTree(bits, code.lengths[:])
		return
	}
	switch code.n {
	case 1:
		writeSimpleTree(bits, uint8(code.symbols[0]))
	case 2:
		writeTwoSymbolTree(bits, uint8(code.symbols[0]), uint8(code.symbols[1]))
	default:
		writeAlphaNormalTree(bits, code.lengths[:])
	}
}

func writeAlphaDistanceTree(bits *bitWriter, code alphaCode) {
	if code.distanceNormal {
		writeAlphaNormalTree(bits, code.distanceLengths[:])
		return
	}
	switch code.distanceN {
	case 1:
		writeSimpleTree(bits, code.distanceSymbols[0])
	case 2:
		writeTwoSymbolTree(bits, code.distanceSymbols[0], code.distanceSymbols[1])
	default:
		writeSimpleTree(bits, 0)
	}
}

func alphaVP8LStreamSize(plan alphaResidualPlan, code alphaCode) uint64 {
	bits := uint64(3)
	bits += alphaGreenTreeBits(code)
	bits += 3 * alphaSimpleTreeBits(0)
	if code.lz77 {
		bits += alphaDistanceTreeBits(code)
	} else {
		bits += alphaSimpleTreeBits(0)
	}
	bits += alphaDataBits(plan, code)
	return (bits + 7) >> 3
}

func alphaGreenTreeBits(code alphaCode) uint64 {
	if code.normal {
		return alphaNormalTreeBits(code.lengths[:])
	}
	switch code.n {
	case 1:
		return alphaSimpleTreeBits(uint8(code.symbols[0]))
	case 2:
		return alphaTwoSymbolTreeBits(uint8(code.symbols[0]))
	default:
		return alphaNormalTreeBits(code.lengths[:])
	}
}

func alphaDistanceTreeBits(code alphaCode) uint64 {
	if code.distanceNormal {
		return alphaNormalTreeBits(code.distanceLengths[:])
	}
	switch code.distanceN {
	case 1:
		return alphaSimpleTreeBits(code.distanceSymbols[0])
	case 2:
		return alphaTwoSymbolTreeBits(code.distanceSymbols[0])
	default:
		return alphaSimpleTreeBits(0)
	}
}

func alphaSimpleTreeBits(symbol uint8) uint64 {
	if symbol < 2 {
		return 4
	}
	return 11
}

func alphaTwoSymbolTreeBits(symbol0 uint8) uint64 {
	if symbol0 < 2 {
		return 12
	}
	return 19
}

func alphaNormalTreeBits(lengths []uint8) uint64 {
	baseBits, tokenCount := alphaNormalTreeBaseBits(lengths)
	return baseBits + alphaCodeLengthLimitBits(int(tokenCount), len(lengths))
}

func alphaNormalTreeBaseBits(lengths []uint8) (uint64, uint16) {
	usage, tokenCount := alphaCodeLengthCodeUsageForLengths(lengths)
	codeLengthCodeLengths, _ := alphaCodeLengthCodeLengthsForUsage(usage)
	tokenBits := alphaCodeLengthTokenBitsWithCodeLengths(lengths, codeLengthCodeLengths)
	nCodes := alphaCodeLengthCodeCountForUsage(usage)
	return uint64(1+4+nCodes*3) + tokenBits, uint16(tokenCount)
}

func alphaCodeLengthLimitBits(maxSymbol int, alphabetSize int) uint64 {
	if maxSymbol >= alphabetSize {
		return 1
	}
	if maxSymbol < 2 {
		maxSymbol = 2
	}
	value := maxSymbol - 2
	nBits := uint8(2)
	for value >= 1<<nBits {
		nBits += 2
	}
	return uint64(1 + 3 + nBits)
}

func alphaDataBits(plan alphaResidualPlan, code alphaCode) uint64 {
	bits := alphaLiteralDataBits(plan, code)
	if code.lz77 {
		bits += alphaLengthExtraBits(plan)
		bits += alphaDistanceDataBits(plan, code)
	}
	return bits
}

func alphaLiteralDataBits(plan alphaResidualPlan, code alphaCode) uint64 {
	if code.normal {
		var bits uint64
		for symbol, count := range plan.counts {
			bits += uint64(count) * uint64(code.lengths[symbol])
		}
		return bits
	}
	switch code.n {
	case 1:
		return 0
	case 2:
		return alphaTotalSymbolCount(plan.counts[:])
	default:
		var bits uint64
		for symbol, count := range plan.counts {
			bits += uint64(count) * uint64(code.lengths[symbol])
		}
		return bits
	}
}

func alphaLengthExtraBits(plan alphaResidualPlan) uint64 {
	var bits uint64
	for code := 0; code < nLengthCodes; code++ {
		bits += uint64(plan.counts[nLiteralCodes+code]) * uint64(vp8lLengthPrefixExtraBits(code))
	}
	return bits
}

func vp8lLengthPrefixExtraBits(code int) uint8 {
	return vp8lPrefixExtraBits(code)
}

func vp8lPrefixExtraBits(code int) uint8 {
	if code < 4 {
		return 0
	}
	return uint8((code - 2) >> 1)
}

func alphaDistanceDataBits(plan alphaResidualPlan, code alphaCode) uint64 {
	if code.distanceNormal {
		var bits uint64
		for symbol, count := range plan.distanceCounts {
			bits += uint64(count) * uint64(code.distanceLengths[symbol])
		}
		return bits
	}
	switch code.distanceN {
	case 1:
		return 0
	case 2:
		return alphaTotalSymbolCount(plan.distanceCounts[:])
	default:
		return 0
	}
}

func alphaTotalSymbolCount(counts []uint32) uint64 {
	var total uint64
	for _, count := range counts {
		total += uint64(count)
	}
	return total
}

func writeAlphaNormalTree(bits *bitWriter, lengths []uint8) {
	tokens := alphaCodeLengthTokens(lengths)
	usage := alphaCodeLengthCodeUsageForTokens(tokens)
	codeLengthCodeLengths, nCodes := alphaCodeLengthCodeLengthsForUsage(usage)
	codes := canonicalAlphaCodeLengthCodes(codeLengthCodeLengths)

	bits.writeBits(0, 1)
	bits.writeBits(uint32(nCodes-4), 4)
	for _, symbol := range normalCodeLengthCodeOrder[:nCodes] {
		bits.writeBits(uint32(codeLengthCodeLengths[symbol]), 3)
	}
	writeAlphaCodeLengthLimit(bits, len(tokens), len(lengths))
	for _, token := range tokens {
		length := codeLengthCodeLengths[token.symbol]
		bits.writeBits(uint32(reverseBits(codes[token.symbol], length)), length)
		bits.writeBits(token.extra, token.extraBits)
	}
}

type alphaCodeLengthToken struct {
	symbol    uint8
	extraBits uint8
	extra     uint32
}

func alphaCodeLengthCodeCountForLengths(lengths []uint8) int {
	usage, _ := alphaCodeLengthCodeUsageForLengths(lengths)
	return alphaCodeLengthCodeCountForUsage(usage)
}

func alphaCodeLengthCodeUsageForLengths(lengths []uint8) ([alphaCodeLengthCodeCount]uint32, int) {
	maxSymbol := alphaCodeLengthLimit(lengths)
	var usage [alphaCodeLengthCodeCount]uint32
	tokenCount := 0
	for i := 0; i < maxSymbol; {
		length := lengths[i]
		if length != 0 {
			run := 1
			for i+run < maxSymbol && lengths[i+run] == length {
				run++
			}
			tokenCount += countAlphaRepeatedCodeLengthRunCodeSymbols(&usage, length, run)
			i += run
			continue
		}
		run := 1
		for i+run < maxSymbol && lengths[i+run] == 0 {
			run++
		}
		tokenCount += countAlphaZeroLengthRunCodeSymbols(&usage, run)
		i += run
	}
	return usage, tokenCount
}

func alphaCodeLengthCodeCountForTokens(tokens []alphaCodeLengthToken) int {
	usage := alphaCodeLengthCodeUsageForTokens(tokens)
	return alphaCodeLengthCodeCountForUsage(usage)
}

func alphaCodeLengthCodeUsageForTokens(tokens []alphaCodeLengthToken) [alphaCodeLengthCodeCount]uint32 {
	var usage [alphaCodeLengthCodeCount]uint32
	for _, token := range tokens {
		usage[token.symbol]++
	}
	return usage
}

func alphaCodeLengthCodeCountForUsage(usage [alphaCodeLengthCodeCount]uint32) int {
	nCodes := 4
	for i, symbol := range normalCodeLengthCodeOrder {
		if usage[symbol] != 0 && i+1 > nCodes {
			nCodes = i + 1
		}
	}
	return nCodes
}

func alphaCodeLengthCodeLengthsForUsage(usage [alphaCodeLengthCodeCount]uint32) ([alphaCodeLengthCodeCount]uint8, int) {
	var lengths [alphaCodeLengthCodeCount]uint8
	var symbols [alphaCodeLengthCodeCount]huffmanSymbol
	n := 0
	for symbol, count := range usage {
		if count == 0 {
			continue
		}
		symbols[n] = huffmanSymbol{count: count, symbol: symbol}
		n++
	}
	switch n {
	case 0:
		return lengths, 4
	case 1:
		lengths[symbols[0].symbol] = 1
		return lengths, alphaCodeLengthCodeCountForUsage(usage)
	case 2:
		lengths[symbols[0].symbol] = 1
		lengths[symbols[1].symbol] = 1
		return lengths, alphaCodeLengthCodeCountForUsage(usage)
	}

	for i := 1; i < n; i++ {
		sym := symbols[i]
		j := i - 1
		for ; j >= 0; j-- {
			if symbols[j].count > sym.count || symbols[j].count == sym.count && symbols[j].symbol < sym.symbol {
				break
			}
			symbols[j+1] = symbols[j]
		}
		symbols[j+1] = sym
	}

	if !assignAlphaCodeLengthCodeLengths(&lengths, symbols[:n]) {
		assignBalancedAlphaCodeLengthCodeLengths(&lengths, symbols[:n])
	}
	return lengths, alphaCodeLengthCodeCountForUsage(usage)
}

func assignAlphaCodeLengthCodeLengths(lengths *[alphaCodeLengthCodeCount]uint8, symbols []huffmanSymbol) bool {
	const inf = ^uint64(0) >> 2

	var prefix [alphaCodeLengthCodeCount + 1]uint64
	for i, sym := range symbols {
		prefix[i+1] = prefix[i] + uint64(sym.count)
	}

	var dpPrev [alphaCodeLengthCodeCount + 1][alphaCodeLengthCodeKraft + 1]uint64
	var dpNext [alphaCodeLengthCodeCount + 1][alphaCodeLengthCodeKraft + 1]uint64
	var choice [alphaCodeLengthCodeMaxLength + 1][alphaCodeLengthCodeCount + 1][alphaCodeLengthCodeKraft + 1]int8
	for used := range dpPrev {
		for kraft := range dpPrev[used] {
			dpPrev[used][kraft] = inf
			dpNext[used][kraft] = inf
		}
	}
	for length := range choice {
		for used := range choice[length] {
			for kraft := range choice[length][used] {
				choice[length][used][kraft] = -1
			}
		}
	}
	dpPrev[0][0] = 0

	n := len(symbols)
	for length := 1; length <= alphaCodeLengthCodeMaxLength; length++ {
		for used := range dpNext {
			for kraft := range dpNext[used] {
				dpNext[used][kraft] = inf
			}
		}
		unit := 1 << (alphaCodeLengthCodeMaxLength - length)
		for used := 0; used <= n; used++ {
			for kraft := 0; kraft <= alphaCodeLengthCodeKraft; kraft++ {
				base := dpPrev[used][kraft]
				if base == inf {
					continue
				}
				for count := 0; used+count <= n; count++ {
					nextKraft := kraft + count*unit
					if nextKraft > alphaCodeLengthCodeKraft {
						break
					}
					cost := base + uint64(length)*(prefix[used+count]-prefix[used])
					if cost >= dpNext[used+count][nextKraft] {
						continue
					}
					dpNext[used+count][nextKraft] = cost
					choice[length][used+count][nextKraft] = int8(count)
				}
			}
		}
		dpPrev, dpNext = dpNext, dpPrev
	}
	if dpPrev[n][alphaCodeLengthCodeKraft] == inf {
		return false
	}

	used := n
	kraft := alphaCodeLengthCodeKraft
	var countsByLength [alphaCodeLengthCodeMaxLength + 1]int
	for length := alphaCodeLengthCodeMaxLength; length >= 1; length-- {
		count := int(choice[length][used][kraft])
		if count < 0 {
			return false
		}
		countsByLength[length] = count
		used -= count
		kraft -= count * (1 << (alphaCodeLengthCodeMaxLength - length))
	}
	if used != 0 || kraft != 0 {
		return false
	}

	i := 0
	for length := 1; length <= alphaCodeLengthCodeMaxLength; length++ {
		for range countsByLength[length] {
			lengths[symbols[i].symbol] = uint8(length)
			i++
		}
	}
	return i == n
}

func assignBalancedAlphaCodeLengthCodeLengths(lengths *[alphaCodeLengthCodeCount]uint8, symbols []huffmanSymbol) {
	longLength := ceilLog2(len(symbols))
	shortLength := longLength - 1
	shortCount := (1 << longLength) - len(symbols)
	for i, sym := range symbols {
		length := longLength
		if i < shortCount {
			length = shortLength
		}
		lengths[sym.symbol] = uint8(length)
	}
}

func countAlphaZeroLengthRunCodeSymbols(usage *[alphaCodeLengthCodeCount]uint32, run int) int {
	tokenCount := 0
	for run > 0 {
		switch {
		case run >= 11:
			n := run
			if n > 138 {
				n = 138
			}
			usage[alphaCodeLengthRepeatZeroBig]++
			tokenCount++
			run -= n
		case run >= 3:
			n := run
			if n > 10 {
				n = 10
			}
			usage[alphaCodeLengthRepeatZero]++
			tokenCount++
			run -= n
		default:
			usage[0]++
			tokenCount++
			run--
		}
	}
	return tokenCount
}

func countAlphaRepeatedCodeLengthRunCodeSymbols(usage *[alphaCodeLengthCodeCount]uint32, length uint8, run int) int {
	usage[length]++
	tokenCount := 1
	run--
	for run > 0 {
		if run >= 3 {
			n := alphaRepeatedCodeLengthRunChunk(run)
			usage[alphaCodeLengthRepeatPrevious]++
			tokenCount++
			run -= n
			continue
		}
		usage[length]++
		tokenCount++
		run--
	}
	return tokenCount
}

func alphaCodeLengthTokenBits(lengths []uint8) (uint64, int) {
	usage, tokenCount := alphaCodeLengthCodeUsageForLengths(lengths)
	codeLengthCodeLengths, _ := alphaCodeLengthCodeLengthsForUsage(usage)
	return alphaCodeLengthTokenBitsWithCodeLengths(lengths, codeLengthCodeLengths), tokenCount
}

func alphaCodeLengthTokenBitsWithCodeLengths(lengths []uint8, codeLengthCodeLengths [alphaCodeLengthCodeCount]uint8) uint64 {
	maxSymbol := alphaCodeLengthLimit(lengths)
	var bits uint64
	for i := 0; i < maxSymbol; {
		length := lengths[i]
		if length != 0 {
			run := 1
			for i+run < maxSymbol && lengths[i+run] == length {
				run++
			}
			bits += alphaRepeatedCodeLengthRunTokenBitsWithCodeLengths(length, run, codeLengthCodeLengths)
			i += run
			continue
		}
		run := 1
		for i+run < maxSymbol && lengths[i+run] == 0 {
			run++
		}
		bits += alphaZeroLengthRunTokenBitsWithCodeLengths(run, codeLengthCodeLengths)
		i += run
	}
	return bits
}

func alphaRepeatedCodeLengthRunTokenBitsWithCodeLengths(length uint8, run int, codeLengthCodeLengths [alphaCodeLengthCodeCount]uint8) uint64 {
	bits := uint64(codeLengthCodeLengths[length])
	run--
	for run > 0 {
		if run >= 3 {
			n := alphaRepeatedCodeLengthRunChunk(run)
			bits += uint64(codeLengthCodeLengths[alphaCodeLengthRepeatPrevious] + 2)
			run -= n
			continue
		}
		bits += uint64(codeLengthCodeLengths[length])
		run--
	}
	return bits
}

func alphaZeroLengthRunTokenBitsWithCodeLengths(run int, codeLengthCodeLengths [alphaCodeLengthCodeCount]uint8) uint64 {
	var bits uint64
	for run > 0 {
		switch {
		case run >= 11:
			n := run
			if n > 138 {
				n = 138
			}
			bits += uint64(codeLengthCodeLengths[alphaCodeLengthRepeatZeroBig] + 7)
			run -= n
		case run >= 3:
			n := run
			if n > 10 {
				n = 10
			}
			bits += uint64(codeLengthCodeLengths[alphaCodeLengthRepeatZero] + 3)
			run -= n
		default:
			bits += uint64(codeLengthCodeLengths[0])
			run--
		}
	}
	return bits
}

func alphaCodeLengthTokens(lengths []uint8) []alphaCodeLengthToken {
	maxSymbol := alphaCodeLengthLimit(lengths)
	tokens := make([]alphaCodeLengthToken, 0, maxSymbol)
	for i := 0; i < maxSymbol; {
		length := lengths[i]
		if length != 0 {
			run := 1
			for i+run < maxSymbol && lengths[i+run] == length {
				run++
			}
			tokens = appendAlphaRepeatedCodeLengthRun(tokens, length, run)
			i += run
			continue
		}
		run := 1
		for i+run < maxSymbol && lengths[i+run] == 0 {
			run++
		}
		tokens = appendAlphaZeroLengthRun(tokens, run)
		i += run
	}
	return tokens
}

func alphaCodeLengthLimit(lengths []uint8) int {
	for i := len(lengths) - 1; i >= 0; i-- {
		if lengths[i] == 0 {
			continue
		}
		if i < 1 {
			return 2
		}
		return i + 1
	}
	return 2
}

func appendAlphaRepeatedCodeLengthRun(tokens []alphaCodeLengthToken, length uint8, run int) []alphaCodeLengthToken {
	tokens = append(tokens, alphaCodeLengthToken{symbol: length})
	run--
	for run > 0 {
		if run >= 3 {
			n := alphaRepeatedCodeLengthRunChunk(run)
			tokens = append(tokens, alphaCodeLengthToken{
				symbol:    alphaCodeLengthRepeatPrevious,
				extraBits: 2,
				extra:     uint32(n - 3),
			})
			run -= n
			continue
		}
		tokens = append(tokens, alphaCodeLengthToken{symbol: length})
		run--
	}
	return tokens
}

func alphaRepeatedCodeLengthRunChunk(run int) int {
	if run <= 6 {
		return run
	}
	if run%6 == 1 {
		return 4
	}
	return 6
}

func appendAlphaZeroLengthRun(tokens []alphaCodeLengthToken, run int) []alphaCodeLengthToken {
	for run > 0 {
		switch {
		case run >= 11:
			n := run
			if n > 138 {
				n = 138
			}
			tokens = append(tokens, alphaCodeLengthToken{
				symbol:    alphaCodeLengthRepeatZeroBig,
				extraBits: 7,
				extra:     uint32(n - 11),
			})
			run -= n
		case run >= 3:
			n := run
			if n > 10 {
				n = 10
			}
			tokens = append(tokens, alphaCodeLengthToken{
				symbol:    alphaCodeLengthRepeatZero,
				extraBits: 3,
				extra:     uint32(n - 3),
			})
			run -= n
		default:
			tokens = append(tokens, alphaCodeLengthToken{symbol: 0})
			run--
		}
	}
	return tokens
}

func writeAlphaCodeLengthLimit(bits *bitWriter, maxSymbol int, alphabetSize int) {
	if maxSymbol >= alphabetSize {
		bits.writeBits(0, 1)
		return
	}
	if maxSymbol < 2 {
		maxSymbol = 2
	}
	value := maxSymbol - 2
	nBits := uint8(2)
	selector := uint32(0)
	for value >= 1<<nBits {
		nBits += 2
		selector++
	}
	bits.writeBits(1, 1)
	bits.writeBits(selector, 3)
	bits.writeBits(uint32(value), nBits)
}

func canonicalAlphaCodeLengthCodes(lengths [alphaCodeLengthCodeCount]uint8) [alphaCodeLengthCodeCount]uint16 {
	var histogram [8]uint16
	for _, length := range lengths {
		if length != 0 {
			histogram[length]++
		}
	}

	code := uint16(0)
	var nextCodes [8]uint16
	for length := 1; length < len(nextCodes); length++ {
		code = (code + histogram[length-1]) << 1
		nextCodes[length] = code
	}

	var codes [alphaCodeLengthCodeCount]uint16
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		codes[symbol] = nextCodes[length]
		nextCodes[length]++
	}
	return codes
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

func writeAlphaLZ77Copy(bits *bitWriter, code alphaCode, length int, distanceSymbol uint8) {
	for length > 0 {
		n := length
		if n > alphaMaxBackwardRefLength {
			n = alphaMaxBackwardRefLength
		}
		prefix := vp8lPrefixCode(n)
		writeAlphaSymbol(bits, code, nLiteralCodes+prefix.code)
		bits.writeBits(prefix.extra, prefix.extraBits)
		writeAlphaDistanceSymbol(bits, code, distanceSymbol)
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
	hasAlpha      bool
	residuals     [4]alphaResidualPlan
	rleResiduals  [4]alphaResidualPlan
	lz77Residuals [4]alphaResidualPlan
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
			p.observeCopy(match.length, match.distanceSymbol)
			x += match.length
			continue
		}
		p.observeRLE(current[x])
		x++
	}
}

type alphaSpatialMatch struct {
	length         int
	distanceSymbol uint8
}

func alphaBestSpatialMatch(current []uint8, previous []uint8, start int, hasPrevious bool) alphaSpatialMatch {
	if !hasPrevious {
		return alphaSpatialMatch{}
	}
	best := alphaSpatialMatch{
		length:         alphaMatchLength(current, previous, start, start),
		distanceSymbol: alphaDistanceAbove,
	}
	if start > 0 {
		if match := alphaMatchLength(current, previous, start, start-1); match > best.length {
			best = alphaSpatialMatch{length: match, distanceSymbol: alphaDistanceTopLeft}
		}
	}
	if start+1 < len(previous) {
		if match := alphaMatchLength(current, previous, start, start+1); match > best.length {
			best = alphaSpatialMatch{length: match, distanceSymbol: alphaDistanceTopRight}
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

func (p *alphaResidualPlan) observeCopy(length int, distanceSymbol uint8) {
	for length > 0 {
		n := length
		if n > alphaMaxBackwardRefLength {
			n = alphaMaxBackwardRefLength
		}
		p.observe(nLiteralCodes + vp8lPrefixCode(n).code)
		p.observeDistance(distanceSymbol)
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
			writeAlphaLZ77Copy(bits, code, match.length, match.distanceSymbol)
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

type alphaCode struct {
	n               int
	symbols         [2]uint16
	lengths         [nLiteralCodes + nLengthCodes]uint8
	codes           [nLiteralCodes + nLengthCodes]uint16
	distanceN       int
	distanceSymbols [2]uint8
	distanceLengths [nDistanceCodes]uint8
	distanceCodes   [nDistanceCodes]uint16
	lz77            bool
	rowCopy         bool
	normal          bool
	distanceNormal  bool
}

func alphaCodeFor(plan alphaResidualPlan) (alphaCode, bool) {
	var scratch alphaHuffmanScratch
	return alphaCodeForWithScratch(plan, &scratch)
}

func alphaCodeForWithScratch(plan alphaResidualPlan, scratch *alphaHuffmanScratch) (alphaCode, bool) {
	switch plan.n {
	case 0:
		return alphaCode{}, false
	case 1:
		if plan.symbols[0] < nLiteralCodes {
			return alphaCode{n: 1, symbols: plan.symbols}, true
		}
	case 2:
		if plan.symbols[0] >= nLiteralCodes || plan.symbols[1] >= nLiteralCodes {
			break
		}
		if plan.symbols[1] < 2 && plan.symbols[0] >= 2 {
			plan.symbols[0], plan.symbols[1] = plan.symbols[1], plan.symbols[0]
		}
		return alphaCode{n: 2, symbols: plan.symbols}, true
	}
	lengths, ok := huffmanCodeLengthsWithScratch(plan.counts, scratch)
	if !ok {
		return alphaCode{}, false
	}
	codes := canonicalCodes(lengths)
	distanceN, distanceSymbols, distanceLengths, distanceCodes, distanceNormal, ok := alphaDistanceCodeForWithScratch(plan, scratch)
	if !ok {
		return alphaCode{}, false
	}
	return alphaCode{
		n:               plan.n,
		lengths:         lengths,
		codes:           codes,
		distanceN:       distanceN,
		distanceSymbols: distanceSymbols,
		distanceLengths: distanceLengths,
		distanceCodes:   distanceCodes,
		normal:          true,
		distanceNormal:  distanceNormal,
	}, true
}

func alphaDistanceCodeFor(plan alphaResidualPlan) (int, [2]uint8, [nDistanceCodes]uint8, [nDistanceCodes]uint16, bool, bool) {
	var scratch alphaHuffmanScratch
	return alphaDistanceCodeForWithScratch(plan, &scratch)
}

func alphaDistanceCodeForWithScratch(plan alphaResidualPlan, scratch *alphaHuffmanScratch) (int, [2]uint8, [nDistanceCodes]uint8, [nDistanceCodes]uint16, bool, bool) {
	if plan.distanceN <= 2 {
		return plan.distanceN, plan.distanceSymbols, [nDistanceCodes]uint8{}, [nDistanceCodes]uint16{}, false, true
	}
	lengths, ok := huffmanDistanceCodeLengthsWithScratch(plan.distanceCounts, scratch)
	if !ok {
		return 0, [2]uint8{}, [nDistanceCodes]uint8{}, [nDistanceCodes]uint16{}, false, false
	}
	return plan.distanceN, plan.distanceSymbols, lengths, canonicalDistanceCodes(lengths), true, true
}

type huffmanNode struct {
	freq   uint64
	symbol int
	left   int
	right  int
}

const (
	maxAlphaHuffmanSymbols = nColorCacheGreenCodes
	maxAlphaHuffmanNodes   = maxAlphaHuffmanSymbols*2 - 1
)

type huffmanSymbol struct {
	count  uint32
	symbol int
}

type alphaHuffmanScratch struct {
	nodes  [maxAlphaHuffmanNodes]huffmanNode
	active [maxAlphaHuffmanSymbols]int
}

func huffmanCodeLengths(counts [nLiteralCodes + nLengthCodes]uint32) ([nLiteralCodes + nLengthCodes]uint8, bool) {
	var lengths [nLiteralCodes + nLengthCodes]uint8
	return lengths, huffmanCodeLengthsInto(lengths[:], counts[:])
}

func huffmanCodeLengthsWithScratch(counts [nLiteralCodes + nLengthCodes]uint32, scratch *alphaHuffmanScratch) ([nLiteralCodes + nLengthCodes]uint8, bool) {
	var lengths [nLiteralCodes + nLengthCodes]uint8
	return lengths, huffmanCodeLengthsIntoScratch(lengths[:], counts[:], scratch)
}

func huffmanDistanceCodeLengths(counts [nDistanceCodes]uint32) ([nDistanceCodes]uint8, bool) {
	var lengths [nDistanceCodes]uint8
	return lengths, huffmanCodeLengthsInto(lengths[:], counts[:])
}

func huffmanDistanceCodeLengthsWithScratch(counts [nDistanceCodes]uint32, scratch *alphaHuffmanScratch) ([nDistanceCodes]uint8, bool) {
	var lengths [nDistanceCodes]uint8
	return lengths, huffmanCodeLengthsIntoScratch(lengths[:], counts[:], scratch)
}

func huffmanCodeLengthsInto(lengths []uint8, counts []uint32) bool {
	var scratch alphaHuffmanScratch
	return huffmanCodeLengthsIntoScratch(lengths, counts, &scratch)
}

func huffmanCodeLengthsIntoScratch(lengths []uint8, counts []uint32, scratch *alphaHuffmanScratch) bool {
	if len(counts) > maxAlphaHuffmanSymbols || len(lengths) < len(counts) {
		return false
	}
	nodes := scratch.nodes[:0]
	active := scratch.active[:0]
	for symbol, count := range counts {
		if count == 0 {
			continue
		}
		nodes = append(nodes, huffmanNode{freq: uint64(count), symbol: symbol, left: -1, right: -1})
		active = append(active, len(nodes)-1)
	}
	if len(active) == 0 {
		return false
	}
	if len(active) == 1 {
		lengths[nodes[active[0]].symbol] = 1
		return true
	}
	if len(active) == 2 {
		lengths[nodes[active[0]].symbol] = 1
		lengths[nodes[active[1]].symbol] = 1
		return true
	}

	for len(active) > 1 {
		first, second := twoSmallestHuffmanNodes(nodes, active)
		a, b := active[first], active[second]
		if first < second {
			active = append(active[:second], active[second+1:]...)
			active = append(active[:first], active[first+1:]...)
		} else {
			active = append(active[:first], active[first+1:]...)
			active = append(active[:second], active[second+1:]...)
		}
		nodes = append(nodes, huffmanNode{
			freq:   nodes[a].freq + nodes[b].freq,
			symbol: minInt(nodes[a].symbol, nodes[b].symbol),
			left:   a,
			right:  b,
		})
		active = append(active, len(nodes)-1)
	}

	if !assignHuffmanLengths(lengths[:], nodes, active[0], 0) {
		return balancedHuffmanCodeLengthsInto(lengths, counts)
	}
	return true
}

func balancedHuffmanCodeLengths(counts [nLiteralCodes + nLengthCodes]uint32) ([nLiteralCodes + nLengthCodes]uint8, bool) {
	var lengths [nLiteralCodes + nLengthCodes]uint8
	return lengths, balancedHuffmanCodeLengthsInto(lengths[:], counts[:])
}

func balancedHuffmanCodeLengthsInto(lengths []uint8, counts []uint32) bool {
	var symbols []huffmanSymbol
	for symbol, count := range counts {
		if count == 0 {
			continue
		}
		symbols = append(symbols, huffmanSymbol{count: count, symbol: symbol})
	}
	switch len(symbols) {
	case 0:
		return false
	case 1:
		lengths[symbols[0].symbol] = 1
		return true
	case 2:
		lengths[symbols[0].symbol] = 1
		lengths[symbols[1].symbol] = 1
		return true
	}

	sort.Slice(symbols, func(i int, j int) bool {
		if symbols[i].count != symbols[j].count {
			return symbols[i].count > symbols[j].count
		}
		return symbols[i].symbol < symbols[j].symbol
	})

	longLength := ceilLog2(len(symbols))
	shortLength := longLength - 1
	shortCount := (1 << longLength) - len(symbols)
	for i, sym := range symbols {
		length := longLength
		if i < shortCount {
			length = shortLength
		}
		lengths[sym.symbol] = uint8(length)
	}
	return true
}

func ceilLog2(n int) int {
	length := 0
	value := 1
	for value < n {
		value <<= 1
		length++
	}
	return length
}

func twoSmallestHuffmanNodes(nodes []huffmanNode, active []int) (int, int) {
	first, second := -1, -1
	for i := range active {
		if first < 0 || lessHuffmanNode(nodes[active[i]], nodes[active[first]]) {
			second = first
			first = i
		} else if second < 0 || lessHuffmanNode(nodes[active[i]], nodes[active[second]]) {
			second = i
		}
	}
	return first, second
}

func lessHuffmanNode(a huffmanNode, b huffmanNode) bool {
	if a.freq != b.freq {
		return a.freq < b.freq
	}
	return a.symbol < b.symbol
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func assignHuffmanLengths(lengths []uint8, nodes []huffmanNode, index int, depth uint8) bool {
	node := nodes[index]
	if node.left < 0 && node.right < 0 {
		if depth == 0 || depth > 15 {
			return false
		}
		lengths[node.symbol] = depth
		return true
	}
	nextDepth := depth + 1
	if nextDepth > 15 {
		return false
	}
	return assignHuffmanLengths(lengths, nodes, node.left, nextDepth) &&
		assignHuffmanLengths(lengths, nodes, node.right, nextDepth)
}

func canonicalCodes(lengths [nLiteralCodes + nLengthCodes]uint8) [nLiteralCodes + nLengthCodes]uint16 {
	var histogram [16]uint16
	for _, length := range lengths {
		if length != 0 {
			histogram[length]++
		}
	}

	code := uint16(0)
	var nextCodes [16]uint16
	for length := 1; length < len(nextCodes); length++ {
		code = (code + histogram[length-1]) << 1
		nextCodes[length] = code
	}

	var codes [nLiteralCodes + nLengthCodes]uint16
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		codes[symbol] = nextCodes[length]
		nextCodes[length]++
	}
	return codes
}

func canonicalDistanceCodes(lengths [nDistanceCodes]uint8) [nDistanceCodes]uint16 {
	var histogram [16]uint16
	for _, length := range lengths {
		if length != 0 {
			histogram[length]++
		}
	}

	code := uint16(0)
	var nextCodes [16]uint16
	for length := 1; length < len(nextCodes); length++ {
		code = (code + histogram[length-1]) << 1
		nextCodes[length] = code
	}

	var codes [nDistanceCodes]uint16
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		codes[symbol] = nextCodes[length]
		nextCodes[length]++
	}
	return codes
}

func reverseBits(v uint16, n uint8) uint16 {
	var r uint16
	for i := uint8(0); i < n; i++ {
		r = r<<1 | v&1
		v >>= 1
	}
	return r
}

var normalCodeLengthCodeOrder = [...]uint8{
	17, 18, 0, 1, 2, 3, 4, 5, 16, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
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

type vp8LossyConfig struct {
	qIndex          int
	quant           vp8Quant
	filter          vp8LoopFilter
	rd              vp8RDConfig
	tryY4           bool
	trySkip         bool
	updateTokenProb bool
	bufferResiduals bool
}

func vp8LossyConfigForModeQuality(mode Mode, quality int) vp8LossyConfig {
	return vp8LossyConfigForQIndex(mode, qualityToVP8QIndex(quality))
}

func vp8LossyConfigForQIndex(mode Mode, qIndex int) vp8LossyConfig {
	qIndex = clipInt(qIndex, 0, 127)
	quant := vp8QuantForIndex(qIndex)
	cfg := vp8LossyConfig{
		qIndex:          qIndex,
		quant:           quant,
		filter:          vp8LoopFilterForQuant(quant),
		rd:              newVP8RDConfig(quant),
		tryY4:           false,
		trySkip:         true,
		updateTokenProb: true,
		bufferResiduals: true,
	}
	if mode == ModeBestCompression {
		cfg.tryY4 = true
	}
	if mode == ModeFast {
		cfg.tryY4 = false
		cfg.trySkip = false
		cfg.updateTokenProb = false
		cfg.bufferResiduals = false
	}
	if mode == ModeLowMemory {
		cfg.bufferResiduals = false
	}
	return cfg
}

func encodeVP8KeyFrame(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, width int, height int, qIndex int) ([]byte, error) {
	return encodeVP8KeyFrameConfig(readLuma, readChroma, bounds, width, height, vp8LossyConfigForQIndex(ModeDefault, qIndex))
}

func encodeVP8KeyFrameConfig(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, width int, height int, cfg vp8LossyConfig) ([]byte, error) {
	mbw := (width + 15) >> 4
	mbh := (height + 15) >> 4
	work := newVP8EncodeBuffers(mbw, mbh)
	useResidualBuffer := cfg.bufferResiduals && (cfg.trySkip || cfg.updateTokenProb) && vp8ResidualBufferFits(mbw, mbh)
	var residualBuffer *vp8ResidualBuffer
	var modes []vp8MBMode
	if useResidualBuffer && !cfg.tryY4 {
		residualBuffer = newVP8ResidualBuffer(mbw * mbh)
		sink := vp8ResidualSink{buffer: residualBuffer}
		modes = analyzeVP8ModesConfigWithSink(readLuma, readChroma, bounds, mbw, mbh, cfg, work, &sink)
	} else {
		modes = analyzeVP8ModesConfig(readLuma, readChroma, bounds, mbw, mbh, cfg, work)
	}
	tokenProbs := vp8DefaultTokenProbs
	var skipMap []bool
	if useResidualBuffer {
		if residualBuffer == nil {
			clear(work.recY)
			residualBuffer = collectVP8ResidualBuffer(readLuma, readChroma, bounds, mbw, mbh, cfg.quant, modes, work)
		}
		skipMap = residualBuffer.skipMap(cfg.trySkip)
		if cfg.updateTokenProb {
			tokenStats := residualBuffer.tokenStats(skipMap)
			tokenProbs = chooseVP8TokenProbsConfig(&tokenStats, cfg.updateTokenProb)
		}
	} else {
		if cfg.trySkip {
			clear(work.recY)
			skipMap = analyzeVP8MacroblockSkips(readLuma, readChroma, bounds, mbw, mbh, cfg.quant, modes, work)
		}
		if cfg.updateTokenProb {
			clear(work.recY)
			tokenStats := collectVP8TokenStatsConfig(readLuma, readChroma, bounds, mbw, mbh, cfg.quant, modes, work, skipMap)
			tokenProbs = chooseVP8TokenProbsConfig(&tokenStats, cfg.updateTokenProb)
		}
	}
	skipProb := vp8SkipProbability(skipMap)
	firstPart, err := vp8FirstPartition(mbw, mbh, cfg.qIndex, cfg.filter, modes, tokenProbs, skipMap, skipProb)
	if err != nil {
		return nil, err
	}
	var residualPart []byte
	if residualBuffer != nil {
		residualPart = residualBuffer.encodeWithSkipMap(&tokenProbs, skipMap, vp8ResidualPartitionCapacity(width, height, cfg.qIndex))
	} else {
		clear(work.recY)
		residualPart = encodeVP8ResidualsConfig(readLuma, readChroma, bounds, width, height, mbw, mbh, cfg.quant, modes, work, &tokenProbs, skipMap)
	}
	frameLen := 10 + len(firstPart) + len(residualPart)
	frame := make([]byte, 0, frameLen)

	tag := uint32(len(firstPart))<<5 | 1<<4
	frame = append(frame, byte(tag), byte(tag>>8), byte(tag>>16))
	frame = append(frame, 0x9d, 0x01, 0x2a)
	frame = append(frame, byte(width), byte(width>>8), byte(height), byte(height>>8))
	frame = append(frame, firstPart...)
	frame = append(frame, residualPart...)
	return frame, nil
}

type vp8MBMode struct {
	useY16  bool
	yMode   uint8
	y4Modes [16]uint8
	cMode   uint8
}

type vp8LoopFilter struct {
	simple       bool
	level        int
	sharpness    int
	deltaEnabled bool
	refDeltas    [4]int
	modeDeltas   [4]int
}

type vp8EncodeBuffers struct {
	recY  []uint8
	recCb []uint8
	recCr []uint8
	top   *vp8TopBuffers
}

type vp8TopBuffers struct {
	modes  []vp8MBMode
	upPred [][4]uint8
	upY    [][4]uint8
	upUV   [][4]uint8
	upY16  []uint8
}

const (
	vp8PredDC uint8 = iota
	vp8PredTM
	vp8PredVE
	vp8PredHE
	vp8PredRD
	vp8PredVR
	vp8PredLD
	vp8PredVL
	vp8PredHD
	vp8PredHU
	vp8NumPredModes
)

func newVP8EncodeBuffers(mbw int, mbh int) *vp8EncodeBuffers {
	yStride := mbw * 16
	cStride := mbw * 8
	ySize := yStride * mbh * 16
	cSize := cStride * mbh * 8
	rec := make([]uint8, ySize+2*cSize)
	work := &vp8EncodeBuffers{
		recY:  rec[:ySize],
		recCb: rec[ySize : ySize+cSize],
		recCr: rec[ySize+cSize:],
	}
	if mbw >= vp8WorkspaceTopMinMBW {
		up := make([][4]uint8, 3*mbw)
		work.top = &vp8TopBuffers{
			modes:  make([]vp8MBMode, mbw*mbh),
			upPred: up[:mbw],
			upY:    up[mbw : 2*mbw],
			upUV:   up[2*mbw:],
			upY16:  make([]uint8, mbw),
		}
	}
	return work
}

func vp8LoopFilterForIndex(qIndex int) vp8LoopFilter {
	return vp8LoopFilterForQuant(vp8QuantForIndex(qIndex))
}

func vp8LoopFilterForQuant(quant vp8Quant) vp8LoopFilter {
	level := 4 + quant.qIndex/6
	if level > 24 {
		level = 24
	}
	if quant.qIndex <= 8 {
		level = maxInt(level-2, 0)
	}
	sharpness := quant.qIndex / 32
	if sharpness > 3 {
		sharpness = 3
	}
	return vp8LoopFilter{
		simple:       false,
		level:        level,
		sharpness:    sharpness,
		deltaEnabled: level > 0,
		modeDeltas:   [4]int{2, 0, 0, 0},
	}
}

func vp8ResidualPartitionCapacity(width int, height int, qIndex int) int {
	pixels := width * height
	divisor := 2
	switch {
	case qIndex <= 8:
		divisor = 1
	case qIndex <= 32:
		divisor = 2
	case qIndex <= 64:
		divisor = 3
	default:
		divisor = 4
	}
	capacity := pixels / divisor
	if capacity < 1024 {
		return 1024
	}
	if capacity > 1<<20 {
		return 1 << 20
	}
	return capacity
}

func vp8FirstPartitionCapacity(mbw int, mbh int) int {
	bitCount := 2 + 1 + 11 + 2 + 12 + 1 + 8*8 + 4*8*3*11*9 + 1 + mbw*mbh*(1+16*7+3)
	capacity := (bitCount+7)/8 + 4
	if capacity > vp8FirstPartitionMax {
		return vp8FirstPartitionMax
	}
	return capacity
}

func vp8FirstPartition(mbw int, mbh int, qIndex int, filter vp8LoopFilter, modes []vp8MBMode, tokenProbs vp8TokenProbs, skipMap []bool, skipProb uint8) ([]byte, error) {
	enc := newVP8BoolEncoderWithCapacity(vp8FirstPartitionCapacity(mbw, mbh))
	writeVP8Literal(enc, 0, 1)           // color space
	writeVP8Literal(enc, 0, 1)           // pixel clamp
	enc.writeBitEqualProb(false)         // no segmentation
	enc.writeBitEqualProb(filter.simple) // loop filter type
	writeVP8Literal(enc, uint32(filter.level), 6)
	writeVP8Literal(enc, uint32(filter.sharpness), 3)
	writeVP8LoopFilterDeltas(enc, filter)
	writeVP8Literal(enc, 0, 2)              // one token partition
	writeVP8Literal(enc, uint32(qIndex), 7) // base quantizer index
	for i := 0; i < 5; i++ {
		enc.writeBitEqualProb(false) // no quantizer delta update
	}
	enc.writeBitEqualProb(false) // do not refresh last frame buffer
	writeVP8TokenProbUpdates(enc, tokenProbs)
	if skipMap == nil {
		enc.writeBitEqualProb(false) // no macroblock skip probability
	} else {
		enc.writeBitEqualProb(true)
		writeVP8Literal(enc, uint32(skipProb), 8)
	}
	upPred := make([][4]uint8, mbw)
	for mby := 0; mby < mbh; mby++ {
		var leftPred [4]uint8
		for mbx := 0; mbx < mbw; mbx++ {
			if skipMap != nil {
				enc.writeBit(skipProb, skipMap[mby*mbw+mbx])
			}
			mode := modes[mby*mbw+mbx]
			enc.writeBit(145, mode.useY16)
			if mode.useY16 {
				writeVP8Y16Mode(enc, mode.yMode)
				for i := 0; i < 4; i++ {
					upPred[mbx][i] = mode.yMode
					leftPred[i] = mode.yMode
				}
			} else {
				writeVP8Y4Modes(enc, &leftPred, &upPred[mbx], mode.y4Modes)
			}
			writeVP8ChromaMode(enc, mode.cMode)
		}
	}
	data := enc.bytes()
	if len(data) > vp8FirstPartitionMax {
		return nil, fmt.Errorf("webp: lossy image is too large for the simple VP8 first partition")
	}
	firstPart := make([]byte, len(data))
	copy(firstPart, data)
	return firstPart, nil
}

func writeVP8LoopFilterDeltas(enc *vp8BoolEncoder, filter vp8LoopFilter) {
	enc.writeBitEqualProb(filter.deltaEnabled)
	if !filter.deltaEnabled {
		return
	}
	enc.writeBitEqualProb(true)
	for _, delta := range filter.refDeltas {
		writeVP8LoopFilterDelta(enc, delta)
	}
	for _, delta := range filter.modeDeltas {
		writeVP8LoopFilterDelta(enc, delta)
	}
}

func writeVP8LoopFilterDelta(enc *vp8BoolEncoder, delta int) {
	if delta == 0 {
		enc.writeBitEqualProb(false)
		return
	}
	enc.writeBitEqualProb(true)
	if delta < 0 {
		writeVP8Literal(enc, uint32(-delta), 6)
		enc.writeBitEqualProb(true)
		return
	}
	writeVP8Literal(enc, uint32(delta), 6)
	enc.writeBitEqualProb(false)
}

func writeVP8TokenProbUpdates(enc *vp8BoolEncoder, tokenProbs vp8TokenProbs) {
	for plane := range vp8TokenProbUpdateProb {
		for band := range vp8TokenProbUpdateProb[plane] {
			for context := range vp8TokenProbUpdateProb[plane][band] {
				for node, updateProb := range vp8TokenProbUpdateProb[plane][band][context] {
					prob := tokenProbs[plane][band][context][node]
					if prob == vp8DefaultTokenProbs[plane][band][context][node] {
						enc.writeBit(updateProb, false)
						continue
					}
					enc.writeBit(updateProb, true)
					writeVP8Literal(enc, uint32(prob), 8)
				}
			}
		}
	}
}

func writeVP8Y16Mode(enc *vp8BoolEncoder, mode uint8) {
	switch mode {
	case vp8PredVE:
		enc.writeBit(156, false)
		enc.writeBit(163, true)
	case vp8PredHE:
		enc.writeBit(156, true)
		enc.writeBitEqualProb(false)
	case vp8PredTM:
		enc.writeBit(156, true)
		enc.writeBitEqualProb(true)
	default:
		enc.writeBit(156, false)
		enc.writeBit(163, false)
	}
}

func writeVP8Y4Modes(enc *vp8BoolEncoder, left *[4]uint8, up *[4]uint8, modes [16]uint8) {
	for by := 0; by < 4; by++ {
		p := left[by]
		for bx := 0; bx < 4; bx++ {
			mode := modes[by*4+bx]
			writeVP8Y4Mode(enc, vp8PredProb[up[bx]][p], mode)
			p = mode
			up[bx] = mode
		}
		left[by] = p
	}
}

func writeVP8Y4Mode(enc *vp8BoolEncoder, prob [9]uint8, mode uint8) {
	switch mode {
	case vp8PredDC:
		enc.writeBit(prob[0], false)
	case vp8PredTM:
		enc.writeBit(prob[0], true)
		enc.writeBit(prob[1], false)
	case vp8PredVE:
		enc.writeBit(prob[0], true)
		enc.writeBit(prob[1], true)
		enc.writeBit(prob[2], false)
	case vp8PredHE:
		enc.writeBit(prob[0], true)
		enc.writeBit(prob[1], true)
		enc.writeBit(prob[2], true)
		enc.writeBit(prob[3], false)
		enc.writeBit(prob[4], false)
	case vp8PredRD:
		enc.writeBit(prob[0], true)
		enc.writeBit(prob[1], true)
		enc.writeBit(prob[2], true)
		enc.writeBit(prob[3], false)
		enc.writeBit(prob[4], true)
		enc.writeBit(prob[5], false)
	case vp8PredVR:
		enc.writeBit(prob[0], true)
		enc.writeBit(prob[1], true)
		enc.writeBit(prob[2], true)
		enc.writeBit(prob[3], false)
		enc.writeBit(prob[4], true)
		enc.writeBit(prob[5], true)
	case vp8PredLD:
		enc.writeBit(prob[0], true)
		enc.writeBit(prob[1], true)
		enc.writeBit(prob[2], true)
		enc.writeBit(prob[3], true)
		enc.writeBit(prob[6], false)
	case vp8PredVL:
		enc.writeBit(prob[0], true)
		enc.writeBit(prob[1], true)
		enc.writeBit(prob[2], true)
		enc.writeBit(prob[3], true)
		enc.writeBit(prob[6], true)
		enc.writeBit(prob[7], false)
	case vp8PredHD:
		enc.writeBit(prob[0], true)
		enc.writeBit(prob[1], true)
		enc.writeBit(prob[2], true)
		enc.writeBit(prob[3], true)
		enc.writeBit(prob[6], true)
		enc.writeBit(prob[7], true)
		enc.writeBit(prob[8], false)
	default:
		enc.writeBit(prob[0], true)
		enc.writeBit(prob[1], true)
		enc.writeBit(prob[2], true)
		enc.writeBit(prob[3], true)
		enc.writeBit(prob[6], true)
		enc.writeBit(prob[7], true)
		enc.writeBit(prob[8], true)
	}
}

func writeVP8ChromaMode(enc *vp8BoolEncoder, mode uint8) {
	switch mode {
	case vp8PredVE:
		enc.writeBit(142, true)
		enc.writeBit(114, false)
	case vp8PredHE:
		enc.writeBit(142, true)
		enc.writeBit(114, true)
		enc.writeBit(183, false)
	case vp8PredTM:
		enc.writeBit(142, true)
		enc.writeBit(114, true)
		enc.writeBit(183, true)
	default:
		enc.writeBit(142, false)
	}
}

var vp8PredProb = [vp8NumPredModes][vp8NumPredModes][9]uint8{
	{
		{231, 120, 48, 89, 115, 113, 120, 152, 112},
		{152, 179, 64, 126, 170, 118, 46, 70, 95},
		{175, 69, 143, 80, 85, 82, 72, 155, 103},
		{56, 58, 10, 171, 218, 189, 17, 13, 152},
		{114, 26, 17, 163, 44, 195, 21, 10, 173},
		{121, 24, 80, 195, 26, 62, 44, 64, 85},
		{144, 71, 10, 38, 171, 213, 144, 34, 26},
		{170, 46, 55, 19, 136, 160, 33, 206, 71},
		{63, 20, 8, 114, 114, 208, 12, 9, 226},
		{81, 40, 11, 96, 182, 84, 29, 16, 36},
	},
	{
		{134, 183, 89, 137, 98, 101, 106, 165, 148},
		{72, 187, 100, 130, 157, 111, 32, 75, 80},
		{66, 102, 167, 99, 74, 62, 40, 234, 128},
		{41, 53, 9, 178, 241, 141, 26, 8, 107},
		{74, 43, 26, 146, 73, 166, 49, 23, 157},
		{65, 38, 105, 160, 51, 52, 31, 115, 128},
		{104, 79, 12, 27, 217, 255, 87, 17, 7},
		{87, 68, 71, 44, 114, 51, 15, 186, 23},
		{47, 41, 14, 110, 182, 183, 21, 17, 194},
		{66, 45, 25, 102, 197, 189, 23, 18, 22},
	},
	{
		{88, 88, 147, 150, 42, 46, 45, 196, 205},
		{43, 97, 183, 117, 85, 38, 35, 179, 61},
		{39, 53, 200, 87, 26, 21, 43, 232, 171},
		{56, 34, 51, 104, 114, 102, 29, 93, 77},
		{39, 28, 85, 171, 58, 165, 90, 98, 64},
		{34, 22, 116, 206, 23, 34, 43, 166, 73},
		{107, 54, 32, 26, 51, 1, 81, 43, 31},
		{68, 25, 106, 22, 64, 171, 36, 225, 114},
		{34, 19, 21, 102, 132, 188, 16, 76, 124},
		{62, 18, 78, 95, 85, 57, 50, 48, 51},
	},
	{
		{193, 101, 35, 159, 215, 111, 89, 46, 111},
		{60, 148, 31, 172, 219, 228, 21, 18, 111},
		{112, 113, 77, 85, 179, 255, 38, 120, 114},
		{40, 42, 1, 196, 245, 209, 10, 25, 109},
		{88, 43, 29, 140, 166, 213, 37, 43, 154},
		{61, 63, 30, 155, 67, 45, 68, 1, 209},
		{100, 80, 8, 43, 154, 1, 51, 26, 71},
		{142, 78, 78, 16, 255, 128, 34, 197, 171},
		{41, 40, 5, 102, 211, 183, 4, 1, 221},
		{51, 50, 17, 168, 209, 192, 23, 25, 82},
	},
	{
		{138, 31, 36, 171, 27, 166, 38, 44, 229},
		{67, 87, 58, 169, 82, 115, 26, 59, 179},
		{63, 59, 90, 180, 59, 166, 93, 73, 154},
		{40, 40, 21, 116, 143, 209, 34, 39, 175},
		{47, 15, 16, 183, 34, 223, 49, 45, 183},
		{46, 17, 33, 183, 6, 98, 15, 32, 183},
		{57, 46, 22, 24, 128, 1, 54, 17, 37},
		{65, 32, 73, 115, 28, 128, 23, 128, 205},
		{40, 3, 9, 115, 51, 192, 18, 6, 223},
		{87, 37, 9, 115, 59, 77, 64, 21, 47},
	},
	{
		{104, 55, 44, 218, 9, 54, 53, 130, 226},
		{64, 90, 70, 205, 40, 41, 23, 26, 57},
		{54, 57, 112, 184, 5, 41, 38, 166, 213},
		{30, 34, 26, 133, 152, 116, 10, 32, 134},
		{39, 19, 53, 221, 26, 114, 32, 73, 255},
		{31, 9, 65, 234, 2, 15, 1, 118, 73},
		{75, 32, 12, 51, 192, 255, 160, 43, 51},
		{88, 31, 35, 67, 102, 85, 55, 186, 85},
		{56, 21, 23, 111, 59, 205, 45, 37, 192},
		{55, 38, 70, 124, 73, 102, 1, 34, 98},
	},
	{
		{125, 98, 42, 88, 104, 85, 117, 175, 82},
		{95, 84, 53, 89, 128, 100, 113, 101, 45},
		{75, 79, 123, 47, 51, 128, 81, 171, 1},
		{57, 17, 5, 71, 102, 57, 53, 41, 49},
		{38, 33, 13, 121, 57, 73, 26, 1, 85},
		{41, 10, 67, 138, 77, 110, 90, 47, 114},
		{115, 21, 2, 10, 102, 255, 166, 23, 6},
		{101, 29, 16, 10, 85, 128, 101, 196, 26},
		{57, 18, 10, 102, 102, 213, 34, 20, 43},
		{117, 20, 15, 36, 163, 128, 68, 1, 26},
	},
	{
		{102, 61, 71, 37, 34, 53, 31, 243, 192},
		{69, 60, 71, 38, 73, 119, 28, 222, 37},
		{68, 45, 128, 34, 1, 47, 11, 245, 171},
		{62, 17, 19, 70, 146, 85, 55, 62, 70},
		{37, 43, 37, 154, 100, 163, 85, 160, 1},
		{63, 9, 92, 136, 28, 64, 32, 201, 85},
		{75, 15, 9, 9, 64, 255, 184, 119, 16},
		{86, 6, 28, 5, 64, 255, 25, 248, 1},
		{56, 8, 17, 132, 137, 255, 55, 116, 128},
		{58, 15, 20, 82, 135, 57, 26, 121, 40},
	},
	{
		{164, 50, 31, 137, 154, 133, 25, 35, 218},
		{51, 103, 44, 131, 131, 123, 31, 6, 158},
		{86, 40, 64, 135, 148, 224, 45, 183, 128},
		{22, 26, 17, 131, 240, 154, 14, 1, 209},
		{45, 16, 21, 91, 64, 222, 7, 1, 197},
		{56, 21, 39, 155, 60, 138, 23, 102, 213},
		{83, 12, 13, 54, 192, 255, 68, 47, 28},
		{85, 26, 85, 85, 128, 128, 32, 146, 171},
		{18, 11, 7, 63, 144, 171, 4, 4, 246},
		{35, 27, 10, 146, 174, 171, 12, 26, 128},
	},
	{
		{190, 80, 35, 99, 180, 80, 126, 54, 45},
		{85, 126, 47, 87, 176, 51, 41, 20, 32},
		{101, 75, 128, 139, 118, 146, 116, 128, 85},
		{56, 41, 15, 176, 236, 85, 37, 9, 62},
		{71, 30, 17, 119, 118, 255, 17, 18, 138},
		{101, 38, 60, 138, 55, 70, 43, 26, 142},
		{146, 36, 19, 30, 171, 255, 97, 27, 20},
		{138, 45, 61, 62, 219, 1, 81, 188, 64},
		{32, 41, 20, 117, 151, 142, 20, 21, 163},
		{112, 19, 12, 61, 195, 128, 48, 4, 24},
	},
}

func writeVP8Literal(enc *vp8BoolEncoder, value uint32, n uint8) {
	for n > 0 {
		n--
		enc.writeBitEqualProb(value&(1<<n) != 0)
	}
}

type vp8Quant struct {
	qIndex int
	y1DC   int
	y1AC   int
	y2DC   int
	y2AC   int
	uvDC   int
	uvAC   int
}

var vp8DCQuantTable = [...]int{
	4, 5, 6, 7, 8, 9, 10, 10,
	11, 12, 13, 14, 15, 16, 17, 17,
	18, 19, 20, 20, 21, 21, 22, 22,
	23, 23, 24, 25, 25, 26, 27, 28,
	29, 30, 31, 32, 33, 34, 35, 36,
	37, 37, 38, 39, 40, 41, 42, 43,
	44, 45, 46, 46, 47, 48, 49, 50,
	51, 52, 53, 54, 55, 56, 57, 58,
	59, 60, 61, 62, 63, 64, 65, 66,
	67, 68, 69, 70, 71, 72, 73, 74,
	75, 76, 76, 77, 78, 79, 80, 81,
	82, 83, 84, 85, 86, 87, 88, 89,
	91, 93, 95, 96, 98, 100, 101, 102,
	104, 106, 108, 110, 112, 114, 116, 118,
	122, 124, 126, 128, 130, 132, 134, 136,
	138, 140, 143, 145, 148, 151, 154, 157,
}

var vp8ACQuantTable = [...]int{
	4, 5, 6, 7, 8, 9, 10, 11,
	12, 13, 14, 15, 16, 17, 18, 19,
	20, 21, 22, 23, 24, 25, 26, 27,
	28, 29, 30, 31, 32, 33, 34, 35,
	36, 37, 38, 39, 40, 41, 42, 43,
	44, 45, 46, 47, 48, 49, 50, 51,
	52, 53, 54, 55, 56, 57, 58, 60,
	62, 64, 66, 68, 70, 72, 74, 76,
	78, 80, 82, 84, 86, 88, 90, 92,
	94, 96, 98, 100, 102, 104, 106, 108,
	110, 112, 114, 116, 119, 122, 125, 128,
	131, 134, 137, 140, 143, 146, 149, 152,
	155, 158, 161, 164, 167, 170, 173, 177,
	181, 185, 189, 193, 197, 201, 205, 209,
	213, 217, 221, 225, 229, 234, 239, 245,
	249, 254, 259, 264, 269, 274, 279, 284,
}

func vp8QuantForIndex(qIndex int) vp8Quant {
	if qIndex < 0 {
		qIndex = 0
	}
	if qIndex > 127 {
		qIndex = 127
	}
	uvIndex := clipInt(qIndex-4, 0, 117)
	if qIndex >= 80 {
		uvIndex = clipInt(qIndex-8, 0, 117)
	}
	y2ACScale := 145
	switch {
	case qIndex <= 16:
		y2ACScale = 135
	case qIndex >= 96:
		y2ACScale = 160
	}
	return vp8Quant{
		qIndex: qIndex,
		y1DC:   vp8DCQuantTable[qIndex],
		y1AC:   vp8ACQuantTable[qIndex],
		y2DC:   maxInt(vp8DCQuantTable[qIndex]*2, 8),
		y2AC:   maxInt(vp8ACQuantTable[qIndex]*y2ACScale/100, 8),
		uvDC:   vp8DCQuantTable[uvIndex],
		uvAC:   vp8ACQuantTable[uvIndex],
	}
}

func qualityToVP8QIndex(quality int) int {
	quality = clipInt(quality, 1, 100)
	if quality >= 100 {
		return 0
	}
	quality = maxInt(quality-25, 1)
	inv := 100 - quality
	linear := (inv*127 + 99/2) / 99
	curved := (inv*inv*127 + 99*99/2) / (99 * 99)
	q := (linear + curved + 1) / 2
	return clipInt(q, 0, 127)
}

type vp8RDConfig struct {
	yLambda  int64
	uvLambda int64
}

func newVP8RDConfig(quant vp8Quant) vp8RDConfig {
	return vp8RDConfig{
		yLambda:  vp8RDLambda(quant.y1AC),
		uvLambda: vp8RDLambda(quant.uvAC),
	}
}

func vp8RDLambda(q int) int64 {
	q = maxInt(q, 1)
	return int64(maxInt(q*q/8, 1))
}

func (rd vp8RDConfig) lumaScore(distortion int64, bitCost int64) int64 {
	return distortion + (bitCost*rd.yLambda+128)/256
}

func (rd vp8RDConfig) chromaScore(distortion int64, bitCost int64) int64 {
	return distortion + (bitCost*rd.uvLambda+128)/256
}

func analyzeVP8Modes(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, quant vp8Quant, work *vp8EncodeBuffers) []vp8MBMode {
	return analyzeVP8ModesConfig(readLuma, readChroma, bounds, mbw, mbh, vp8LossyConfigForQIndex(ModeDefault, quant.qIndex), work)
}

func analyzeVP8ModesConfig(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, cfg vp8LossyConfig, work *vp8EncodeBuffers) []vp8MBMode {
	return analyzeVP8ModesConfigWithSink(readLuma, readChroma, bounds, mbw, mbh, cfg, work, nil)
}

func analyzeVP8ModesConfigWithSink(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, cfg vp8LossyConfig, work *vp8EncodeBuffers, sink *vp8ResidualSink) []vp8MBMode {
	yStride := mbw * 16
	cStride := mbw * 8
	recY := work.recY
	recCb := work.recCb
	recCr := work.recCr
	var modes []vp8MBMode
	var upPred [][4]uint8
	var upY [][4]uint8
	var upUV [][4]uint8
	var upY16 []uint8
	if work.top == nil {
		modes = make([]vp8MBMode, mbw*mbh)
		upPred = make([][4]uint8, mbw)
		upY = make([][4]uint8, mbw)
		upUV = make([][4]uint8, mbw)
		upY16 = make([]uint8, mbw)
	} else {
		modes = work.top.modes
		upPred = work.top.upPred
		upY = work.top.upY
		upUV = work.top.upUV
		upY16 = work.top.upY16
		clear(upPred)
		clear(upY)
		clear(upUV)
		clear(upY16)
	}
	for mby := 0; mby < mbh; mby++ {
		var leftPred [4]uint8
		var leftY [4]uint8
		var leftUV [4]uint8
		var leftY16 uint8
		for mbx := 0; mbx < mbw; mbx++ {
			lumaTarget := makeLumaTargetMB(readLuma, bounds, mbx, mby)
			lumaBlocks := &lumaTarget.blocks
			chromaTarget := makeChromaTargetMB(readChroma, bounds, mbx, mby)
			mode := vp8MBMode{
				cMode: chooseVP8ChromaModeFromTarget(&chromaTarget, mbx, mby, recCb, recCr, cStride, cfg.quant, cfg.rd, &leftUV, &upUV[mbx]),
			}
			savedLeftPred := leftPred
			savedUpPred := upPred[mbx]
			savedLeftY := leftY
			savedUpY := upY[mbx]
			savedLeftY16 := leftY16
			savedUpY16 := upY16[mbx]

			y16Mode, y16Score := chooseVP8Y16Mode(lumaBlocks, mbx, mby, recY, yStride, cfg.quant, cfg.rd, &leftY, &upY[mbx], &leftY16, &upY16[mbx])
			if cfg.tryY4 {
				y4Score := chooseVP8Y4Modes(lumaBlocks, mbx, mby, recY, yStride, cfg.quant, cfg.rd, &leftPred, &upPred[mbx], &leftY, &upY[mbx], &mode)
				if y16Score > y4Score {
					processVP8ChromaTargetMB(&chromaTarget, mbx, mby, recCb, recCr, cStride, cfg.quant, mode, &leftUV, &upUV[mbx], nil)
					modes[mby*mbw+mbx] = mode
					continue
				}
				leftPred = savedLeftPred
				upPred[mbx] = savedUpPred
				leftY = savedLeftY
				upY[mbx] = savedUpY
				leftY16 = savedLeftY16
				upY16[mbx] = savedUpY16
			}
			mode.useY16 = true
			mode.yMode = y16Mode
			for i := 0; i < 4; i++ {
				leftPred[i] = y16Mode
				upPred[mbx][i] = y16Mode
			}
			lumaNZ := processVP8Luma16MB(readLuma, bounds, mbx, mby, recY, yStride, cfg.quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], sink)
			chromaNZ := processVP8ChromaTargetMB(&chromaTarget, mbx, mby, recCb, recCr, cStride, cfg.quant, mode, &leftUV, &upUV[mbx], sink)
			sink.finishMacroblock(lumaNZ || chromaNZ)
			modes[mby*mbw+mbx] = mode
		}
	}
	return modes
}

func collectVP8TokenStats(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, quant vp8Quant, modes []vp8MBMode, work *vp8EncodeBuffers) vp8TokenStats {
	return collectVP8TokenStatsConfig(readLuma, readChroma, bounds, mbw, mbh, quant, modes, work, nil)
}

func collectVP8TokenStatsConfig(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, quant vp8Quant, modes []vp8MBMode, work *vp8EncodeBuffers, skipMap []bool) vp8TokenStats {
	yStride := mbw * 16
	cStride := mbw * 8
	var stats vp8TokenStats
	var upY [][4]uint8
	var upUV [][4]uint8
	var upY16 []uint8
	if work.top == nil {
		upY = make([][4]uint8, mbw)
		upUV = make([][4]uint8, mbw)
		upY16 = make([]uint8, mbw)
	} else {
		upY = work.top.upY
		upUV = work.top.upUV
		upY16 = work.top.upY16
		clear(upY)
		clear(upUV)
		clear(upY16)
	}
	sink := vp8ResidualSink{stats: &stats}

	for mby := 0; mby < mbh; mby++ {
		var leftY [4]uint8
		var leftUV [4]uint8
		var leftY16 uint8
		for mbx := 0; mbx < mbw; mbx++ {
			mode := modes[mby*mbw+mbx]
			if skipMap != nil && skipMap[mby*mbw+mbx] {
				processVP8LumaMB(readLuma, bounds, mbx, mby, work.recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], nil)
				processVP8ChromaMB(readChroma, bounds, mbx, mby, work.recCb, work.recCr, cStride, quant, mode, &leftUV, &upUV[mbx], nil)
				continue
			}
			processVP8LumaMB(readLuma, bounds, mbx, mby, work.recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], &sink)
			processVP8ChromaMB(readChroma, bounds, mbx, mby, work.recCb, work.recCr, cStride, quant, mode, &leftUV, &upUV[mbx], &sink)
		}
	}
	return stats
}

func analyzeVP8MacroblockSkips(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, quant vp8Quant, modes []vp8MBMode, work *vp8EncodeBuffers) []bool {
	yStride := mbw * 16
	cStride := mbw * 8
	var upY [][4]uint8
	var upUV [][4]uint8
	var upY16 []uint8
	if work.top == nil {
		upY = make([][4]uint8, mbw)
		upUV = make([][4]uint8, mbw)
		upY16 = make([]uint8, mbw)
	} else {
		upY = work.top.upY
		upUV = work.top.upUV
		upY16 = work.top.upY16
		clear(upY)
		clear(upUV)
		clear(upY16)
	}

	skipMap := make([]bool, mbw*mbh)
	skipCount := 0
	for mby := 0; mby < mbh; mby++ {
		var leftY [4]uint8
		var leftUV [4]uint8
		var leftY16 uint8
		for mbx := 0; mbx < mbw; mbx++ {
			mode := modes[mby*mbw+mbx]
			lumaNZ := processVP8LumaMB(readLuma, bounds, mbx, mby, work.recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], nil)
			chromaNZ := processVP8ChromaMB(readChroma, bounds, mbx, mby, work.recCb, work.recCr, cStride, quant, mode, &leftUV, &upUV[mbx], nil)
			if !lumaNZ && !chromaNZ {
				skipMap[mby*mbw+mbx] = true
				skipCount++
			}
		}
	}
	if !vp8ShouldUseMacroblockSkip(len(skipMap), skipCount) {
		return nil
	}
	return skipMap
}

func vp8ShouldUseMacroblockSkip(total int, skipped int) bool {
	return skipped > 0 && skipped*8 > total+9
}

func vp8SkipProbability(skipMap []bool) uint8 {
	if skipMap == nil {
		return 0
	}
	notSkipped := 0
	for _, skipped := range skipMap {
		if !skipped {
			notSkipped++
		}
	}
	prob := (notSkipped*255 + len(skipMap)/2) / len(skipMap)
	return uint8(clipInt(prob, 1, 255))
}

func encodeVP8Residuals(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, width int, height int, mbw int, mbh int, quant vp8Quant, modes []vp8MBMode, work *vp8EncodeBuffers, tokenProbs *vp8TokenProbs) []byte {
	return encodeVP8ResidualsConfig(readLuma, readChroma, bounds, width, height, mbw, mbh, quant, modes, work, tokenProbs, nil)
}

func encodeVP8ResidualsConfig(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, width int, height int, mbw int, mbh int, quant vp8Quant, modes []vp8MBMode, work *vp8EncodeBuffers, tokenProbs *vp8TokenProbs, skipMap []bool) []byte {
	yStride := mbw * 16
	cStride := mbw * 8
	recY := work.recY
	recCb := work.recCb
	recCr := work.recCr

	enc := newVP8BoolEncoderWithCapacity(vp8ResidualPartitionCapacity(width, height, quant.qIndex))
	sink := vp8ResidualSink{encoder: enc, probs: tokenProbs}
	var upY [][4]uint8
	var upUV [][4]uint8
	var upY16 []uint8
	if work.top == nil {
		upY = make([][4]uint8, mbw)
		upUV = make([][4]uint8, mbw)
		upY16 = make([]uint8, mbw)
	} else {
		upY = work.top.upY
		upUV = work.top.upUV
		upY16 = work.top.upY16
		clear(upY)
		clear(upUV)
		clear(upY16)
	}

	for mby := 0; mby < mbh; mby++ {
		var leftY [4]uint8
		var leftUV [4]uint8
		var leftY16 uint8
		for mbx := 0; mbx < mbw; mbx++ {
			mode := modes[mby*mbw+mbx]
			if skipMap != nil && skipMap[mby*mbw+mbx] {
				processVP8LumaMB(readLuma, bounds, mbx, mby, recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], nil)
				processVP8ChromaMB(readChroma, bounds, mbx, mby, recCb, recCr, cStride, quant, mode, &leftUV, &upUV[mbx], nil)
				continue
			}
			processVP8LumaMB(readLuma, bounds, mbx, mby, recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], &sink)
			processVP8ChromaMB(readChroma, bounds, mbx, mby, recCb, recCr, cStride, quant, mode, &leftUV, &upUV[mbx], &sink)
		}
	}
	return enc.bytes()
}

func reconstructVP8LumaMB(readLuma lumaReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, mode vp8MBMode) {
	if mode.useY16 {
		processVP8Luma16MB(readLuma, bounds, mbx, mby, recY, stride, quant, mode, nil, nil, nil, nil, nil)
		return
	}
	processVP8Luma4MB(readLuma, bounds, mbx, mby, recY, stride, quant, nil, nil, mode, nil)
}

func processVP8LumaMB(readLuma lumaReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, mode vp8MBMode, left *[4]uint8, up *[4]uint8, leftY16 *uint8, upY16 *uint8, sink *vp8ResidualSink) bool {
	if mode.useY16 {
		return processVP8Luma16MB(readLuma, bounds, mbx, mby, recY, stride, quant, mode, left, up, leftY16, upY16, sink)
	}
	nz := processVP8Luma4MB(readLuma, bounds, mbx, mby, recY, stride, quant, left, up, mode, sink)
	*leftY16 = 0
	*upY16 = 0
	return nz
}

func processVP8Luma4MB(readLuma lumaReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, left *[4]uint8, up *[4]uint8, mode vp8MBMode, sink *vp8ResidualSink) bool {
	var localLeft [4]uint8
	var localUp [4]uint8
	if left == nil {
		left = &localLeft
	}
	if up == nil {
		up = &localUp
	}
	hasNZ := false
	for by := 0; by < 4; by++ {
		nz := left[by]
		for bx := 0; bx < 4; bx++ {
			x := mbx*16 + bx*4
			y := mby*16 + by*4
			pred := predictLuma4(recY, stride, x, y, mode.y4Modes[by*4+bx])
			residual := lumaResidualBlock(readLuma, bounds, x, y, pred)
			coeff := quantizeVP8Block(residual, quant.y1DC, quant.y1AC)
			context := nz + up[bx]
			blockNZ := sink.writeBlock(vp8PlaneY1SansY2, context, coeff, 0)
			hasNZ = hasNZ || blockNZ != 0
			recon := reconstructVP8Block(pred, coeff, quant.y1DC, quant.y1AC)
			put4(recY, stride, x, y, recon)
			nz = blockNZ
			up[bx] = blockNZ
		}
		left[by] = nz
	}
	return hasNZ
}

func processVP8Luma16MB(readLuma lumaReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, mode vp8MBMode, left *[4]uint8, up *[4]uint8, leftY16 *uint8, upY16 *uint8, sink *vp8ResidualSink) bool {
	var localLeft [4]uint8
	var localUp [4]uint8
	var localLeftY16 uint8
	var localUpY16 uint8
	if left == nil {
		left = &localLeft
	}
	if up == nil {
		up = &localUp
	}
	if leftY16 == nil {
		leftY16 = &localLeftY16
	}
	if upY16 == nil {
		upY16 = &localUpY16
	}
	pred16 := predictLuma16(recY, stride, mbx, mby, mode.yMode)
	var transformed [16][16]int
	var y2Input [16]int
	for by := 0; by < 4; by++ {
		for bx := 0; bx < 4; bx++ {
			x := mbx*16 + bx*4
			y := mby*16 + by*4
			index := by*4 + bx
			pred := pred16[index]
			residual := lumaResidualBlock(readLuma, bounds, x, y, pred)
			block := forwardDCT4(residual)
			transformed[index] = block
			y2Input[index] = block[0]
		}
	}

	y2Coeff := quantizeTransformedVP8Block(forwardWHT4(y2Input), quant.y2DC, quant.y2AC)
	y2Context := *leftY16 + *upY16
	y16NZ := sink.writeBlock(vp8PlaneY2, y2Context, y2Coeff, 0)
	hasNZ := y16NZ != 0
	*leftY16 = y16NZ
	*upY16 = y16NZ
	y2Recon := inverseWHT4(dequantizeVP8Block(y2Coeff, quant.y2DC, quant.y2AC))

	for by := 0; by < 4; by++ {
		nz := left[by]
		for bx := 0; bx < 4; bx++ {
			index := by*4 + bx
			coeff := quantizeTransformedVP8BlockACOnly(transformed[index], quant.y1AC)
			context := nz + up[bx]
			blockNZ := sink.writeBlock(vp8PlaneY1WithY2, context, coeff, 1)
			hasNZ = hasNZ || blockNZ != 0
			reconCoeff := dequantizeVP8Block(coeff, 0, quant.y1AC)
			reconCoeff[0] = y2Recon[index]
			recon := inverseDCT4(pred16[index], reconCoeff)
			put4(recY, stride, mbx*16+bx*4, mby*16+by*4, recon)
			nz = blockNZ
			up[bx] = blockNZ
		}
		left[by] = nz
	}
	return hasNZ
}

func reconstructVP8ChromaMB(readChroma chromaReader, bounds image.Rectangle, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, mode vp8MBMode) {
	target := makeChromaTargetMB(readChroma, bounds, mbx, mby)
	processVP8ChromaPlane(target.cb[:], mbx, mby, recCb, stride, quant, nil, nil, mode.cMode, true, nil)
	processVP8ChromaPlane(target.cr[:], mbx, mby, recCr, stride, quant, nil, nil, mode.cMode, false, nil)
}

func processVP8ChromaMB(readChroma chromaReader, bounds image.Rectangle, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, mode vp8MBMode, left *[4]uint8, up *[4]uint8, sink *vp8ResidualSink) bool {
	target := makeChromaTargetMB(readChroma, bounds, mbx, mby)
	return processVP8ChromaTargetMB(&target, mbx, mby, recCb, recCr, stride, quant, mode, left, up, sink)
}

func processVP8ChromaTargetMB(target *chromaTargetMB, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, mode vp8MBMode, left *[4]uint8, up *[4]uint8, sink *vp8ResidualSink) bool {
	cbNZ := processVP8ChromaPlane(target.cb[:], mbx, mby, recCb, stride, quant, left, up, mode.cMode, true, sink)
	crNZ := processVP8ChromaPlane(target.cr[:], mbx, mby, recCr, stride, quant, left, up, mode.cMode, false, sink)
	return cbNZ || crNZ
}

func processVP8ChromaPlane(target []uint8, mbx int, mby int, rec []uint8, stride int, quant vp8Quant, left *[4]uint8, up *[4]uint8, mode uint8, cb bool, sink *vp8ResidualSink) bool {
	var localLeft [4]uint8
	var localUp [4]uint8
	if left == nil {
		left = &localLeft
	}
	if up == nil {
		up = &localUp
	}
	hasNZ := false
	base := 0
	if !cb {
		base = 2
	}
	pred8 := predictChroma8(rec, stride, mbx, mby, mode)
	for by := 0; by < 2; by++ {
		nz := left[base+by]
		for bx := 0; bx < 2; bx++ {
			x := mbx*8 + bx*4
			y := mby*8 + by*4
			pred := pred8[by*2+bx]
			residual := chromaResidualBlockFromTarget(target, bx, by, pred)
			coeff := quantizeVP8Block(residual, quant.uvDC, quant.uvAC)
			context := nz + up[base+bx]
			blockNZ := sink.writeBlock(vp8PlaneUV, context, coeff, 0)
			hasNZ = hasNZ || blockNZ != 0
			recon := reconstructVP8Block(pred, coeff, quant.uvDC, quant.uvAC)
			put4(rec, stride, x, y, recon)
			nz = blockNZ
			up[base+bx] = blockNZ
		}
		left[base+by] = nz
	}
	return hasNZ
}

func chooseVP8Y4Modes(target *lumaTargetBlocks, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, leftPred *[4]uint8, upPred *[4]uint8, leftNZ *[4]uint8, upNZ *[4]uint8, mode *vp8MBMode) int64 {
	score := rd.lumaScore(0, vp8BitCost(145, false))
	for by := 0; by < 4; by++ {
		p := leftPred[by]
		nz := leftNZ[by]
		for bx := 0; bx < 4; bx++ {
			x := mbx*16 + bx*4
			y := mby*16 + by*4
			luma := &(*target)[by*4+bx]
			blockMode, blockScore, blockNZ, recon := chooseVP8Y4Mode(luma, x, y, recY, stride, quant, rd, upPred[bx], p, nz+upNZ[bx])
			mode.y4Modes[by*4+bx] = blockMode
			put4(recY, stride, x, y, recon)
			score += blockScore
			p = blockMode
			nz = blockNZ
			upPred[bx] = blockMode
			upNZ[bx] = blockNZ
		}
		leftPred[by] = p
		leftNZ[by] = nz
	}
	return score
}

func chooseVP8Y4Mode(target *[16]uint8, x int, y int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, topPred uint8, leftPred uint8, context uint8) (uint8, int64, uint8, [16]uint8) {
	bestMode := uint8(vp8PredDC)
	bestScore := int64(1<<63 - 1)
	bestNZ := uint8(0)
	var bestRecon [16]uint8
	neighbors := makeLuma4Neighbors(recY, stride, x, y)
	for mode := uint8(0); mode < vp8NumPredModes; mode++ {
		pred := predictLuma4WithNeighbors(&neighbors, mode)
		residual := lumaResidualBlockFromTarget(target, pred)
		coeff := quantizeVP8Block(residual, quant.y1DC, quant.y1AC)
		recon := reconstructVP8Block(pred, coeff, quant.y1DC, quant.y1AC)
		distortion := scoreLuma4FromTarget(target, recon)
		blockBitCost, blockNZ := vp8BlockBitCostAndNonZeroPtr(vp8PlaneY1SansY2, context, &coeff)
		bitCost := vp8Y4ModeCost(topPred, leftPred, mode) + blockBitCost
		score := rd.lumaScore(distortion, bitCost)
		if score < bestScore {
			bestScore = score
			bestMode = mode
			bestRecon = recon
			if blockNZ {
				bestNZ = 1
			} else {
				bestNZ = 0
			}
		}
	}
	return bestMode, bestScore, bestNZ, bestRecon
}

var vp8Y4ModeCostTable = makeVP8Y4ModeCostTable()

func makeVP8Y4ModeCostTable() [vp8NumPredModes][vp8NumPredModes][vp8NumPredModes]int64 {
	var costs [vp8NumPredModes][vp8NumPredModes][vp8NumPredModes]int64
	for topPred := uint8(0); topPred < vp8NumPredModes; topPred++ {
		for leftPred := uint8(0); leftPred < vp8NumPredModes; leftPred++ {
			prob := vp8PredProb[topPred][leftPred]
			for mode := uint8(0); mode < vp8NumPredModes; mode++ {
				costs[topPred][leftPred][mode] = vp8Y4ModeCostFromProb(prob, mode)
			}
		}
	}
	return costs
}

func vp8Y4ModeCost(topPred uint8, leftPred uint8, mode uint8) int64 {
	return vp8Y4ModeCostTable[topPred][leftPred][mode]
}

func vp8Y4ModeCostFromProb(prob [9]uint8, mode uint8) int64 {
	switch mode {
	case vp8PredDC:
		return vp8BitCost(prob[0], false)
	case vp8PredTM:
		return vp8BitCost(prob[0], true) + vp8BitCost(prob[1], false)
	case vp8PredVE:
		return vp8BitCost(prob[0], true) + vp8BitCost(prob[1], true) + vp8BitCost(prob[2], false)
	case vp8PredHE:
		return vp8BitCost(prob[0], true) + vp8BitCost(prob[1], true) + vp8BitCost(prob[2], true) +
			vp8BitCost(prob[3], false) + vp8BitCost(prob[4], false)
	case vp8PredRD:
		return vp8BitCost(prob[0], true) + vp8BitCost(prob[1], true) + vp8BitCost(prob[2], true) +
			vp8BitCost(prob[3], false) + vp8BitCost(prob[4], true) + vp8BitCost(prob[5], false)
	case vp8PredVR:
		return vp8BitCost(prob[0], true) + vp8BitCost(prob[1], true) + vp8BitCost(prob[2], true) +
			vp8BitCost(prob[3], false) + vp8BitCost(prob[4], true) + vp8BitCost(prob[5], true)
	case vp8PredLD:
		return vp8BitCost(prob[0], true) + vp8BitCost(prob[1], true) + vp8BitCost(prob[2], true) +
			vp8BitCost(prob[3], true) + vp8BitCost(prob[6], false)
	case vp8PredVL:
		return vp8BitCost(prob[0], true) + vp8BitCost(prob[1], true) + vp8BitCost(prob[2], true) +
			vp8BitCost(prob[3], true) + vp8BitCost(prob[6], true) + vp8BitCost(prob[7], false)
	case vp8PredHD:
		return vp8BitCost(prob[0], true) + vp8BitCost(prob[1], true) + vp8BitCost(prob[2], true) +
			vp8BitCost(prob[3], true) + vp8BitCost(prob[6], true) + vp8BitCost(prob[7], true) +
			vp8BitCost(prob[8], false)
	default:
		return vp8BitCost(prob[0], true) + vp8BitCost(prob[1], true) + vp8BitCost(prob[2], true) +
			vp8BitCost(prob[3], true) + vp8BitCost(prob[6], true) + vp8BitCost(prob[7], true) +
			vp8BitCost(prob[8], true)
	}
}

var vp8BitCostTable = makeVP8BitCostTable()

func makeVP8BitCostTable() [256][2]int64 {
	var costs [256][2]int64
	for prob := 0; prob < 256; prob++ {
		costs[prob][0] = vp8ProbabilityCost(prob)
		costs[prob][1] = vp8ProbabilityCost(256 - prob)
	}
	return costs
}

func vp8ProbabilityCost(prob int) int64 {
	if prob <= 0 {
		return 1 << 30
	}
	return int64(math.Log2(256/float64(prob)) * 256)
}

func vp8BitCost(prob uint8, bit bool) int64 {
	if bit {
		return vp8BitCostTable[prob][1]
	}
	return vp8BitCostTable[prob][0]
}

func chooseVP8Y16Mode(target *lumaTargetBlocks, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, left *[4]uint8, up *[4]uint8, leftY16 *uint8, upY16 *uint8) (uint8, int64) {
	bestMode := vp8PredDC
	bestScore := int64(1<<63 - 1)
	modes, nModes := vp8CandidatePredModes(mbx, mby)
	for i := 0; i < nModes; i++ {
		mode := modes[i]
		score := scoreLuma16RD(target, mbx, mby, recY, stride, quant, rd, left, up, *leftY16+*upY16, mode)
		if score < bestScore {
			bestScore = score
			bestMode = mode
		}
	}
	return bestMode, bestScore
}

func scoreLuma16RD(target *lumaTargetBlocks, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, left *[4]uint8, up *[4]uint8, y16Context uint8, mode uint8) int64 {
	pred16 := predictLuma16(recY, stride, mbx, mby, mode)
	var transformed [16][16]int
	var y2Input [16]int
	for by := 0; by < 4; by++ {
		for bx := 0; bx < 4; bx++ {
			index := by*4 + bx
			pred := pred16[index]
			residual := lumaResidualBlockFromTarget(&(*target)[index], pred)
			block := forwardDCT4(residual)
			transformed[index] = block
			y2Input[index] = block[0]
		}
	}

	y2Coeff := quantizeTransformedVP8Block(forwardWHT4(y2Input), quant.y2DC, quant.y2AC)
	y2Recon := inverseWHT4(dequantizeVP8Block(y2Coeff, quant.y2DC, quant.y2AC))
	bitCost := vp8BitCost(145, true) + vp8Y16ModeCost(mode) + vp8BlockBitCost(vp8PlaneY2, y16Context, y2Coeff)
	var distortion int64
	localLeft := *left
	localUp := *up
	for by := 0; by < 4; by++ {
		nz := localLeft[by]
		for bx := 0; bx < 4; bx++ {
			index := by*4 + bx
			coeff := quantizeTransformedVP8BlockACOnly(transformed[index], quant.y1AC)
			blockBitCost, blockNZ := vp8BlockBitCostFromAndNonZeroPtr(vp8PlaneY1WithY2, nz+localUp[bx], &coeff, 1)
			bitCost += blockBitCost
			reconCoeff := dequantizeVP8Block(coeff, 0, quant.y1AC)
			reconCoeff[0] = y2Recon[index]
			recon := inverseDCT4(pred16[index], reconCoeff)
			distortion += scoreLuma4FromTarget(&(*target)[index], recon)
			if blockNZ {
				nz = 1
				localUp[bx] = 1
			} else {
				nz = 0
				localUp[bx] = 0
			}
		}
		localLeft[by] = nz
	}
	return rd.lumaScore(distortion, bitCost)
}

func chooseVP8ChromaMode(readChroma chromaReader, bounds image.Rectangle, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, rd vp8RDConfig, left *[4]uint8, up *[4]uint8) uint8 {
	target := makeChromaTargetMB(readChroma, bounds, mbx, mby)
	return chooseVP8ChromaModeFromTarget(&target, mbx, mby, recCb, recCr, stride, quant, rd, left, up)
}

func chooseVP8ChromaModeFromTarget(target *chromaTargetMB, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, rd vp8RDConfig, left *[4]uint8, up *[4]uint8) uint8 {
	bestMode := vp8PredDC
	bestScore := int64(1<<63 - 1)
	modes, nModes := vp8CandidatePredModes(mbx, mby)
	for i := 0; i < nModes; i++ {
		mode := modes[i]
		score := scoreChromaRD(target, mbx, mby, recCb, recCr, stride, quant, rd, left, up, mode)
		if score < bestScore {
			bestScore = score
			bestMode = mode
		}
	}
	return bestMode
}

func scoreChromaRD(target *chromaTargetMB, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, rd vp8RDConfig, left *[4]uint8, up *[4]uint8, mode uint8) int64 {
	localLeft := *left
	localUp := *up
	bitCost := vp8ChromaModeCost(mode)
	distortion, cbBits := scoreChromaPlaneRD(target.cb[:], mbx, mby, recCb, stride, quant, &localLeft, &localUp, mode, true)
	crDistortion, crBits := scoreChromaPlaneRD(target.cr[:], mbx, mby, recCr, stride, quant, &localLeft, &localUp, mode, false)
	return rd.chromaScore(distortion+crDistortion, bitCost+cbBits+crBits)
}

func scoreChromaPlaneRD(target []uint8, mbx int, mby int, rec []uint8, stride int, quant vp8Quant, left *[4]uint8, up *[4]uint8, mode uint8, cb bool) (int64, int64) {
	base := 0
	if !cb {
		base = 2
	}
	pred8 := predictChroma8(rec, stride, mbx, mby, mode)
	var distortion int64
	var bitCost int64
	for by := 0; by < 2; by++ {
		nz := left[base+by]
		for bx := 0; bx < 2; bx++ {
			pred := pred8[by*2+bx]
			residual := chromaResidualBlockFromTarget(target, bx, by, pred)
			coeff := quantizeVP8Block(residual, quant.uvDC, quant.uvAC)
			blockBitCost, blockNZ := vp8BlockBitCostAndNonZeroPtr(vp8PlaneUV, nz+up[base+bx], &coeff)
			bitCost += blockBitCost
			recon := reconstructVP8Block(pred, coeff, quant.uvDC, quant.uvAC)
			distortion += scoreChroma4FromTarget(target, bx, by, recon)
			if blockNZ {
				nz = 1
				up[base+bx] = 1
			} else {
				nz = 0
				up[base+bx] = 0
			}
		}
		left[base+by] = nz
	}
	return distortion, bitCost
}

func scoreChroma4FromTarget(target []uint8, bx int, by int, block [16]uint8) int64 {
	var score int64
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			got := target[(by*4+yy)*8+bx*4+xx]
			score += squareInt(int(got) - int(block[yy*4+xx]))
		}
	}
	return score
}

var vp8Y16ModeCostTable = makeVP8Y16ModeCostTable()

func makeVP8Y16ModeCostTable() [vp8NumPredModes]int64 {
	var costs [vp8NumPredModes]int64
	for mode := uint8(0); mode < vp8NumPredModes; mode++ {
		costs[mode] = vp8Y16ModeCostFromMode(mode)
	}
	return costs
}

func vp8Y16ModeCost(mode uint8) int64 {
	return vp8Y16ModeCostTable[mode]
}

func vp8Y16ModeCostFromMode(mode uint8) int64 {
	switch mode {
	case vp8PredVE:
		return vp8BitCost(156, false) + vp8BitCost(163, true)
	case vp8PredHE:
		return vp8BitCost(156, true) + vp8BitCost(128, false)
	case vp8PredTM:
		return vp8BitCost(156, true) + vp8BitCost(128, true)
	default:
		return vp8BitCost(156, false) + vp8BitCost(163, false)
	}
}

var vp8ChromaModeCostTable = makeVP8ChromaModeCostTable()

func makeVP8ChromaModeCostTable() [vp8NumPredModes]int64 {
	var costs [vp8NumPredModes]int64
	for mode := uint8(0); mode < vp8NumPredModes; mode++ {
		costs[mode] = vp8ChromaModeCostFromMode(mode)
	}
	return costs
}

func vp8ChromaModeCost(mode uint8) int64 {
	return vp8ChromaModeCostTable[mode]
}

func vp8ChromaModeCostFromMode(mode uint8) int64 {
	switch mode {
	case vp8PredVE:
		return vp8BitCost(142, true) + vp8BitCost(114, false)
	case vp8PredHE:
		return vp8BitCost(142, true) + vp8BitCost(114, true) + vp8BitCost(183, false)
	case vp8PredTM:
		return vp8BitCost(142, true) + vp8BitCost(114, true) + vp8BitCost(183, true)
	default:
		return vp8BitCost(142, false)
	}
}

func vp8CandidatePredModes(mbx int, mby int) ([4]uint8, int) {
	modes := [4]uint8{vp8PredDC}
	n := 1
	if mby > 0 {
		modes[n] = vp8PredVE
		n++
	}
	if mbx > 0 {
		modes[n] = vp8PredHE
		n++
	}
	if mbx > 0 && mby > 0 {
		modes[n] = vp8PredTM
		n++
	}
	return modes, n
}

type luma16PredBlocks [16][16]uint8

func predictLuma16(rec []uint8, stride int, mbx int, mby int, mode uint8) luma16PredBlocks {
	x0 := mbx * 16
	y0 := mby * 16
	var pred luma16PredBlocks
	switch mode {
	case vp8PredVE:
		top := rec[(y0-1)*stride+x0:]
		for by := 0; by < 4; by++ {
			for bx := 0; bx < 4; bx++ {
				block := &pred[by*4+bx]
				for y := 0; y < 4; y++ {
					copy(block[y*4:y*4+4], top[bx*4:bx*4+4])
				}
			}
		}
	case vp8PredHE:
		for by := 0; by < 4; by++ {
			for bx := 0; bx < 4; bx++ {
				block := &pred[by*4+bx]
				for y := 0; y < 4; y++ {
					v := rec[(y0+by*4+y)*stride+x0-1]
					for x := 0; x < 4; x++ {
						block[y*4+x] = v
					}
				}
			}
		}
	case vp8PredTM:
		topLeft := int(rec[(y0-1)*stride+x0-1])
		for by := 0; by < 4; by++ {
			for bx := 0; bx < 4; bx++ {
				block := &pred[by*4+bx]
				for y := 0; y < 4; y++ {
					left := int(rec[(y0+by*4+y)*stride+x0-1])
					for x := 0; x < 4; x++ {
						top := int(rec[(y0-1)*stride+x0+bx*4+x])
						block[y*4+x] = clipUint8(left + top - topLeft)
					}
				}
			}
		}
	default:
		block := filledBlock4(dcPred16(rec, stride, mbx, mby))
		for i := range pred {
			pred[i] = block
		}
	}
	return pred
}

type chroma8PredBlocks [4][16]uint8

func predictChroma8(rec []uint8, stride int, mbx int, mby int, mode uint8) chroma8PredBlocks {
	x0 := mbx * 8
	y0 := mby * 8
	var pred chroma8PredBlocks
	switch mode {
	case vp8PredVE:
		top := rec[(y0-1)*stride+x0:]
		for by := 0; by < 2; by++ {
			for bx := 0; bx < 2; bx++ {
				block := &pred[by*2+bx]
				for y := 0; y < 4; y++ {
					copy(block[y*4:y*4+4], top[bx*4:bx*4+4])
				}
			}
		}
	case vp8PredHE:
		for by := 0; by < 2; by++ {
			for bx := 0; bx < 2; bx++ {
				block := &pred[by*2+bx]
				for y := 0; y < 4; y++ {
					v := rec[(y0+by*4+y)*stride+x0-1]
					for x := 0; x < 4; x++ {
						block[y*4+x] = v
					}
				}
			}
		}
	case vp8PredTM:
		topLeft := int(rec[(y0-1)*stride+x0-1])
		for by := 0; by < 2; by++ {
			for bx := 0; bx < 2; bx++ {
				block := &pred[by*2+bx]
				for y := 0; y < 4; y++ {
					left := int(rec[(y0+by*4+y)*stride+x0-1])
					for x := 0; x < 4; x++ {
						top := int(rec[(y0-1)*stride+x0+bx*4+x])
						block[y*4+x] = clipUint8(left + top - topLeft)
					}
				}
			}
		}
	default:
		block := filledBlock4(dcPred8(rec, stride, mbx, mby))
		for i := range pred {
			pred[i] = block
		}
	}
	return pred
}

func dcPred16(rec []uint8, stride int, mbx int, mby int) uint8 {
	x0 := mbx * 16
	y0 := mby * 16
	switch {
	case mbx == 0 && mby == 0:
		return 0x80
	case mbx == 0:
		sum := 8
		for x := 0; x < 16; x++ {
			sum += int(rec[(y0-1)*stride+x0+x])
		}
		return uint8(sum / 16)
	case mby == 0:
		sum := 8
		for y := 0; y < 16; y++ {
			sum += int(rec[(y0+y)*stride+x0-1])
		}
		return uint8(sum / 16)
	default:
		sum := 16
		for x := 0; x < 16; x++ {
			sum += int(rec[(y0-1)*stride+x0+x])
		}
		for y := 0; y < 16; y++ {
			sum += int(rec[(y0+y)*stride+x0-1])
		}
		return uint8(sum / 32)
	}
}

func dcPred8(rec []uint8, stride int, mbx int, mby int) uint8 {
	x0 := mbx * 8
	y0 := mby * 8
	switch {
	case mbx == 0 && mby == 0:
		return 0x80
	case mbx == 0:
		sum := 4
		for x := 0; x < 8; x++ {
			sum += int(rec[(y0-1)*stride+x0+x])
		}
		return uint8(sum / 8)
	case mby == 0:
		sum := 4
		for y := 0; y < 8; y++ {
			sum += int(rec[(y0+y)*stride+x0-1])
		}
		return uint8(sum / 8)
	default:
		sum := 8
		for x := 0; x < 8; x++ {
			sum += int(rec[(y0-1)*stride+x0+x])
		}
		for y := 0; y < 8; y++ {
			sum += int(rec[(y0+y)*stride+x0-1])
		}
		return uint8(sum / 16)
	}
}

func squareInt(v int) int64 {
	return int64(v * v)
}

func lumaResidualBlock(readLuma lumaReader, bounds image.Rectangle, x int, y int, pred [16]uint8) [16]int {
	if lumaResidualBlockInBounds(bounds, x, y) {
		return lumaResidualBlockFast(readLuma, bounds.Min.X+x, bounds.Min.Y+y, pred)
	}
	var residual [16]int
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			luma := sampleLuma(readLuma, bounds, x+xx, y+yy)
			residual[yy*4+xx] = int(luma) - int(pred[yy*4+xx])
		}
	}
	return residual
}

func lumaResidualBlockInBounds(bounds image.Rectangle, x int, y int) bool {
	return x >= 0 && y >= 0 && x+4 <= bounds.Dx() && y+4 <= bounds.Dy()
}

func lumaResidualBlockFast(readLuma lumaReader, x int, y int, pred [16]uint8) [16]int {
	var residual [16]int
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			luma := readLuma(x+xx, y+yy)
			residual[yy*4+xx] = int(luma) - int(pred[yy*4+xx])
		}
	}
	return residual
}

type lumaTargetMB struct {
	blocks lumaTargetBlocks
}

type lumaTargetBlocks [16][16]uint8

func makeLumaTargetMB(readLuma lumaReader, bounds image.Rectangle, mbx int, mby int) lumaTargetMB {
	var target lumaTargetMB
	baseX := mbx * 16
	baseY := mby * 16
	if lumaTargetMBInBounds(bounds, baseX, baseY) {
		absX := bounds.Min.X + baseX
		absY := bounds.Min.Y + baseY
		for by := 0; by < 4; by++ {
			for bx := 0; bx < 4; bx++ {
				block := &target.blocks[by*4+bx]
				for y := 0; y < 4; y++ {
					for x := 0; x < 4; x++ {
						block[y*4+x] = readLuma(absX+bx*4+x, absY+by*4+y)
					}
				}
			}
		}
		return target
	}
	for by := 0; by < 4; by++ {
		for bx := 0; bx < 4; bx++ {
			block := &target.blocks[by*4+bx]
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					block[y*4+x] = sampleLuma(readLuma, bounds, baseX+bx*4+x, baseY+by*4+y)
				}
			}
		}
	}
	return target
}

func lumaTargetMBInBounds(bounds image.Rectangle, baseX int, baseY int) bool {
	return baseX >= 0 && baseY >= 0 && baseX+16 <= bounds.Dx() && baseY+16 <= bounds.Dy()
}

func makeLumaTargetBlocks(target *lumaTargetMB) lumaTargetBlocks {
	return target.blocks
}

func lumaResidualBlockFromTarget(target *[16]uint8, pred [16]uint8) [16]int {
	var residual [16]int
	for i := range residual {
		residual[i] = int(target[i]) - int(pred[i])
	}
	return residual
}

func scoreLuma4FromTarget(target *[16]uint8, block [16]uint8) int64 {
	var score int64
	for i := range block {
		score += squareInt(int(target[i]) - int(block[i]))
	}
	return score
}

func chromaResidualBlock(readChroma chromaReader, bounds image.Rectangle, x int, y int, pred [16]uint8, cb bool) [16]int {
	var residual [16]int
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			target := chromaSample(readChroma, bounds, x+xx*2, y+yy*2, cb)
			residual[yy*4+xx] = int(target) - int(pred[yy*4+xx])
		}
	}
	return residual
}

type chromaTargetMB struct {
	cb [64]uint8
	cr [64]uint8
}

type chromaPairCacheMB struct {
	cb [18 * 18]uint8
	cr [18 * 18]uint8
}

func makeChromaTargetMB(readChroma chromaReader, bounds image.Rectangle, mbx int, mby int) chromaTargetMB {
	var target chromaTargetMB
	baseX := mbx * 16
	baseY := mby * 16
	if chromaTargetMBInBounds(bounds, baseX, baseY) && chromaTargetCacheUseful(bounds) {
		absX := bounds.Min.X + baseX
		absY := bounds.Min.Y + baseY
		cache := makeChromaPairCacheMB(readChroma, absX, absY)
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				cb, cr := chromaSamplePairFromCache(&cache, x*2, y*2)
				i := y*8 + x
				target.cb[i] = cb
				target.cr[i] = cr
			}
		}
		return target
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			cb, cr := chromaSamplePair(readChroma, bounds, baseX+x*2, baseY+y*2)
			i := y*8 + x
			target.cb[i] = cb
			target.cr[i] = cr
		}
	}
	return target
}

func chromaTargetMBInBounds(bounds image.Rectangle, baseX int, baseY int) bool {
	return baseX > 0 && baseY > 0 && baseX+16 < bounds.Dx() && baseY+16 < bounds.Dy()
}

func chromaTargetCacheUseful(bounds image.Rectangle) bool {
	return bounds.Dx() >= vp8ChromaCacheMinDim && bounds.Dy() >= vp8ChromaCacheMinDim
}

func makeChromaPairCacheMB(readChroma chromaReader, absX int, absY int) chromaPairCacheMB {
	var cache chromaPairCacheMB
	for y := 0; y < 18; y++ {
		for x := 0; x < 18; x++ {
			cb, cr := readChroma(absX+x-1, absY+y-1)
			i := y*18 + x
			cache.cb[i] = cb
			cache.cr[i] = cr
		}
	}
	return cache
}

func chromaSamplePairFromCache(cache *chromaPairCacheMB, x int, y int) (uint8, uint8) {
	centerCb, centerCr, cbMin, cbMax, crMin, crMax := chromaCenterStatsFromCache(cache, x, y)
	cbSimple := cbMax-cbMin <= 16
	crSimple := crMax-crMin <= 16
	cb := uint8((centerCb + 2) / 4)
	cr := uint8((centerCr + 2) / 4)
	if cbSimple && crSimple {
		return cb, cr
	}

	filterCb, filterCr := chromaFilterSumsFromCache(cache, x, y)
	if !cbSimple {
		cb = uint8((filterCb + 18) / 36)
	}
	if !crSimple {
		cr = uint8((filterCr + 18) / 36)
	}
	return cb, cr
}

func chromaCenterStatsFromCache(cache *chromaPairCacheMB, x int, y int) (int, int, int, int, int, int) {
	cbSum, crSum := 0, 0
	cbMin, crMin := 256, 256
	cbMax, crMax := -1, -1
	for yy := 0; yy < 2; yy++ {
		for xx := 0; xx < 2; xx++ {
			i := (y+yy+1)*18 + x + xx + 1
			cb := cache.cb[i]
			cr := cache.cr[i]
			cbSum += int(cb)
			crSum += int(cr)
			cbMin = minInt(cbMin, int(cb))
			cbMax = maxInt(cbMax, int(cb))
			crMin = minInt(crMin, int(cr))
			crMax = maxInt(crMax, int(cr))
		}
	}
	return cbSum, crSum, cbMin, cbMax, crMin, crMax
}

func chromaFilterSumsFromCache(cache *chromaPairCacheMB, x int, y int) (int, int) {
	filterCb, filterCr := 0, 0
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			i := (y+yy)*18 + x + xx
			weight := chromaSampleFilterWeights[yy*4+xx]
			filterCb += weight * int(cache.cb[i])
			filterCr += weight * int(cache.cr[i])
		}
	}
	return filterCb, filterCr
}

func chromaResidualBlockFromTarget(target []uint8, bx int, by int, pred [16]uint8) [16]int {
	var residual [16]int
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			targetValue := target[(by*4+yy)*8+bx*4+xx]
			residual[yy*4+xx] = int(targetValue) - int(pred[yy*4+xx])
		}
	}
	return residual
}

var chromaSampleFilterWeights = [16]int{
	1, 2, 2, 1,
	2, 4, 4, 2,
	2, 4, 4, 2,
	1, 2, 2, 1,
}

func chromaSample(readChroma chromaReader, bounds image.Rectangle, x int, y int, cb bool) uint8 {
	return chromaSampleFiltered(readChroma, bounds, x, y, cb)
}

func chromaSamplePair(readChroma chromaReader, bounds image.Rectangle, x int, y int) (uint8, uint8) {
	if chromaSampleWindowInBounds(bounds, x, y) {
		return chromaSamplePairInBounds(readChroma, bounds.Min.X+x, bounds.Min.Y+y)
	}
	return chromaSamplePairClamped(readChroma, bounds, x, y)
}

func chromaSamplePairInBounds(readChroma chromaReader, x int, y int) (uint8, uint8) {
	centerCb, centerCr, cbMin, cbMax, crMin, crMax := chromaCenterStatsInBounds(readChroma, x, y)
	cbSimple := cbMax-cbMin <= 16
	crSimple := crMax-crMin <= 16
	cb := uint8((centerCb + 2) / 4)
	cr := uint8((centerCr + 2) / 4)
	if cbSimple && crSimple {
		return cb, cr
	}

	filterCb, filterCr := chromaFilterSumsInBounds(readChroma, x, y)
	if !cbSimple {
		cb = uint8((filterCb + 18) / 36)
	}
	if !crSimple {
		cr = uint8((filterCr + 18) / 36)
	}
	return cb, cr
}

func chromaSamplePairClamped(readChroma chromaReader, bounds image.Rectangle, x int, y int) (uint8, uint8) {
	centerCb, centerCr, cbMin, cbMax, crMin, crMax := chromaCenterStatsClamped(readChroma, bounds, x, y)
	cbSimple := cbMax-cbMin <= 16
	crSimple := crMax-crMin <= 16
	cb := uint8((centerCb + 2) / 4)
	cr := uint8((centerCr + 2) / 4)
	if cbSimple && crSimple {
		return cb, cr
	}

	filterCb, filterCr := chromaFilterSumsClamped(readChroma, bounds, x, y)
	if !cbSimple {
		cb = uint8((filterCb + 18) / 36)
	}
	if !crSimple {
		cr = uint8((filterCr + 18) / 36)
	}
	return cb, cr
}

func chromaSampleFiltered(readChroma chromaReader, bounds image.Rectangle, x int, y int, cb bool) uint8 {
	if chromaSampleWindowInBounds(bounds, x, y) {
		return chromaSampleFilteredInBounds(readChroma, bounds.Min.X+x, bounds.Min.Y+y, cb)
	}
	return chromaSampleFilteredClamped(readChroma, bounds, x, y, cb)
}

func chromaSampleWindowInBounds(bounds image.Rectangle, x int, y int) bool {
	return x > 0 && y > 0 && x+2 < bounds.Dx() && y+2 < bounds.Dy()
}

func chromaSampleFilteredInBounds(readChroma chromaReader, x int, y int, cb bool) uint8 {
	centerCb, centerCr, cbMin, cbMax, crMin, crMax := chromaCenterStatsInBounds(readChroma, x, y)
	if cb {
		if cbMax-cbMin <= 16 {
			return uint8((centerCb + 2) / 4)
		}
		filterCb, _ := chromaFilterSumsInBounds(readChroma, x, y)
		return uint8((filterCb + 18) / 36)
	}
	if crMax-crMin <= 16 {
		return uint8((centerCr + 2) / 4)
	}
	_, filterCr := chromaFilterSumsInBounds(readChroma, x, y)
	return uint8((filterCr + 18) / 36)
}

func chromaSampleFilteredClamped(readChroma chromaReader, bounds image.Rectangle, x int, y int, cb bool) uint8 {
	centerCb, centerCr, cbMin, cbMax, crMin, crMax := chromaCenterStatsClamped(readChroma, bounds, x, y)
	if cb {
		if cbMax-cbMin <= 16 {
			return uint8((centerCb + 2) / 4)
		}
		filterCb, _ := chromaFilterSumsClamped(readChroma, bounds, x, y)
		return uint8((filterCb + 18) / 36)
	}
	if crMax-crMin <= 16 {
		return uint8((centerCr + 2) / 4)
	}
	_, filterCr := chromaFilterSumsClamped(readChroma, bounds, x, y)
	return uint8((filterCr + 18) / 36)
}

func chromaCenterStatsInBounds(readChroma chromaReader, x int, y int) (int, int, int, int, int, int) {
	cbSum, crSum := 0, 0
	cbMin, crMin := 256, 256
	cbMax, crMax := -1, -1
	for yy := 0; yy < 2; yy++ {
		for xx := 0; xx < 2; xx++ {
			cb, cr := readChroma(x+xx, y+yy)
			cbSum += int(cb)
			crSum += int(cr)
			cbMin = minInt(cbMin, int(cb))
			cbMax = maxInt(cbMax, int(cb))
			crMin = minInt(crMin, int(cr))
			crMax = maxInt(crMax, int(cr))
		}
	}
	return cbSum, crSum, cbMin, cbMax, crMin, crMax
}

func chromaCenterStatsClamped(readChroma chromaReader, bounds image.Rectangle, x int, y int) (int, int, int, int, int, int) {
	cbSum, crSum := 0, 0
	cbMin, crMin := 256, 256
	cbMax, crMax := -1, -1
	for yy := 0; yy < 2; yy++ {
		for xx := 0; xx < 2; xx++ {
			cb, cr := chromaPairAt(readChroma, bounds, x+xx, y+yy)
			cbSum += int(cb)
			crSum += int(cr)
			cbMin = minInt(cbMin, int(cb))
			cbMax = maxInt(cbMax, int(cb))
			crMin = minInt(crMin, int(cr))
			crMax = maxInt(crMax, int(cr))
		}
	}
	return cbSum, crSum, cbMin, cbMax, crMin, crMax
}

func chromaFilterSumsInBounds(readChroma chromaReader, x int, y int) (int, int) {
	filterCb, filterCr := 0, 0
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			cb, cr := readChroma(x+xx-1, y+yy-1)
			weight := chromaSampleFilterWeights[yy*4+xx]
			filterCb += weight * int(cb)
			filterCr += weight * int(cr)
		}
	}
	return filterCb, filterCr
}

func chromaFilterSumsClamped(readChroma chromaReader, bounds image.Rectangle, x int, y int) (int, int) {
	filterCb, filterCr := 0, 0
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			cb, cr := chromaPairAt(readChroma, bounds, x+xx-1, y+yy-1)
			weight := chromaSampleFilterWeights[yy*4+xx]
			filterCb += weight * int(cb)
			filterCr += weight * int(cr)
		}
	}
	return filterCb, filterCr
}

func chromaValueAt(readChroma chromaReader, bounds image.Rectangle, x int, y int, cb bool) int {
	u, v := sampleChroma(readChroma, bounds, x, y)
	if cb {
		return int(u)
	}
	return int(v)
}

func chromaValue(readChroma chromaReader, x int, y int, cb bool) int {
	u, v := readChroma(x, y)
	if cb {
		return int(u)
	}
	return int(v)
}

func chromaValueForPixel(c color.NRGBA, cb bool) int {
	u, v := rgbToChroma(c.R, c.G, c.B)
	if cb {
		return int(u)
	}
	return int(v)
}

func chromaPairAt(readChroma chromaReader, bounds image.Rectangle, x int, y int) (uint8, uint8) {
	return sampleChroma(readChroma, bounds, x, y)
}

func sampleChroma(readChroma chromaReader, bounds image.Rectangle, x int, y int) (uint8, uint8) {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x >= bounds.Dx() {
		x = bounds.Dx() - 1
	}
	if y >= bounds.Dy() {
		y = bounds.Dy() - 1
	}
	return readChroma(bounds.Min.X+x, bounds.Min.Y+y)
}

func sampleLuma(readLuma lumaReader, bounds image.Rectangle, x int, y int) uint8 {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x >= bounds.Dx() {
		x = bounds.Dx() - 1
	}
	if y >= bounds.Dy() {
		y = bounds.Dy() - 1
	}
	return readLuma(bounds.Min.X+x, bounds.Min.Y+y)
}

func samplePixel(readPixel pixelReader, bounds image.Rectangle, x int, y int) color.NRGBA {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x >= bounds.Dx() {
		x = bounds.Dx() - 1
	}
	if y >= bounds.Dy() {
		y = bounds.Dy() - 1
	}
	return readPixel(bounds.Min.X+x, bounds.Min.Y+y)
}

func rgbToLuma(r uint8, g uint8, b uint8) uint8 {
	r1 := int32(r)
	g1 := int32(g)
	b1 := int32(b)
	return uint8((19595*r1 + 38470*g1 + 7471*b1 + 1<<15) >> 16)
}

func rgbToChroma(r uint8, g uint8, b uint8) (uint8, uint8) {
	r1 := int32(r)
	g1 := int32(g)
	b1 := int32(b)

	cb := -11056*r1 - 21712*g1 + 32768*b1 + 257<<15
	if uint32(cb)&0xff000000 == 0 {
		cb >>= 16
	} else {
		cb = ^(cb >> 31)
	}

	cr := 32768*r1 - 27440*g1 - 5328*b1 + 257<<15
	if uint32(cr)&0xff000000 == 0 {
		cr >>= 16
	} else {
		cr = ^(cr >> 31)
	}

	return uint8(cb), uint8(cr)
}

type luma4Neighbors struct {
	topLeft int
	top     [8]int
	left    [4]int
}

func makeLuma4Neighbors(rec []uint8, stride int, x int, y int) luma4Neighbors {
	var neighbors luma4Neighbors
	neighbors.topLeft = luma4TopLeft(rec, stride, x, y)
	for i := range neighbors.top {
		neighbors.top[i] = luma4Top(rec, stride, x, y, i)
	}
	for i := range neighbors.left {
		neighbors.left[i] = luma4Left(rec, stride, x, y, i)
	}
	return neighbors
}

func predictLuma4(rec []uint8, stride int, x int, y int, mode uint8) [16]uint8 {
	neighbors := makeLuma4Neighbors(rec, stride, x, y)
	return predictLuma4WithNeighbors(&neighbors, mode)
}

func predictLuma4WithNeighbors(neighbors *luma4Neighbors, mode uint8) [16]uint8 {
	a := neighbors.topLeft
	b := neighbors.top[0]
	c := neighbors.top[1]
	d := neighbors.top[2]
	e := neighbors.top[3]
	f := neighbors.top[4]
	g := neighbors.top[5]
	h := neighbors.top[6]
	i := neighbors.top[7]
	p := neighbors.left[0]
	q := neighbors.left[1]
	r := neighbors.left[2]
	s := neighbors.left[3]

	var block [16]uint8
	switch mode {
	case vp8PredTM:
		for yy := 0; yy < 4; yy++ {
			left := neighbors.left[yy]
			for xx := 0; xx < 4; xx++ {
				block[yy*4+xx] = clipUint8(left + neighbors.top[xx] - a)
			}
		}
	case vp8PredVE:
		abc := avg3(a, b, c)
		bcd := avg3(b, c, d)
		cde := avg3(c, d, e)
		def := avg3(d, e, f)
		for yy := 0; yy < 4; yy++ {
			block[yy*4+0] = abc
			block[yy*4+1] = bcd
			block[yy*4+2] = cde
			block[yy*4+3] = def
		}
	case vp8PredHE:
		ssr := avg3(s, s, r)
		srq := avg3(s, r, q)
		rqp := avg3(r, q, p)
		apq := avg3(a, p, q)
		for xx := 0; xx < 4; xx++ {
			block[0*4+xx] = apq
			block[1*4+xx] = rqp
			block[2*4+xx] = srq
			block[3*4+xx] = ssr
		}
	case vp8PredRD:
		srq := avg3(s, r, q)
		rqp := avg3(r, q, p)
		qpa := avg3(q, p, a)
		pab := avg3(p, a, b)
		abc := avg3(a, b, c)
		bcd := avg3(b, c, d)
		cde := avg3(c, d, e)
		block = [16]uint8{
			pab, abc, bcd, cde,
			qpa, pab, abc, bcd,
			rqp, qpa, pab, abc,
			srq, rqp, qpa, pab,
		}
	case vp8PredVR:
		ab := avg2(a, b)
		bc := avg2(b, c)
		cd := avg2(c, d)
		de := avg2(d, e)
		rqp := avg3(r, q, p)
		qpa := avg3(q, p, a)
		pab := avg3(p, a, b)
		abc := avg3(a, b, c)
		bcd := avg3(b, c, d)
		cde := avg3(c, d, e)
		block = [16]uint8{
			ab, bc, cd, de,
			pab, abc, bcd, cde,
			qpa, ab, bc, cd,
			rqp, pab, abc, bcd,
		}
	case vp8PredLD:
		abc := avg3(b, c, d)
		bcd := avg3(c, d, e)
		cde := avg3(d, e, f)
		def := avg3(e, f, g)
		efg := avg3(f, g, h)
		fgh := avg3(g, h, i)
		ghh := avg3(h, i, i)
		block = [16]uint8{
			abc, bcd, cde, def,
			bcd, cde, def, efg,
			cde, def, efg, fgh,
			def, efg, fgh, ghh,
		}
	case vp8PredVL:
		ab := avg2(b, c)
		bc := avg2(c, d)
		cd := avg2(d, e)
		de := avg2(e, f)
		abc := avg3(b, c, d)
		bcd := avg3(c, d, e)
		cde := avg3(d, e, f)
		def := avg3(e, f, g)
		efg := avg3(f, g, h)
		fgh := avg3(g, h, i)
		block = [16]uint8{
			ab, bc, cd, de,
			abc, bcd, cde, def,
			bc, cd, de, efg,
			bcd, cde, def, fgh,
		}
	case vp8PredHD:
		sr := avg2(s, r)
		rq := avg2(r, q)
		qp := avg2(q, p)
		pa := avg2(p, a)
		srq := avg3(s, r, q)
		rqp := avg3(r, q, p)
		qpa := avg3(q, p, a)
		pab := avg3(p, a, b)
		abc := avg3(a, b, c)
		bcd := avg3(b, c, d)
		block = [16]uint8{
			pa, pab, abc, bcd,
			qp, qpa, pa, pab,
			rq, rqp, qp, qpa,
			sr, srq, rq, rqp,
		}
	case vp8PredHU:
		pq := avg2(p, q)
		qr := avg2(q, r)
		rs := avg2(r, s)
		pqr := avg3(p, q, r)
		qrs := avg3(q, r, s)
		rss := avg3(r, s, s)
		sss := uint8(s)
		block = [16]uint8{
			pq, pqr, qr, qrs,
			qr, qrs, rs, rss,
			rs, rss, sss, sss,
			sss, sss, sss, sss,
		}
	default:
		block = pred4DCBlockFromNeighbors(neighbors)
	}
	return block
}

func luma4Top(rec []uint8, stride int, x int, y int, dx int) int {
	if y == 0 {
		return 0x7f
	}
	xx := x + dx
	if xx >= stride {
		xx = stride - 1
	}
	return int(rec[(y-1)*stride+xx])
}

func luma4Left(rec []uint8, stride int, x int, y int, dy int) int {
	if x == 0 {
		return 0x81
	}
	return int(rec[(y+dy)*stride+x-1])
}

func luma4TopLeft(rec []uint8, stride int, x int, y int) int {
	if y == 0 {
		return 0x7f
	}
	if x == 0 {
		return 0x81
	}
	return int(rec[(y-1)*stride+x-1])
}

func avg2(a int, b int) uint8 {
	return uint8((a + b + 1) / 2)
}

func avg3(a int, b int, c int) uint8 {
	return uint8((a + 2*b + c + 2) / 4)
}

func pred4DCBlock(rec []uint8, stride int, x int, y int) [16]uint8 {
	neighbors := makeLuma4Neighbors(rec, stride, x, y)
	return pred4DCBlockFromNeighbors(&neighbors)
}

func pred4DCBlockFromNeighbors(neighbors *luma4Neighbors) [16]uint8 {
	sum := 4
	for i := 0; i < 4; i++ {
		sum += neighbors.top[i]
	}
	for j := 0; j < 4; j++ {
		sum += neighbors.left[j]
	}
	return filledBlock4(uint8(sum / 8))
}

func filledBlock4(v uint8) [16]uint8 {
	var block [16]uint8
	for i := range block {
		block[i] = v
	}
	return block
}

func pred8DC(rec []uint8, stride int, mbx int, mby int, x int, y int) [16]uint8 {
	leftX := mbx * 8
	topY := mby * 8
	switch {
	case mbx == 0 && mby == 0:
		return filledBlock4(0x80)
	case mbx == 0:
		sum := 4
		for i := 0; i < 8; i++ {
			sum += int(rec[(topY-1)*stride+leftX+i])
		}
		return filledBlock4(uint8(sum / 8))
	case mby == 0:
		sum := 4
		for j := 0; j < 8; j++ {
			sum += int(rec[(topY+j)*stride+leftX-1])
		}
		return filledBlock4(uint8(sum / 8))
	default:
		sum := 8
		for i := 0; i < 8; i++ {
			sum += int(rec[(topY-1)*stride+leftX+i])
		}
		for j := 0; j < 8; j++ {
			sum += int(rec[(topY+j)*stride+leftX-1])
		}
		return filledBlock4(uint8(sum / 16))
	}
}

func put4(dst []uint8, stride int, x int, y int, block [16]uint8) {
	for yy := 0; yy < 4; yy++ {
		row := dst[(y+yy)*stride+x:]
		copy(row[:4], block[yy*4:yy*4+4])
	}
}

func quantizeVP8Block(residual [16]int, dcQ int, acQ int) vp8QuantizedBlock {
	transformed := forwardDCT4(residual)
	return quantizeTransformedVP8Block(transformed, dcQ, acQ)
}

func quantizeTransformedVP8Block(transformed [16]int, dcQ int, acQ int) vp8QuantizedBlock {
	var coeff vp8QuantizedBlock
	coeff[0] = quantizeTransformCoeff(transformed[0], dcQ)
	for i := 1; i < 16; i++ {
		coeff[i] = quantizeTransformCoeff(transformed[i], acQ)
	}
	return coeff
}

func quantizeTransformedVP8BlockACOnly(transformed [16]int, acQ int) vp8QuantizedBlock {
	var coeff vp8QuantizedBlock
	for i := 1; i < 16; i++ {
		coeff[i] = quantizeTransformCoeff(transformed[i], acQ)
	}
	return coeff
}

func forwardDCT4(residual [16]int) [16]int {
	var tmp [16]int
	for i := 0; i < 4; i++ {
		d0 := residual[i*4+0]
		d1 := residual[i*4+1]
		d2 := residual[i*4+2]
		d3 := residual[i*4+3]
		a0 := d0 + d3
		a1 := d1 + d2
		a2 := d1 - d2
		a3 := d0 - d3
		tmp[0+i*4] = (a0 + a1) * 8
		tmp[1+i*4] = (a2*2217 + a3*5352 + 1812) >> 9
		tmp[2+i*4] = (a0 - a1) * 8
		tmp[3+i*4] = (a3*2217 - a2*5352 + 937) >> 9
	}

	var out [16]int
	for i := 0; i < 4; i++ {
		a0 := tmp[0+i] + tmp[12+i]
		a1 := tmp[4+i] + tmp[8+i]
		a2 := tmp[4+i] - tmp[8+i]
		a3 := tmp[0+i] - tmp[12+i]
		out[0+i] = (a0 + a1 + 7) >> 4
		out[4+i] = ((a2*2217 + a3*5352 + 12000) >> 16) + boolInt(a3 != 0)
		out[8+i] = (a0 - a1 + 7) >> 4
		out[12+i] = (a3*2217 - a2*5352 + 51000) >> 16
	}
	return out
}

func quantizeTransformCoeff(v int, q int) int16 {
	if q <= 0 {
		return 0
	}
	sign := 1
	if v < 0 {
		sign = -1
		v = -v
	}
	return int16(sign * clipInt((v+q/2)/q, 0, 2047))
}

func forwardWHT4(in [16]int) [16]int {
	var tmp [16]int
	for i := 0; i < 4; i++ {
		a0 := in[i*4+0] + in[i*4+2]
		a1 := in[i*4+1] + in[i*4+3]
		a2 := in[i*4+1] - in[i*4+3]
		a3 := in[i*4+0] - in[i*4+2]
		tmp[0+i*4] = a0 + a1
		tmp[1+i*4] = a3 + a2
		tmp[2+i*4] = a3 - a2
		tmp[3+i*4] = a0 - a1
	}

	var out [16]int
	for i := 0; i < 4; i++ {
		a0 := tmp[0+i] + tmp[8+i]
		a1 := tmp[4+i] + tmp[12+i]
		a2 := tmp[4+i] - tmp[12+i]
		a3 := tmp[0+i] - tmp[8+i]
		out[0+i] = (a0 + a1) >> 1
		out[4+i] = (a3 + a2) >> 1
		out[8+i] = (a3 - a2) >> 1
		out[12+i] = (a0 - a1) >> 1
	}
	return out
}

func inverseWHT4(coeff [16]int) [16]int {
	var tmp [16]int
	for i := 0; i < 4; i++ {
		a0 := coeff[0+i] + coeff[12+i]
		a1 := coeff[4+i] + coeff[8+i]
		a2 := coeff[4+i] - coeff[8+i]
		a3 := coeff[0+i] - coeff[12+i]
		tmp[0+i] = a0 + a1
		tmp[8+i] = a0 - a1
		tmp[4+i] = a3 + a2
		tmp[12+i] = a3 - a2
	}

	var out [16]int
	for i := 0; i < 4; i++ {
		dc := tmp[0+i*4] + 3
		a0 := dc + tmp[3+i*4]
		a1 := tmp[1+i*4] + tmp[2+i*4]
		a2 := tmp[1+i*4] - tmp[2+i*4]
		a3 := dc - tmp[3+i*4]
		out[i*4+0] = (a0 + a1) >> 3
		out[i*4+1] = (a3 + a2) >> 3
		out[i*4+2] = (a0 - a1) >> 3
		out[i*4+3] = (a3 - a2) >> 3
	}
	return out
}

func reconstructVP8Block(pred [16]uint8, coeff vp8QuantizedBlock, dcQ int, acQ int) [16]uint8 {
	return inverseDCT4(pred, dequantizeVP8Block(coeff, dcQ, acQ))
}

func dequantizeVP8Block(coeff vp8QuantizedBlock, dcQ int, acQ int) [16]int {
	var dequant [16]int
	dequant[0] = int(coeff[0]) * dcQ
	for i := 1; i < 16; i++ {
		dequant[i] = int(coeff[i]) * acQ
	}
	return dequant
}

func inverseDCT4(pred [16]uint8, coeff [16]int) [16]uint8 {
	const (
		c1 = 85627
		c2 = 35468
	)

	var m [16]int
	for i := 0; i < 4; i++ {
		a := coeff[0+i] + coeff[8+i]
		b := coeff[0+i] - coeff[8+i]
		c := (coeff[4+i]*c2)>>16 - (coeff[12+i]*c1)>>16
		d := (coeff[4+i]*c1)>>16 + (coeff[12+i]*c2)>>16
		m[i*4+0] = a + d
		m[i*4+1] = b + c
		m[i*4+2] = b - c
		m[i*4+3] = a - d
	}

	var out [16]uint8
	for j := 0; j < 4; j++ {
		dc := m[0*4+j] + 4
		a := dc + m[2*4+j]
		b := dc - m[2*4+j]
		c := (m[1*4+j]*c2)>>16 - (m[3*4+j]*c1)>>16
		d := (m[1*4+j]*c1)>>16 + (m[3*4+j]*c2)>>16
		out[j*4+0] = clipUint8(int(pred[j*4+0]) + ((a + d) >> 3))
		out[j*4+1] = clipUint8(int(pred[j*4+1]) + ((b + c) >> 3))
		out[j*4+2] = clipUint8(int(pred[j*4+2]) + ((b - c) >> 3))
		out[j*4+3] = clipUint8(int(pred[j*4+3]) + ((a - d) >> 3))
	}
	return out
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func hasNonZeroBlockCoeff(coeff vp8QuantizedBlock) bool {
	return hasNonZeroBlockCoeffFrom(coeff, 0)
}

func hasNonZeroBlockCoeffFrom(coeff vp8QuantizedBlock, start int) bool {
	for i := start; i < len(coeff); i++ {
		if coeff[i] != 0 {
			return true
		}
	}
	return false
}

func clipUint8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func clipInt(v int, min int, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
