package webp

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
)

const (
	defaultLossyQuality  = 100
	vp8FirstPartitionMax = 1<<19 - 1
	vp8xAlphaFlag        = 0x10
	vp8xPayloadSize      = 10

	alphCompressionNone  = 0
	alphCompressionVP8L  = 1
	alphFilterNone       = 0
	alphFilterHorizontal = 1
	alphFilterVertical   = 2
	alphFilterGradient   = 3
)

func encodeLossy(w io.Writer, m image.Image, bounds image.Rectangle, width int, height int, quality int) error {
	if width > maxVP8Dimension || height > maxVP8Dimension {
		return fmt.Errorf("webp: image dimensions %dx%d exceed VP8 limit %dx%d", width, height, maxVP8Dimension, maxVP8Dimension)
	}

	readPixel := pixelReaderFor(m)
	alphaAnalysis := analyzeLossyAlpha(readPixel, bounds, width, height)
	qIndex := qualityToVP8QIndex(quality)
	frame, err := encodeVP8KeyFrame(readPixel, bounds, width, height, qIndex)
	if err != nil {
		return err
	}
	if alphaAnalysis.hasAlpha {
		return writeLossyExtended(w, readPixel, bounds, width, height, frame, alphaAnalysis)
	}
	return writeLossySimple(w, frame)
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

func writeLossyExtended(w io.Writer, readPixel pixelReader, bounds image.Rectangle, width int, height int, frame []byte, alphaAnalysis lossyAlphaAnalysis) error {
	framePayloadSize := uint64(len(frame))
	alphaPayload, err := makeAlphaPayload(readPixel, bounds, width, height, alphaAnalysis)
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

func makeAlphaPayload(readPixel pixelReader, bounds image.Rectangle, width int, height int, analysis lossyAlphaAnalysis) (alphaPayload, error) {
	rawSize := uint64(1) + uint64(width)*uint64(height)
	if rawSize > math.MaxUint32 {
		return alphaPayload{}, fmt.Errorf("webp: encoded image is too large")
	}

	best := alphaPayload{size: rawSize}
	for filter, plan := range analysis.residuals {
		if !plan.encodable() {
			continue
		}
		code, ok := alphaCodeFor(plan)
		if !ok {
			continue
		}
		stream, err := encodeAlphaVP8LStream(readPixel, bounds, width, height, byte(filter), code)
		if err != nil {
			return alphaPayload{}, err
		}
		size := uint64(1 + len(stream))
		if size < best.size {
			best = alphaPayload{
				size:       size,
				compressed: true,
				filter:     byte(filter),
				stream:     stream,
			}
		}
	}
	return best, nil
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
	writeSimpleTree(bits, 0)

	if code.n != 1 {
		writeAlphaResidualBits(bits, readPixel, bounds, width, height, filter, code)
	}
}

func writeAlphaGreenTree(bits *bitWriter, code alphaCode) {
	switch code.n {
	case 1:
		writeSimpleTree(bits, code.symbols[0])
	case 2:
		writeTwoSymbolTree(bits, code.symbols[0], code.symbols[1])
	default:
		writeAlphaNormalTree(bits, code.lengths)
	}
}

func writeAlphaNormalTree(bits *bitWriter, lengths [nLiteralCodes + nLengthCodes]uint8) {
	bits.writeBits(0, 1)
	bits.writeBits(15, 4)
	for _, symbol := range normalCodeLengthCodeOrder {
		length := uint8(0)
		if symbol <= 15 {
			length = 4
		}
		bits.writeBits(uint32(length), 3)
	}
	bits.writeBits(0, 1)
	for _, length := range lengths {
		bits.writeBits(uint32(reverseBits(uint16(length), 4)), 4)
	}
}

func writeAlphaResidualBits(bits *bitWriter, readPixel pixelReader, bounds image.Rectangle, width int, height int, filter byte, code alphaCode) {
	previous := make([]uint8, width)
	current := make([]uint8, width)
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
			writeAlphaCode(bits, code, alpha-alphaPredictor(filter, x, y, left, above, upperLeft))
			current[x] = alpha
			left = alpha
		}
		previous, current = current, previous
	}
}

func writeAlphaCode(bits *bitWriter, code alphaCode, symbol uint8) {
	switch code.n {
	case 1:
		return
	case 2:
		if symbol == code.symbols[0] {
			bits.writeBits(0, 1)
		} else {
			bits.writeBits(1, 1)
		}
	default:
		length := code.lengths[symbol]
		bits.writeBits(uint32(reverseBits(code.codes[symbol], length)), length)
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
	hasAlpha  bool
	residuals [4]alphaResidualPlan
}

type alphaResidualPlan struct {
	n       int
	symbols [2]uint8
	counts  [nLiteralCodes]uint32
}

func analyzeLossyAlpha(readPixel pixelReader, bounds image.Rectangle, width int, height int) lossyAlphaAnalysis {
	var analysis lossyAlphaAnalysis
	previous := make([]uint8, width)
	current := make([]uint8, width)
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
			analysis.residuals[alphFilterNone].observe(alpha)
			analysis.residuals[alphFilterHorizontal].observe(alpha - alphaPredictor(alphFilterHorizontal, x, y, left, above, upperLeft))
			analysis.residuals[alphFilterVertical].observe(alpha - alphaPredictor(alphFilterVertical, x, y, left, above, upperLeft))
			analysis.residuals[alphFilterGradient].observe(alpha - alphaPredictor(alphFilterGradient, x, y, left, above, upperLeft))
			current[x] = alpha
			left = alpha
		}
		previous, current = current, previous
	}
	return analysis
}

