package webp

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

func TestAlphaLZ77PlanUsesPreviousRowNeighborhoodDistances(t *testing.T) {
	previous := []uint8{10, 20, 30, 40, 50, 60, 70, 80, 90}

	topLeft := []uint8{99, 10, 20, 30, 40, 50, 60, 70, 80}
	var topLeftPlan alphaResidualPlan
	topLeftPlan.observeLZ77Row(topLeft, previous, true)
	topLeftPlan.flushRLE()
	if topLeftPlan.distanceCounts[vp8lDistancePrefixCode(alphaDistanceTopLeft).code] == 0 {
		t.Fatal("missing top-left distance reference")
	}

	topRight := []uint8{20, 30, 40, 50, 60, 70, 80, 90, 99}
	var topRightPlan alphaResidualPlan
	topRightPlan.observeLZ77Row(topRight, previous, true)
	topRightPlan.flushRLE()
	if topRightPlan.distanceCounts[vp8lDistancePrefixCode(alphaDistanceTopRight).code] == 0 {
		t.Fatal("missing top-right distance reference")
	}
}

func TestAlphaLZ77PlanUsesExpandedPreviousRowDistances(t *testing.T) {
	previous := []uint8{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	current := []uint8{201, 202, 10, 20, 30, 40, 50, 60, 70, 80}
	var plan alphaResidualPlan
	plan.observeLZ77Row(current, previous, true)
	plan.flushRLE()
	distanceCode, ok := vp8lDistanceCodeForPositionDistance(len(previous)+2, len(previous))
	if !ok {
		t.Fatal("expanded previous-row distance code is unavailable")
	}
	symbol := vp8lDistancePrefixCode(distanceCode).code
	if plan.distanceCounts[symbol] == 0 {
		t.Fatalf("missing expanded previous-row distance symbol %d", symbol)
	}
}

func TestAlphaDistanceCodeUsesNormalTreeForNeighborhoodDistances(t *testing.T) {
	var plan alphaResidualPlan
	plan.observeCopy(alphaMinBackwardRefLength, alphaDistanceAbove)
	plan.observeCopy(alphaMinBackwardRefLength, alphaDistancePrevious)
	plan.observeCopy(alphaMinBackwardRefLength, alphaDistanceTopLeft)
	plan.observeCopy(alphaMinBackwardRefLength, alphaDistanceTopRight)
	code, ok := alphaCodeFor(plan)
	if !ok {
		t.Fatal("alphaCodeFor returned false")
	}
	if !code.distanceNormal {
		t.Fatal("distance tree is not normal")
	}
	for _, distanceCode := range []int{
		alphaDistanceAbove,
		alphaDistancePrevious,
		alphaDistanceTopLeft,
		alphaDistanceTopRight,
	} {
		symbol := vp8lDistancePrefixCode(distanceCode).code
		if code.distanceLengths[symbol] == 0 {
			t.Fatalf("distance symbol %d has zero code length", symbol)
		}
	}
}

func TestAlphaOptimalPlansImproveGreedyCandidate(t *testing.T) {
	found := false
	for _, img := range []*image.NRGBA{
		newAlphaSizeEstimateRunsImage(),
		newAlphaSizeEstimateNeighborhoodImage(),
	} {
		bounds := img.Bounds()
		analysis := analyzeLossyAlphaConfig(pixelReaderFor(img), bounds, bounds.Dx(), bounds.Dy(), lossyAlphaConfigForMode(ModeBestCompression))
		for filter, optimal := range analysis.optimalResiduals {
			if len(optimal.tokens) == 0 {
				continue
			}
			greedy := analysis.lz77Residuals[filter]
			greedyCode, ok := alphaCodeFor(greedy)
			if !ok {
				t.Fatalf("filter %d greedy code is unavailable", filter)
			}
			greedyCode.lz77 = true
			greedyCode.rowCopy = true
			optimalCode, ok := alphaCodeFor(optimal)
			if !ok {
				t.Fatalf("filter %d optimal code is unavailable", filter)
			}
			optimalCode.lz77 = true
			if got, wantMax := alphaVP8LStreamSize(optimal, optimalCode), alphaVP8LStreamSize(greedy, greedyCode); got >= wantMax {
				t.Fatalf("filter %d optimal size = %d, want < greedy size %d", filter, got, wantMax)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no alpha fixture produced an improved optimal plan")
	}
}

func TestAlphaPayloadCandidateSizeMatchesEncodedStream(t *testing.T) {
	for _, img := range []*image.NRGBA{
		newAlphaSizeEstimateRunsImage(),
		newAlphaSizeEstimateNeighborhoodImage(),
	} {
		readPixel := pixelReaderFor(img)
		bounds := img.Bounds()
		width, height := bounds.Dx(), bounds.Dy()
		cfg := lossyAlphaConfigForMode(ModeBestCompression)
		analysis := analyzeLossyAlphaConfig(readPixel, bounds, width, height, cfg)
		candidates := appendAlphaPayloadCandidatesConfig(nil, analysis, cfg)
		if len(candidates) == 0 {
			t.Fatal("no alpha payload candidates")
		}
		for _, candidate := range candidates {
			stream, err := encodeAlphaVP8LStream(readPixel, bounds, width, height, candidate.filter, candidate.plan, candidate.code)
			if err != nil {
				t.Fatalf("encodeAlphaVP8LStream failed: %v", err)
			}
			want := uint64(1 + len(stream))
			if got := alphaPayloadCandidateSize(candidate); got != want {
				t.Fatalf("candidate size = %d, want %d", got, want)
			}
		}
	}
}

func newAlphaSizeEstimateRunsImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 96, 6))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			alpha := uint8(32)
			if (x/12+y)%2 == 1 {
				alpha = 220
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*7 + y*3),
				G: uint8(y*11 + x),
				B: uint8(x*5 + y*13),
				A: alpha,
			})
		}
	}
	return img
}

func newAlphaSizeEstimateNeighborhoodImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 96, 9))
	for y := 0; y < img.Rect.Dy(); y++ {
		shift := 0
		if y%2 == 1 {
			shift = -1
		}
		for x := 0; x < img.Rect.Dx(); x++ {
			index := x + shift
			if index < 0 {
				index += img.Rect.Dx()
			}
			alpha := uint8(32 + (index*37)%191)
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*3 + y),
				G: uint8(y*5 + x/2),
				B: uint8((x+y)*2 + x*y/17),
				A: alpha,
			})
		}
	}
	return img
}

func huffmanKraftSumForTest(lengths [nLiteralCodes + nLengthCodes]uint8) int {
	sum := 0
	for _, length := range lengths {
		if length != 0 {
			sum += 1 << (15 - length)
		}
	}
	return sum
}

func alphaCodeLengthCodeKraftSumForTest(lengths [alphaCodeLengthCodeCount]uint8) int {
	sum := 0
	for _, length := range lengths {
		if length != 0 {
			sum += 1 << (alphaCodeLengthCodeMaxLength - length)
		}
	}
	return sum
}

func assertCanonicalCodesForTest(t *testing.T, lengths []uint8, codes []uint16) {
	t.Helper()
	if len(codes) < len(lengths) {
		t.Fatalf("code count = %d, want at least %d", len(codes), len(lengths))
	}
	var seen [1 << 15]bool
	previousLength := uint8(0)
	previousCode := uint16(0)
	havePrevious := false
	for symbol, length := range lengths {
		if length == 0 {
			if codes[symbol] != 0 {
				t.Fatalf("unused code[%d] = %b, want 0", symbol, codes[symbol])
			}
			continue
		}
		if length > 15 {
			t.Fatalf("length[%d] = %d, want at most 15", symbol, length)
		}
		code := codes[symbol]
		if code >= 1<<length {
			t.Fatalf("code[%d] = %b exceeds length %d", symbol, code, length)
		}
		prefix := int(code) << (15 - length)
		span := 1 << (15 - length)
		for i := 0; i < span; i++ {
			if seen[prefix+i] {
				t.Fatalf("code[%d] = %b length %d overlaps an earlier code", symbol, code, length)
			}
			seen[prefix+i] = true
		}
		if havePrevious {
			if length < previousLength {
				t.Fatalf("length[%d] = %d after previous length %d", symbol, length, previousLength)
			}
			if length == previousLength && code <= previousCode {
				t.Fatalf("code[%d] = %b, want greater than previous code %b for equal length", symbol, code, previousCode)
			}
		}
		previousLength = length
		previousCode = code
		havePrevious = true
	}
}