func (p *alphaResidualPlan) observe(value uint8) {
	if p.counts[value] == 0 {
		if p.n < len(p.symbols) {
			p.symbols[p.n] = value
		}
		p.n++
	}
	p.counts[value]++
}

func (p alphaResidualPlan) encodable() bool {
	return p.n > 0
}

type alphaCode struct {
	n       int
	symbols [2]uint8
	lengths [nLiteralCodes + nLengthCodes]uint8
	codes   [nLiteralCodes]uint16
}

func alphaCodeFor(plan alphaResidualPlan) (alphaCode, bool) {
	switch plan.n {
	case 0:
		return alphaCode{}, false
	case 1:
		return alphaCode{n: 1, symbols: plan.symbols}, true
	case 2:
		if plan.symbols[1] < 2 && plan.symbols[0] >= 2 {
			plan.symbols[0], plan.symbols[1] = plan.symbols[1], plan.symbols[0]
		}
		return alphaCode{n: 2, symbols: plan.symbols}, true
	default:
		lengths, ok := huffmanCodeLengths(plan.counts)
		if !ok {
			return alphaCode{}, false
		}
		codes := canonicalCodes(lengths)
		return alphaCode{n: plan.n, lengths: lengths, codes: codes}, true
	}
}

type huffmanNode struct {
	freq   uint64
	symbol int
	left   int
	right  int
}