func expandAlphaCodeLengthTokensForTest(tokens []alphaCodeLengthToken, n int) []uint8 {
	out := make([]uint8, 0, n)
	previousNonZero := uint8(8)
	for _, token := range tokens {
		switch token.symbol {
		case alphaCodeLengthRepeatPrevious:
			run := int(token.extra) + 3
			for i := 0; i < run; i++ {
				out = append(out, previousNonZero)
			}
		case alphaCodeLengthRepeatZero:
			run := int(token.extra) + 3
			for i := 0; i < run; i++ {
				out = append(out, 0)
			}
		case alphaCodeLengthRepeatZeroBig:
			run := int(token.extra) + 11
			for i := 0; i < run; i++ {
				out = append(out, 0)
			}
		default:
			out = append(out, token.symbol)
			if token.symbol != 0 {
				previousNonZero = token.symbol
			}
		}
	}
	if len(out) > n {
		return out[:n]
	}
	for len(out) < n {
		out = append(out, 0)
	}
	return out
}

func assertLossyVP8Frame(t *testing.T, frame []byte, wantWidth int, wantHeight int) {
	t.Helper()
	if len(frame) < 10 {
		t.Fatalf("VP8 frame length = %d, want at least 10", len(frame))
	}
	frameTag := uint32(frame[0]) | uint32(frame[1])<<8 | uint32(frame[2])<<16
	if frameTag&1 != 0 {
		t.Fatal("VP8 frame is not a key frame")
	}
	if frameTag>>4&1 != 1 {
		t.Fatal("VP8 frame show_frame flag is false")
	}
	firstPartitionLen := int(frameTag >> 5)
	if firstPartitionLen <= 0 || 10+firstPartitionLen >= len(frame) {
		t.Fatalf("first partition length = %d, frame length = %d", firstPartitionLen, len(frame))
	}
	if !bytes.Equal(frame[3:6], []byte{0x9d, 0x01, 0x2a}) {
		t.Fatalf("invalid VP8 start code: % x", frame[3:6])
	}
	width := int(binary.LittleEndian.Uint16(frame[6:8]) & 0x3fff)
	height := int(binary.LittleEndian.Uint16(frame[8:10]) & 0x3fff)
	if width != wantWidth || height != wantHeight {
		t.Fatalf("VP8 dimensions = %dx%d, want %dx%d", width, height, wantWidth, wantHeight)
	}
}

func readVP8LoopFilterHeader(t *testing.T, frame []byte) vp8LoopFilter {
	t.Helper()
	firstPart := readVP8FirstPartition(t, frame)
	var r testVP8PartitionReader
	r.init(firstPart)

	colorSpace := r.readUint(128, 1)
	pixelClamp := r.readUint(128, 1)
	readVP8SegmentationHeader(t, &r)
	simple := r.readBit(128)
	level := r.readUint(128, 6)
	sharpness := r.readUint(128, 3)
	deltaEnabled, refDeltas, modeDeltas := readVP8LoopFilterDeltas(t, &r)
	if r.unexpectedEOF {
		t.Fatal("unexpected end of VP8 first partition")
	}
	if colorSpace != 0 {
		t.Fatalf("VP8 color space = %d, want 0", colorSpace)
	}
	if pixelClamp != 0 {
		t.Fatalf("VP8 pixel clamp = %d, want 0", pixelClamp)
	}
	return vp8LoopFilter{
		simple:       simple,
		level:        int(level),
		sharpness:    int(sharpness),
		deltaEnabled: deltaEnabled,
		refDeltas:    refDeltas,
		modeDeltas:   modeDeltas,
	}
}