func huffmanCodeLengths(counts [nLiteralCodes]uint32) ([nLiteralCodes + nLengthCodes]uint8, bool) {
	var lengths [nLiteralCodes + nLengthCodes]uint8
	var nodes []huffmanNode
	var active []int
	for symbol, count := range counts {
		if count == 0 {
			continue
		}
		nodes = append(nodes, huffmanNode{freq: uint64(count), symbol: symbol, left: -1, right: -1})
		active = append(active, len(nodes)-1)
	}
	if len(active) <= 2 {
		return lengths, false
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
		return lengths, false
	}
	return lengths, true
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

func canonicalCodes(lengths [nLiteralCodes + nLengthCodes]uint8) [nLiteralCodes]uint16 {
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

	var codes [nLiteralCodes]uint16
	for symbol, length := range lengths[:nLiteralCodes] {
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

func encodeVP8KeyFrame(readPixel pixelReader, bounds image.Rectangle, width int, height int, qIndex int) ([]byte, error) {
	mbw := (width + 15) >> 4
	mbh := (height + 15) >> 4
	quant := vp8QuantForIndex(qIndex)
	filter := vp8LoopFilterForQuant(quant)
	work := newVP8EncodeBuffers(mbw, mbh)
	modes := analyzeVP8Modes(readPixel, bounds, mbw, mbh, quant, work)
	work.clear()
	tokenStats := collectVP8TokenStats(readPixel, bounds, mbw, mbh, quant, modes, work)
	tokenProbs := chooseVP8TokenProbs(&tokenStats)
	firstPart, err := vp8FirstPartition(mbw, mbh, qIndex, filter, modes, tokenProbs)
	if err != nil {
		return nil, err
	}
	work.clear()
	residualPart := encodeVP8Residuals(readPixel, bounds, width, height, mbw, mbh, quant, modes, work, &tokenProbs)
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
	return &vp8EncodeBuffers{
		recY:  make([]uint8, yStride*mbh*16),
		recCb: make([]uint8, cStride*mbh*8),
		recCr: make([]uint8, cStride*mbh*8),
	}
}

func (b *vp8EncodeBuffers) clear() {
	clear(b.recY)
	clear(b.recCb)
	clear(b.recCr)
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

func vp8ResidualPartitionCapacity(width int, height int) int {
	capacity := width * height
	if capacity < 1024 {
		return 1024
	}
	if capacity > 1<<20 {
		return 1 << 20
	}
	return capacity
}

func vp8FirstPartition(mbw int, mbh int, qIndex int, filter vp8LoopFilter, modes []vp8MBMode, tokenProbs vp8TokenProbs) ([]byte, error) {
	bitCount := 2 + 1 + 11 + 2 + 12 + 1 + 8*8 + 4*8*3*11*9 + 1 + mbw*mbh*(1+16*7+3)
	capacity := (bitCount+7)/8 + 4
	if capacity > vp8FirstPartitionMax {
		capacity = vp8FirstPartitionMax
	}
	enc := newVP8BoolEncoderWithCapacity(capacity)
	writeVP8Literal(enc, 0, 1)       // color space
	writeVP8Literal(enc, 0, 1)       // pixel clamp
	enc.writeBit(128, false)         // no segmentation
	enc.writeBit(128, filter.simple) // loop filter type
	writeVP8Literal(enc, uint32(filter.level), 6)
	writeVP8Literal(enc, uint32(filter.sharpness), 3)
	writeVP8LoopFilterDeltas(enc, filter)
	writeVP8Literal(enc, 0, 2)              // one token partition
	writeVP8Literal(enc, uint32(qIndex), 7) // base quantizer index
	for i := 0; i < 5; i++ {
		enc.writeBit(128, false) // no quantizer delta update
	}
	enc.writeBit(128, false) // do not refresh last frame buffer
	writeVP8TokenProbUpdates(enc, tokenProbs)
	enc.writeBit(128, false) // no macroblock skip probability
	upPred := make([][4]uint8, mbw)
	for mby := 0; mby < mbh; mby++ {
		var leftPred [4]uint8
		for mbx := 0; mbx < mbw; mbx++ {
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
	enc.writeBit(128, filter.deltaEnabled)
	if !filter.deltaEnabled {
		return
	}
	enc.writeBit(128, true)
	for _, delta := range filter.refDeltas {
		writeVP8LoopFilterDelta(enc, delta)
	}
	for _, delta := range filter.modeDeltas {
		writeVP8LoopFilterDelta(enc, delta)
	}
}

func writeVP8LoopFilterDelta(enc *vp8BoolEncoder, delta int) {
	if delta == 0 {
		enc.writeBit(128, false)
		return
	}
	enc.writeBit(128, true)
	if delta < 0 {
		writeVP8Literal(enc, uint32(-delta), 6)
		enc.writeBit(128, true)
		return
	}
	writeVP8Literal(enc, uint32(delta), 6)
	enc.writeBit(128, false)
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
		enc.writeBit(128, false)
	case vp8PredTM:
		enc.writeBit(156, true)
		enc.writeBit(128, true)
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
		enc.writeBit(128, value&(1<<n) != 0)
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

func analyzeVP8Modes(readPixel pixelReader, bounds image.Rectangle, mbw int, mbh int, quant vp8Quant, work *vp8EncodeBuffers) []vp8MBMode {
	yStride := mbw * 16
	cStride := mbw * 8
	recY := work.recY
	recCb := work.recCb
	recCr := work.recCr
	modes := make([]vp8MBMode, mbw*mbh)
	rd := newVP8RDConfig(quant)
	upPred := make([][4]uint8, mbw)
	upY := make([][4]uint8, mbw)
	upUV := make([][4]uint8, mbw)
	upY16 := make([]uint8, mbw)

	for mby := 0; mby < mbh; mby++ {
		var leftPred [4]uint8
		var leftY [4]uint8
		var leftUV [4]uint8
		var leftY16 uint8
		for mbx := 0; mbx < mbw; mbx++ {
			mode := vp8MBMode{
				cMode: chooseVP8ChromaMode(readPixel, bounds, mbx, mby, recCb, recCr, cStride, quant, rd, &leftUV, &upUV[mbx]),
			}
			var savedLuma [256]uint8
			saveLumaMB(recY, yStride, mbx, mby, &savedLuma)
			savedLeftPred := leftPred
			savedUpPred := upPred[mbx]
			savedLeftY := leftY
			savedUpY := upY[mbx]
			savedLeftY16 := leftY16
			savedUpY16 := upY16[mbx]

			y16Mode, y16Score := chooseVP8Y16Mode(readPixel, bounds, mbx, mby, recY, yStride, quant, rd, &leftY, &upY[mbx], &leftY16, &upY16[mbx])
			y4Score := chooseVP8Y4Modes(readPixel, bounds, mbx, mby, recY, yStride, quant, rd, &leftPred, &upPred[mbx], &leftY, &upY[mbx], &mode)
			if y16Score <= y4Score {
				restoreLumaMB(recY, yStride, mbx, mby, &savedLuma)
				leftPred = savedLeftPred
				upPred[mbx] = savedUpPred
				leftY = savedLeftY
				upY[mbx] = savedUpY
				leftY16 = savedLeftY16
				upY16[mbx] = savedUpY16
				mode.useY16 = true
				mode.yMode = y16Mode
				for i := 0; i < 4; i++ {
					leftPred[i] = y16Mode
					upPred[mbx][i] = y16Mode
				}
				processVP8Luma16MB(nil, readPixel, bounds, mbx, mby, recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], nil, nil)
			}
			processVP8ChromaMB(nil, readPixel, bounds, mbx, mby, recCb, recCr, cStride, quant, mode, &leftUV, &upUV[mbx], nil, nil)
			modes[mby*mbw+mbx] = mode
		}
	}
	return modes
}

func saveLumaMB(recY []uint8, stride int, mbx int, mby int, dst *[256]uint8) {
	x0 := mbx * 16
	y0 := mby * 16
	for y := 0; y < 16; y++ {
		copy(dst[y*16:y*16+16], recY[(y0+y)*stride+x0:(y0+y)*stride+x0+16])
	}
}

func restoreLumaMB(recY []uint8, stride int, mbx int, mby int, src *[256]uint8) {
	x0 := mbx * 16
	y0 := mby * 16
	for y := 0; y < 16; y++ {
		copy(recY[(y0+y)*stride+x0:(y0+y)*stride+x0+16], src[y*16:y*16+16])
	}
}

func collectVP8TokenStats(readPixel pixelReader, bounds image.Rectangle, mbw int, mbh int, quant vp8Quant, modes []vp8MBMode, work *vp8EncodeBuffers) vp8TokenStats {
	yStride := mbw * 16
	cStride := mbw * 8
	var stats vp8TokenStats
	upY := make([][4]uint8, mbw)
	upUV := make([][4]uint8, mbw)
	upY16 := make([]uint8, mbw)

	for mby := 0; mby < mbh; mby++ {
		var leftY [4]uint8
		var leftUV [4]uint8
		var leftY16 uint8
		for mbx := 0; mbx < mbw; mbx++ {
			mode := modes[mby*mbw+mbx]
			processVP8LumaMB(nil, readPixel, bounds, mbx, mby, work.recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], nil, &stats)
			processVP8ChromaMB(nil, readPixel, bounds, mbx, mby, work.recCb, work.recCr, cStride, quant, mode, &leftUV, &upUV[mbx], nil, &stats)
		}
	}
	return stats
}

func encodeVP8Residuals(readPixel pixelReader, bounds image.Rectangle, width int, height int, mbw int, mbh int, quant vp8Quant, modes []vp8MBMode, work *vp8EncodeBuffers, tokenProbs *vp8TokenProbs) []byte {
	yStride := mbw * 16
	cStride := mbw * 8
	recY := work.recY
	recCb := work.recCb
	recCr := work.recCr

	enc := newVP8BoolEncoderWithCapacity(vp8ResidualPartitionCapacity(width, height))
	upY := make([][4]uint8, mbw)
	upUV := make([][4]uint8, mbw)
	upY16 := make([]uint8, mbw)

	for mby := 0; mby < mbh; mby++ {
		var leftY [4]uint8
		var leftUV [4]uint8
		var leftY16 uint8
		for mbx := 0; mbx < mbw; mbx++ {
			mode := modes[mby*mbw+mbx]
			processVP8LumaMB(enc, readPixel, bounds, mbx, mby, recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], tokenProbs, nil)
			processVP8ChromaMB(enc, readPixel, bounds, mbx, mby, recCb, recCr, cStride, quant, mode, &leftUV, &upUV[mbx], tokenProbs, nil)
		}
	}
	return enc.bytes()
}

func reconstructVP8LumaMB(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, mode vp8MBMode) {
	if mode.useY16 {
		processVP8Luma16MB(nil, readPixel, bounds, mbx, mby, recY, stride, quant, mode, nil, nil, nil, nil, nil, nil)
		return
	}
	processVP8Luma4MB(nil, readPixel, bounds, mbx, mby, recY, stride, quant, nil, nil, mode, nil, nil)
}

func processVP8LumaMB(enc *vp8BoolEncoder, readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, mode vp8MBMode, left *[4]uint8, up *[4]uint8, leftY16 *uint8, upY16 *uint8, tokenProbs *vp8TokenProbs, stats *vp8TokenStats) {
	if mode.useY16 {
		processVP8Luma16MB(enc, readPixel, bounds, mbx, mby, recY, stride, quant, mode, left, up, leftY16, upY16, tokenProbs, stats)
		return
	}
	processVP8Luma4MB(enc, readPixel, bounds, mbx, mby, recY, stride, quant, left, up, mode, tokenProbs, stats)
	*leftY16 = 0
	*upY16 = 0
}

func processVP8Luma4MB(enc *vp8BoolEncoder, readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, left *[4]uint8, up *[4]uint8, mode vp8MBMode, tokenProbs *vp8TokenProbs, stats *vp8TokenStats) {
	var localLeft [4]uint8
	var localUp [4]uint8
	if left == nil {
		left = &localLeft
	}
	if up == nil {
		up = &localUp
	}
	for by := 0; by < 4; by++ {
		nz := left[by]
		for bx := 0; bx < 4; bx++ {
			x := mbx*16 + bx*4
			y := mby*16 + by*4
			pred := predictLuma4(recY, stride, x, y, mode.y4Modes[by*4+bx])
			residual := lumaResidualBlock(readPixel, bounds, x, y, pred)
			coeff := quantizeVP8Block(residual, quant.y1DC, quant.y1AC)
			blockNZ := uint8(0)
			context := nz + up[bx]
			if stats != nil {
				blockNZ = vp8RecordBlockTokens(stats, vp8PlaneY1SansY2, context, coeff)
			} else if enc != nil {
				blockNZ = encodeVP8BlockWithProbs(enc, tokenProbs, vp8PlaneY1SansY2, context, coeff)
			} else if hasNonZeroBlockCoeff(coeff) {
				blockNZ = 1
			}
			recon := reconstructVP8Block(pred, coeff, quant.y1DC, quant.y1AC)
			put4(recY, stride, x, y, recon)
			nz = blockNZ
			up[bx] = blockNZ
		}
		left[by] = nz
	}
}

func processVP8Luma16MB(enc *vp8BoolEncoder, readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, mode vp8MBMode, left *[4]uint8, up *[4]uint8, leftY16 *uint8, upY16 *uint8, tokenProbs *vp8TokenProbs, stats *vp8TokenStats) {
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
			pred := subLuma16Block(pred16, bx, by)
			residual := lumaResidualBlock(readPixel, bounds, x, y, pred)
			block := forwardDCT4(residual)
			index := by*4 + bx
			transformed[index] = block
			y2Input[index] = block[0]
		}
	}

	y2Coeff := quantizeTransformedVP8Block(forwardWHT4(y2Input), quant.y2DC, quant.y2AC)
	y16NZ := uint8(0)
	y2Context := *leftY16 + *upY16
	if stats != nil {
		y16NZ = vp8RecordBlockTokens(stats, vp8PlaneY2, y2Context, y2Coeff)
	} else if enc != nil {
		y16NZ = encodeVP8BlockWithProbs(enc, tokenProbs, vp8PlaneY2, y2Context, y2Coeff)
	} else if hasNonZeroBlockCoeff(y2Coeff) {
		y16NZ = 1
	}
	*leftY16 = y16NZ
	*upY16 = y16NZ
	y2Recon := inverseWHT4(dequantizeVP8Block(y2Coeff, quant.y2DC, quant.y2AC))

	for by := 0; by < 4; by++ {
		nz := left[by]
		for bx := 0; bx < 4; bx++ {
			index := by*4 + bx
			coeff := quantizeTransformedVP8Block(transformed[index], 0, quant.y1AC)
			coeff[0] = 0
			blockNZ := uint8(0)
			context := nz + up[bx]
			if stats != nil {
				blockNZ = vp8RecordBlockTokensFrom(stats, vp8PlaneY1WithY2, context, coeff, 1)
			} else if enc != nil {
				blockNZ = encodeVP8BlockSkipFirstWithProbs(enc, tokenProbs, vp8PlaneY1WithY2, context, coeff)
			} else if hasNonZeroBlockCoeffFrom(coeff, 1) {
				blockNZ = 1
			}
			reconCoeff := dequantizeVP8Block(coeff, 0, quant.y1AC)
			reconCoeff[0] = y2Recon[index]
			recon := inverseDCT4(subLuma16Block(pred16, bx, by), reconCoeff)
			put4(recY, stride, mbx*16+bx*4, mby*16+by*4, recon)
			nz = blockNZ
			up[bx] = blockNZ
		}
		left[by] = nz
	}
}

func reconstructVP8ChromaMB(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, mode vp8MBMode) {
	processVP8ChromaPlane(nil, readPixel, bounds, mbx, mby, recCb, stride, quant, nil, nil, mode.cMode, true, nil, nil)
	processVP8ChromaPlane(nil, readPixel, bounds, mbx, mby, recCr, stride, quant, nil, nil, mode.cMode, false, nil, nil)
}

func processVP8ChromaMB(enc *vp8BoolEncoder, readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, mode vp8MBMode, left *[4]uint8, up *[4]uint8, tokenProbs *vp8TokenProbs, stats *vp8TokenStats) {
	processVP8ChromaPlane(enc, readPixel, bounds, mbx, mby, recCb, stride, quant, left, up, mode.cMode, true, tokenProbs, stats)
	processVP8ChromaPlane(enc, readPixel, bounds, mbx, mby, recCr, stride, quant, left, up, mode.cMode, false, tokenProbs, stats)
}

func processVP8ChromaPlane(enc *vp8BoolEncoder, readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, rec []uint8, stride int, quant vp8Quant, left *[4]uint8, up *[4]uint8, mode uint8, cb bool, tokenProbs *vp8TokenProbs, stats *vp8TokenStats) {
	var localLeft [4]uint8
	var localUp [4]uint8
	if left == nil {
		left = &localLeft
	}
	if up == nil {
		up = &localUp
	}
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
			pred := subChroma8Block(pred8, bx, by)
			residual := chromaResidualBlock(readPixel, bounds, mbx*16+bx*8, mby*16+by*8, pred, cb)
			coeff := quantizeVP8Block(residual, quant.uvDC, quant.uvAC)
			blockNZ := uint8(0)
			context := nz + up[base+bx]
			if stats != nil {
				blockNZ = vp8RecordBlockTokens(stats, vp8PlaneUV, context, coeff)
			} else if enc != nil {
				blockNZ = encodeVP8BlockWithProbs(enc, tokenProbs, vp8PlaneUV, context, coeff)
			} else if hasNonZeroBlockCoeff(coeff) {
				blockNZ = 1
			}
			recon := reconstructVP8Block(pred, coeff, quant.uvDC, quant.uvAC)
			put4(rec, stride, x, y, recon)
			nz = blockNZ
			up[base+bx] = blockNZ
		}
		left[base+by] = nz
	}
}

func chooseVP8Y4Modes(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, leftPred *[4]uint8, upPred *[4]uint8, leftNZ *[4]uint8, upNZ *[4]uint8, mode *vp8MBMode) int64 {
	score := rd.lumaScore(0, vp8BitCost(145, false))
	for by := 0; by < 4; by++ {
		p := leftPred[by]
		nz := leftNZ[by]
		for bx := 0; bx < 4; bx++ {
			x := mbx*16 + bx*4
			y := mby*16 + by*4
			blockMode, blockScore, blockNZ := chooseVP8Y4Mode(readPixel, bounds, x, y, recY, stride, quant, rd, upPred[bx], p, nz+upNZ[bx])
			mode.y4Modes[by*4+bx] = blockMode
			pred := predictLuma4(recY, stride, x, y, blockMode)
			residual := lumaResidualBlock(readPixel, bounds, x, y, pred)
			coeff := quantizeVP8Block(residual, quant.y1DC, quant.y1AC)
			recon := reconstructVP8Block(pred, coeff, quant.y1DC, quant.y1AC)
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

func chooseVP8Y4Mode(readPixel pixelReader, bounds image.Rectangle, x int, y int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, topPred uint8, leftPred uint8, context uint8) (uint8, int64, uint8) {
	bestMode := uint8(vp8PredDC)
	bestScore := int64(1<<63 - 1)
	bestNZ := uint8(0)
	for mode := uint8(0); mode < vp8NumPredModes; mode++ {
		pred := predictLuma4(recY, stride, x, y, mode)
		residual := lumaResidualBlock(readPixel, bounds, x, y, pred)
		coeff := quantizeVP8Block(residual, quant.y1DC, quant.y1AC)
		recon := reconstructVP8Block(pred, coeff, quant.y1DC, quant.y1AC)
		distortion := scoreLuma4(readPixel, bounds, x, y, recon)
		bitCost := vp8Y4ModeCost(topPred, leftPred, mode) + vp8BlockBitCost(vp8PlaneY1SansY2, context, coeff)
		score := rd.lumaScore(distortion, bitCost)
		if score < bestScore {
			bestScore = score
			bestMode = mode
			if hasNonZeroBlockCoeff(coeff) {
				bestNZ = 1
			} else {
				bestNZ = 0
			}
		}
	}
	return bestMode, bestScore, bestNZ
}

func scoreLuma4(readPixel pixelReader, bounds image.Rectangle, x int, y int, block [16]uint8) int64 {
	var score int64
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			c := samplePixel(readPixel, bounds, x+xx, y+yy)
			luma := rgbToLuma(c.R, c.G, c.B)
			score += squareInt(int(luma) - int(block[yy*4+xx]))
		}
	}
	return score
}

func vp8Y4ModeCost(topPred uint8, leftPred uint8, mode uint8) int64 {
	prob := vp8PredProb[topPred][leftPred]
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

func chooseVP8Y16Mode(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, left *[4]uint8, up *[4]uint8, leftY16 *uint8, upY16 *uint8) (uint8, int64) {
	bestMode := vp8PredDC
	bestScore := int64(1<<63 - 1)
	modes, nModes := vp8CandidatePredModes(mbx, mby)
	for i := 0; i < nModes; i++ {
		mode := modes[i]
		score := scoreLuma16RD(readPixel, bounds, mbx, mby, recY, stride, quant, rd, left, up, *leftY16+*upY16, mode)
		if score < bestScore {
			bestScore = score
			bestMode = mode
		}
	}
	return bestMode, bestScore
}

func scoreLuma16RD(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, left *[4]uint8, up *[4]uint8, y16Context uint8, mode uint8) int64 {
	pred16 := predictLuma16(recY, stride, mbx, mby, mode)
	var transformed [16][16]int
	var y2Input [16]int
	for by := 0; by < 4; by++ {
		for bx := 0; bx < 4; bx++ {
			x := mbx*16 + bx*4
			y := mby*16 + by*4
			pred := subLuma16Block(pred16, bx, by)
			residual := lumaResidualBlock(readPixel, bounds, x, y, pred)
			block := forwardDCT4(residual)
			index := by*4 + bx
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
			coeff := quantizeTransformedVP8Block(transformed[index], 0, quant.y1AC)
			coeff[0] = 0
			bitCost += vp8BlockBitCostFrom(vp8PlaneY1WithY2, nz+localUp[bx], coeff, 1)
			reconCoeff := dequantizeVP8Block(coeff, 0, quant.y1AC)
			reconCoeff[0] = y2Recon[index]
			recon := inverseDCT4(subLuma16Block(pred16, bx, by), reconCoeff)
			distortion += scoreLuma4(readPixel, bounds, mbx*16+bx*4, mby*16+by*4, recon)
			if hasNonZeroBlockCoeffFrom(coeff, 1) {
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

func chooseVP8ChromaMode(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, rd vp8RDConfig, left *[4]uint8, up *[4]uint8) uint8 {
	bestMode := vp8PredDC
	bestScore := int64(1<<63 - 1)
	modes, nModes := vp8CandidatePredModes(mbx, mby)
	for i := 0; i < nModes; i++ {
		mode := modes[i]
		score := scoreChromaRD(readPixel, bounds, mbx, mby, recCb, recCr, stride, quant, rd, left, up, mode)
		if score < bestScore {
			bestScore = score
			bestMode = mode
		}
	}
	return bestMode
}

func scoreChromaRD(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, rd vp8RDConfig, left *[4]uint8, up *[4]uint8, mode uint8) int64 {
	localLeft := *left
	localUp := *up
	bitCost := vp8ChromaModeCost(mode)
	distortion, cbBits := scoreChromaPlaneRD(readPixel, bounds, mbx, mby, recCb, stride, quant, &localLeft, &localUp, mode, true)
	crDistortion, crBits := scoreChromaPlaneRD(readPixel, bounds, mbx, mby, recCr, stride, quant, &localLeft, &localUp, mode, false)
	return rd.chromaScore(distortion+crDistortion, bitCost+cbBits+crBits)
}

func scoreChromaPlaneRD(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, rec []uint8, stride int, quant vp8Quant, left *[4]uint8, up *[4]uint8, mode uint8, cb bool) (int64, int64) {
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
			pred := subChroma8Block(pred8, bx, by)
			residual := chromaResidualBlock(readPixel, bounds, mbx*16+bx*8, mby*16+by*8, pred, cb)
			coeff := quantizeVP8Block(residual, quant.uvDC, quant.uvAC)
			bitCost += vp8BlockBitCost(vp8PlaneUV, nz+up[base+bx], coeff)
			recon := reconstructVP8Block(pred, coeff, quant.uvDC, quant.uvAC)
			distortion += scoreChroma4(readPixel, bounds, mbx*16+bx*8, mby*16+by*8, recon, cb)
			if hasNonZeroBlockCoeff(coeff) {
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

func scoreChroma4(readPixel pixelReader, bounds image.Rectangle, x int, y int, block [16]uint8, cb bool) int64 {
	var score int64
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			got := chromaSample(readPixel, bounds, x+xx*2, y+yy*2, cb)
			score += squareInt(int(got) - int(block[yy*4+xx]))
		}
	}
	return score
}

func vp8Y16ModeCost(mode uint8) int64 {
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

func vp8ChromaModeCost(mode uint8) int64 {
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

func predictLuma16(rec []uint8, stride int, mbx int, mby int, mode uint8) [256]uint8 {
	x0 := mbx * 16
	y0 := mby * 16
	var pred [256]uint8
	switch mode {
	case vp8PredVE:
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				pred[y*16+x] = rec[(y0-1)*stride+x0+x]
			}
		}
	case vp8PredHE:
		for y := 0; y < 16; y++ {
			v := rec[(y0+y)*stride+x0-1]
			for x := 0; x < 16; x++ {
				pred[y*16+x] = v
			}
		}
	case vp8PredTM:
		topLeft := int(rec[(y0-1)*stride+x0-1])
		for y := 0; y < 16; y++ {
			left := int(rec[(y0+y)*stride+x0-1])
			for x := 0; x < 16; x++ {
				top := int(rec[(y0-1)*stride+x0+x])
				pred[y*16+x] = clipUint8(left + top - topLeft)
			}
		}
	default:
		pred = filledLuma16(dcPred16(rec, stride, mbx, mby))
	}
	return pred
}

func predictChroma8(rec []uint8, stride int, mbx int, mby int, mode uint8) [64]uint8 {
	x0 := mbx * 8
	y0 := mby * 8
	var pred [64]uint8
	switch mode {
	case vp8PredVE:
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				pred[y*8+x] = rec[(y0-1)*stride+x0+x]
			}
		}
	case vp8PredHE:
		for y := 0; y < 8; y++ {
			v := rec[(y0+y)*stride+x0-1]
			for x := 0; x < 8; x++ {
				pred[y*8+x] = v
			}
		}
	case vp8PredTM:
		topLeft := int(rec[(y0-1)*stride+x0-1])
		for y := 0; y < 8; y++ {
			left := int(rec[(y0+y)*stride+x0-1])
			for x := 0; x < 8; x++ {
				top := int(rec[(y0-1)*stride+x0+x])
				pred[y*8+x] = clipUint8(left + top - topLeft)
			}
		}
	default:
		pred = filledChroma8(dcPred8(rec, stride, mbx, mby))
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

func filledLuma16(v uint8) [256]uint8 {
	var block [256]uint8
	for i := range block {
		block[i] = v
	}
	return block
}

func filledChroma8(v uint8) [64]uint8 {
	var block [64]uint8
	for i := range block {
		block[i] = v
	}
	return block
}

func subLuma16Block(pred [256]uint8, bx int, by int) [16]uint8 {
	var block [16]uint8
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			block[y*4+x] = pred[(by*4+y)*16+bx*4+x]
		}
	}
	return block
}

func subChroma8Block(pred [64]uint8, bx int, by int) [16]uint8 {
	var block [16]uint8
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			block[y*4+x] = pred[(by*4+y)*8+bx*4+x]
		}
	}
	return block
}

func squareInt(v int) int64 {
	return int64(v * v)
}

func lumaResidualBlock(readPixel pixelReader, bounds image.Rectangle, x int, y int, pred [16]uint8) [16]int {
	var residual [16]int
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			c := samplePixel(readPixel, bounds, x+xx, y+yy)
			luma := rgbToLuma(c.R, c.G, c.B)
			residual[yy*4+xx] = int(luma) - int(pred[yy*4+xx])
		}
	}
	return residual
}