type testVP8SegmentationHeader struct {
	enabled      bool
	updateMap    bool
	updateData   bool
	absolute     bool
	quantizers   [vp8SegmentCount]int
	filterLevels [vp8SegmentCount]int
	mapProbs     [3]uint8
}

func readVP8SegmentationHeader(t *testing.T, r *testVP8PartitionReader) testVP8SegmentationHeader {
	t.Helper()
	header := testVP8SegmentationHeader{
		enabled:  r.readBit(128),
		mapProbs: [3]uint8{255, 255, 255},
	}
	if !header.enabled {
		return header
	}
	header.updateMap = r.readBit(128)
	header.updateData = r.readBit(128)
	if header.updateData {
		header.absolute = r.readBit(128)
		for i := range header.quantizers {
			header.quantizers[i] = readVP8OptionalSignedLiteral(r, 7)
		}
		for i := range header.filterLevels {
			header.filterLevels[i] = readVP8OptionalSignedLiteral(r, 6)
		}
	}
	if header.updateMap {
		for i := range header.mapProbs {
			if r.readBit(128) {
				header.mapProbs[i] = uint8(r.readUint(128, 8))
			}
		}
	}
	if r.unexpectedEOF {
		t.Fatal("unexpected end while reading segmentation header")
	}
	return header
}

func readVP8OptionalSignedLiteral(r *testVP8PartitionReader, bits uint8) int {
	if !r.readBit(128) {
		return 0
	}
	value := int(r.readUint(128, bits))
	if r.readBit(128) {
		return -value
	}
	return value
}

func readVP8QuantDeltas(r *testVP8PartitionReader) vp8QuantDeltas {
	return vp8QuantDeltas{
		y1DC: readVP8OptionalSignedLiteral(r, 4),
		y2DC: readVP8OptionalSignedLiteral(r, 4),
		y2AC: readVP8OptionalSignedLiteral(r, 4),
		uvDC: readVP8OptionalSignedLiteral(r, 4),
		uvAC: readVP8OptionalSignedLiteral(r, 4),
	}
}

func readVP8SegmentID(r *testVP8PartitionReader, probs [3]uint8) uint8 {
	if !r.readBit(probs[0]) {
		if r.readBit(probs[1]) {
			return 1
		}
		return 0
	}
	if r.readBit(probs[2]) {
		return 3
	}
	return 2
}

func readVP8LoopFilterDeltas(t *testing.T, r *testVP8PartitionReader) (bool, [4]int, [4]int) {
	t.Helper()
	var refDeltas [4]int
	var modeDeltas [4]int
	if !r.readBit(128) {
		return false, refDeltas, modeDeltas
	}
	if !r.readBit(128) {
		return true, refDeltas, modeDeltas
	}
	for i := range refDeltas {
		refDeltas[i] = readVP8LoopFilterDelta(r)
	}
	for i := range modeDeltas {
		modeDeltas[i] = readVP8LoopFilterDelta(r)
	}
	if r.unexpectedEOF {
		t.Fatal("unexpected end while reading loop filter deltas")
	}
	return true, refDeltas, modeDeltas
}

func readVP8LoopFilterDelta(r *testVP8PartitionReader) int {
	if !r.readBit(128) {
		return 0
	}
	delta := int(r.readUint(128, 6))
	if r.readBit(128) {
		return -delta
	}
	return delta
}