func chromaResidualBlock(readPixel pixelReader, bounds image.Rectangle, x int, y int, pred [16]uint8, cb bool) [16]int {
	var residual [16]int
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			target := chromaSample(readPixel, bounds, x+xx*2, y+yy*2, cb)
			residual[yy*4+xx] = int(target) - int(pred[yy*4+xx])
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

func chromaSample(readPixel pixelReader, bounds image.Rectangle, x int, y int, cb bool) uint8 {
	return chromaSampleFiltered(readPixel, bounds, x, y, cb)
}

func chromaSampleFiltered(readPixel pixelReader, bounds image.Rectangle, x int, y int, cb bool) uint8 {
	centerSum := 0
	minValue := 256
	maxValue := -1
	for yy := 0; yy < 2; yy++ {
		for xx := 0; xx < 2; xx++ {
			value := chromaValueAt(readPixel, bounds, x+xx, y+yy, cb)
			centerSum += value
			if value < minValue {
				minValue = value
			}
			if value > maxValue {
				maxValue = value
			}
		}
	}
	if maxValue-minValue <= 16 {
		return uint8((centerSum + 2) / 4)
	}

	filterSum := 0
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			value := chromaValueAt(readPixel, bounds, x+xx-1, y+yy-1, cb)
			filterSum += chromaSampleFilterWeights[yy*4+xx] * value
		}
	}
	return uint8((filterSum + 18) / 36)
}