func readVP8FirstPartition(t *testing.T, frame []byte) []byte {
	t.Helper()
	if len(frame) < 10 {
		t.Fatalf("VP8 frame length = %d, want at least 10", len(frame))
	}
	frameTag := uint32(frame[0]) | uint32(frame[1])<<8 | uint32(frame[2])<<16
	firstPartitionLen := int(frameTag >> 5)
	if firstPartitionLen <= 0 || 10+firstPartitionLen >= len(frame) {
		t.Fatalf("first partition length = %d, frame length = %d", firstPartitionLen, len(frame))
	}
	return frame[10 : 10+firstPartitionLen]
}

func readVP8FirstPartitionY4Modes(t *testing.T, firstPart []byte) [16]uint8 {
	t.Helper()
	var r testVP8PartitionReader
	r.init(firstPart)

	readVP8FirstPartitionHeaderBeforeTokenProbs(t, &r)
	readVP8FirstPartitionTokenProbs(t, &r)
	r.readBit(128) // macroblock skip probability
	if r.unexpectedEOF {
		t.Fatal("unexpected end before Y4 modes")
	}
	if useY16 := r.readBit(145); useY16 {
		t.Fatal("macroblock uses Y16 mode, want Y4")
	}

	var modes [16]uint8
	var up [4]uint8
	for by := 0; by < 4; by++ {
		p := uint8(0)
		for bx := 0; bx < 4; bx++ {
			mode := readVP8Y4Mode(&r, vp8PredProb[up[bx]][p])
			modes[by*4+bx] = mode
			p = mode
			up[bx] = mode
		}
	}
	if r.unexpectedEOF {
		t.Fatal("unexpected end while reading Y4 modes")
	}
	return modes
}

func readVP8FirstPartitionHeaderBeforeTokenProbs(t *testing.T, r *testVP8PartitionReader) {
	t.Helper()
	r.readUint(128, 1) // color space
	r.readUint(128, 1) // pixel clamp
	readVP8SegmentationHeader(t, r)
	r.readBit(128)     // loop filter type
	r.readUint(128, 6) // loop filter level
	r.readUint(128, 3) // sharpness
	readVP8LoopFilterDeltas(t, r)
	r.readUint(128, 2) // token partitions
	r.readUint(128, 7) // base quantizer
	readVP8QuantDeltas(r)
	r.readBit(128) // refresh last frame buffer
	if r.unexpectedEOF {
		t.Fatal("unexpected end before token probability updates")
	}
}

func readVP8FirstPartitionTokenProbs(t *testing.T, r *testVP8PartitionReader) vp8TokenProbs {
	t.Helper()
	probs := vp8DefaultTokenProbs
	for plane := range vp8TokenProbUpdateProb {
		for band := range vp8TokenProbUpdateProb[plane] {
			for context := range vp8TokenProbUpdateProb[plane][band] {
				for node, updateProb := range vp8TokenProbUpdateProb[plane][band][context] {
					if r.readBit(updateProb) {
						probs[plane][band][context][node] = uint8(r.readUint(128, 8))
					}
				}
			}
		}
	}
	if r.unexpectedEOF {
		t.Fatal("unexpected end while reading token probability updates")
	}
	return probs
}

func readVP8Y4Mode(r *testVP8PartitionReader, prob [9]uint8) uint8 {
	if !r.readBit(prob[0]) {
		return vp8PredDC
	}
	if !r.readBit(prob[1]) {
		return vp8PredTM
	}
	if !r.readBit(prob[2]) {
		return vp8PredVE
	}
	if !r.readBit(prob[3]) {
		if !r.readBit(prob[4]) {
			return vp8PredHE
		}
		if !r.readBit(prob[5]) {
			return vp8PredRD
		}
		return vp8PredVR
	}
	if !r.readBit(prob[6]) {
		return vp8PredLD
	}
	if !r.readBit(prob[7]) {
		return vp8PredVL
	}
	if !r.readBit(prob[8]) {
		return vp8PredHD
	}
	return vp8PredHU
}