func chromaValueAt(readPixel pixelReader, bounds image.Rectangle, x int, y int, cb bool) int {
	c := samplePixel(readPixel, bounds, x, y)
	u, v := rgbToChroma(c.R, c.G, c.B)
	if cb {
		return int(u)
	}
	return int(v)
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

func predictLuma4(rec []uint8, stride int, x int, y int, mode uint8) [16]uint8 {
	a := luma4TopLeft(rec, stride, x, y)
	b := luma4Top(rec, stride, x, y, 0)
	c := luma4Top(rec, stride, x, y, 1)
	d := luma4Top(rec, stride, x, y, 2)
	e := luma4Top(rec, stride, x, y, 3)
	f := luma4Top(rec, stride, x, y, 4)
	g := luma4Top(rec, stride, x, y, 5)
	h := luma4Top(rec, stride, x, y, 6)
	i := luma4Top(rec, stride, x, y, 7)
	p := luma4Left(rec, stride, x, y, 0)
	q := luma4Left(rec, stride, x, y, 1)
	r := luma4Left(rec, stride, x, y, 2)
	s := luma4Left(rec, stride, x, y, 3)

	var block [16]uint8
	switch mode {
	case vp8PredTM:
		for yy := 0; yy < 4; yy++ {
			left := luma4Left(rec, stride, x, y, yy)
			for xx := 0; xx < 4; xx++ {
				block[yy*4+xx] = clipUint8(left + luma4Top(rec, stride, x, y, xx) - a)
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
		block = pred4DCBlock(rec, stride, x, y)
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
	sum := 4
	for i := 0; i < 4; i++ {
		if y == 0 {
			sum += 0x7f
		} else {
			sum += int(rec[(y-1)*stride+x+i])
		}
	}
	for j := 0; j < 4; j++ {
		if x == 0 {
			sum += 0x81
		} else {
			sum += int(rec[(y+j)*stride+x-1])
		}
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

func quantizeVP8Block(residual [16]int, dcQ int, acQ int) [16]int {
	transformed := forwardDCT4(residual)
	return quantizeTransformedVP8Block(transformed, dcQ, acQ)
}

func quantizeTransformedVP8Block(transformed [16]int, dcQ int, acQ int) [16]int {
	var coeff [16]int
	for i, v := range transformed {
		q := acQ
		if i == 0 {
			q = dcQ
		}
		coeff[i] = quantizeTransformCoeff(v, q)
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

func quantizeTransformCoeff(v int, q int) int {
	if q <= 0 {
		return 0
	}
	sign := 1
	if v < 0 {
		sign = -1
		v = -v
	}
	return sign * clipInt((v+q/2)/q, 0, 2047)
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

func reconstructVP8Block(pred [16]uint8, coeff [16]int, dcQ int, acQ int) [16]uint8 {
	return inverseDCT4(pred, dequantizeVP8Block(coeff, dcQ, acQ))
}

func dequantizeVP8Block(coeff [16]int, dcQ int, acQ int) [16]int {
	var dequant [16]int
	dequant[0] = coeff[0] * dcQ
	for i := 1; i < 16; i++ {
		dequant[i] = coeff[i] * acQ
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

func hasNonZeroBlockCoeff(coeff [16]int) bool {
	return hasNonZeroBlockCoeffFrom(coeff, 0)
}

func hasNonZeroBlockCoeffFrom(coeff [16]int, start int) bool {
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
