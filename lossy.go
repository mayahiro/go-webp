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
	filter := vp8LoopFilterForIndex(qIndex)
	work := newVP8EncodeBuffers(mbw, mbh)
	modes := analyzeVP8Modes(readPixel, bounds, mbw, mbh, quant, work)
	firstPart, err := vp8FirstPartition(mbw, mbh, qIndex, filter, modes)
	if err != nil {
		return nil, err
	}
	work.clear()
	residualPart := encodeVP8Residuals(readPixel, bounds, width, height, mbw, mbh, quant, modes, work)
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
	useY16 bool
	yMode  uint8
	cMode  uint8
}

type vp8LoopFilter struct {
	simple    bool
	level     int
	sharpness int
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
	level := 4 + qIndex/8
	if level > 24 {
		level = 24
	}
	return vp8LoopFilter{
		simple:    true,
		level:     level,
		sharpness: 0,
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

func vp8FirstPartition(mbw int, mbh int, qIndex int, filter vp8LoopFilter, modes []vp8MBMode) ([]byte, error) {
	bitCount := 2 + 1 + 11 + 2 + 12 + 1 + 4*8*3*11 + 1 + mbw*mbh*36
	size := (bitCount+7)/8 + 4
	if size > vp8FirstPartitionMax {
		return nil, fmt.Errorf("webp: lossy image is too large for the simple VP8 first partition")
	}
	enc := newVP8BoolEncoderWithCapacity(size)
	writeVP8Literal(enc, 0, 1)       // color space
	writeVP8Literal(enc, 0, 1)       // pixel clamp
	enc.writeBit(128, false)         // no segmentation
	enc.writeBit(128, filter.simple) // loop filter type
	writeVP8Literal(enc, uint32(filter.level), 6)
	writeVP8Literal(enc, uint32(filter.sharpness), 3)
	enc.writeBit(128, false)                // no loop filter delta
	writeVP8Literal(enc, 0, 2)              // one token partition
	writeVP8Literal(enc, uint32(qIndex), 7) // base quantizer index
	for i := 0; i < 5; i++ {
		enc.writeBit(128, false) // no quantizer delta update
	}
	enc.writeBit(128, false) // do not refresh last frame buffer
	writeVP8TokenProbUpdateFlags(enc)
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
				writeVP8Y4DCModes(enc, &leftPred, &upPred[mbx])
			}
			writeVP8ChromaMode(enc, mode.cMode)
		}
	}
	data := enc.bytes()
	if len(data) > size {
		return nil, fmt.Errorf("webp: lossy image is too large for the simple VP8 first partition")
	}
	firstPart := make([]byte, size)
	copy(firstPart, data)
	return firstPart, nil
}

func writeVP8TokenProbUpdateFlags(enc *vp8BoolEncoder) {
	for plane := range vp8TokenProbUpdateProb {
		for band := range vp8TokenProbUpdateProb[plane] {
			for context := range vp8TokenProbUpdateProb[plane][band] {
				for _, prob := range vp8TokenProbUpdateProb[plane][band][context] {
					enc.writeBit(prob, false)
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

func writeVP8Y4DCModes(enc *vp8BoolEncoder, left *[4]uint8, up *[4]uint8) {
	for by := 0; by < 4; by++ {
		p := left[by]
		for bx := 0; bx < 4; bx++ {
			enc.writeBit(vp8Y4DCPredProb[up[bx]][p], false)
			p = vp8PredDC
			up[bx] = vp8PredDC
		}
		left[by] = p
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

var vp8Y4DCPredProb = [4][4]uint8{
	{231, 152, 175, 56},
	{134, 72, 66, 41},
	{88, 43, 39, 56},
	{193, 60, 112, 40},
}

func writeVP8Literal(enc *vp8BoolEncoder, value uint32, n uint8) {
	for n > 0 {
		n--
		enc.writeBit(128, value&(1<<n) != 0)
	}
}

type vp8Quant struct {
	y1DC int
	y1AC int
	y2DC int
	y2AC int
	uvDC int
	uvAC int
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
	return vp8Quant{
		y1DC: vp8DCQuantTable[qIndex],
		y1AC: vp8ACQuantTable[qIndex],
		y2DC: vp8DCQuantTable[qIndex] * 2,
		y2AC: maxInt(vp8ACQuantTable[qIndex]*155/100, 8),
		uvDC: vp8DCQuantTable[clipInt(qIndex, 0, 117)],
		uvAC: vp8ACQuantTable[qIndex],
	}
}

func qualityToVP8QIndex(quality int) int {
	quality = clipInt(quality, 1, 100)
	return (100 - quality) * 127 / 99
}

func analyzeVP8Modes(readPixel pixelReader, bounds image.Rectangle, mbw int, mbh int, quant vp8Quant, work *vp8EncodeBuffers) []vp8MBMode {
	yStride := mbw * 16
	cStride := mbw * 8
	recY := work.recY
	recCb := work.recCb
	recCr := work.recCr
	modes := make([]vp8MBMode, mbw*mbh)

	for mby := 0; mby < mbh; mby++ {
		for mbx := 0; mbx < mbw; mbx++ {
			mode := chooseVP8MBMode(readPixel, bounds, mbx, mby, recY, recCb, recCr, yStride, cStride)
			modes[mby*mbw+mbx] = mode
			reconstructVP8LumaMB(readPixel, bounds, mbx, mby, recY, yStride, quant, mode)
			reconstructVP8ChromaMB(readPixel, bounds, mbx, mby, recCb, recCr, cStride, quant, mode)
		}
	}
	return modes
}

func chooseVP8MBMode(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, recCb []uint8, recCr []uint8, yStride int, cStride int) vp8MBMode {
	mode := vp8MBMode{cMode: chooseVP8ChromaMode(readPixel, bounds, mbx, mby, recCb, recCr, cStride)}
	y4Score := estimateY4Score(readPixel, bounds, mbx, mby)
	y16Mode, y16Score := chooseVP8Y16Mode(readPixel, bounds, mbx, mby, recY, yStride)
	if y16Score <= y4Score {
		mode.useY16 = true
		mode.yMode = y16Mode
	}
	return mode
}

func encodeVP8Residuals(readPixel pixelReader, bounds image.Rectangle, width int, height int, mbw int, mbh int, quant vp8Quant, modes []vp8MBMode, work *vp8EncodeBuffers) []byte {
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
			encodeVP8LumaMB(enc, readPixel, bounds, mbx, mby, recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx])
			encodeVP8ChromaMB(enc, readPixel, bounds, mbx, mby, recCb, recCr, cStride, quant, mode, &leftUV, &upUV[mbx])
		}
	}
	return enc.bytes()
}

func reconstructVP8LumaMB(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, mode vp8MBMode) {
	if mode.useY16 {
		processVP8Luma16MB(nil, readPixel, bounds, mbx, mby, recY, stride, quant, mode, nil, nil, nil, nil)
		return
	}
	processVP8Luma4MB(nil, readPixel, bounds, mbx, mby, recY, stride, quant, nil, nil)
}

func encodeVP8LumaMB(enc *vp8BoolEncoder, readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, mode vp8MBMode, left *[4]uint8, up *[4]uint8, leftY16 *uint8, upY16 *uint8) {
	if mode.useY16 {
		processVP8Luma16MB(enc, readPixel, bounds, mbx, mby, recY, stride, quant, mode, left, up, leftY16, upY16)
		return
	}
	processVP8Luma4MB(enc, readPixel, bounds, mbx, mby, recY, stride, quant, left, up)
	*leftY16 = 0
	*upY16 = 0
}

func processVP8Luma4MB(enc *vp8BoolEncoder, readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, left *[4]uint8, up *[4]uint8) {
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
			pred := pred4DCBlock(recY, stride, x, y)
			residual := lumaResidualBlock(readPixel, bounds, x, y, pred)
			coeff := quantizeVP8Block(residual, quant.y1DC, quant.y1AC)
			blockNZ := uint8(0)
			if enc != nil {
				blockNZ = encodeVP8Block(enc, vp8PlaneY1SansY2, nz+up[bx], coeff)
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

func processVP8Luma16MB(enc *vp8BoolEncoder, readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, mode vp8MBMode, left *[4]uint8, up *[4]uint8, leftY16 *uint8, upY16 *uint8) {
	var localLeft [4]uint8
	var localUp [4]uint8
	if left == nil {
		left = &localLeft
	}
	if up == nil {
		up = &localUp
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
	if enc != nil {
		y16NZ = encodeVP8Block(enc, vp8PlaneY2, *leftY16+*upY16, y2Coeff)
		*leftY16 = y16NZ
		*upY16 = y16NZ
	}
	y2Recon := inverseWHT4(dequantizeVP8Block(y2Coeff, quant.y2DC, quant.y2AC))

	for by := 0; by < 4; by++ {
		nz := left[by]
		for bx := 0; bx < 4; bx++ {
			index := by*4 + bx
			coeff := quantizeTransformedVP8Block(transformed[index], 0, quant.y1AC)
			coeff[0] = 0
			blockNZ := uint8(0)
			if enc != nil {
				blockNZ = encodeVP8BlockSkipFirst(enc, vp8PlaneY1WithY2, nz+up[bx], coeff)
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
	_ = y16NZ
}

func reconstructVP8ChromaMB(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, mode vp8MBMode) {
	processVP8ChromaPlane(nil, readPixel, bounds, mbx, mby, recCb, stride, quant, nil, nil, mode.cMode, true)
	processVP8ChromaPlane(nil, readPixel, bounds, mbx, mby, recCr, stride, quant, nil, nil, mode.cMode, false)
}

func encodeVP8ChromaMB(enc *vp8BoolEncoder, readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, mode vp8MBMode, left *[4]uint8, up *[4]uint8) {
	processVP8ChromaPlane(enc, readPixel, bounds, mbx, mby, recCb, stride, quant, left, up, mode.cMode, true)
	processVP8ChromaPlane(enc, readPixel, bounds, mbx, mby, recCr, stride, quant, left, up, mode.cMode, false)
}

func processVP8ChromaPlane(enc *vp8BoolEncoder, readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, rec []uint8, stride int, quant vp8Quant, left *[4]uint8, up *[4]uint8, mode uint8, cb bool) {
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
			if enc != nil {
				blockNZ = encodeVP8Block(enc, vp8PlaneUV, nz+up[base+bx], coeff)
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

func estimateY4Score(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int) int64 {
	var score int64
	for by := 0; by < 4; by++ {
		for bx := 0; bx < 4; bx++ {
			var values [16]uint8
			sum := 0
			for yy := 0; yy < 4; yy++ {
				for xx := 0; xx < 4; xx++ {
					c := samplePixel(readPixel, bounds, mbx*16+bx*4+xx, mby*16+by*4+yy)
					y := rgbToLuma(c.R, c.G, c.B)
					values[yy*4+xx] = y
					sum += int(y)
				}
			}
			avg := (sum + 8) / 16
			for _, v := range values {
				score += squareInt(int(v) - avg)
			}
		}
	}
	return score
}

func chooseVP8Y16Mode(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int) (uint8, int64) {
	bestMode := vp8PredDC
	bestScore := int64(1<<63 - 1)
	modes, nModes := vp8CandidatePredModes(mbx, mby)
	for i := 0; i < nModes; i++ {
		mode := modes[i]
		pred := predictLuma16(recY, stride, mbx, mby, mode)
		score := scoreLuma16(readPixel, bounds, mbx, mby, pred)
		if score < bestScore {
			bestScore = score
			bestMode = mode
		}
	}
	return bestMode, bestScore
}

func chooseVP8ChromaMode(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recCb []uint8, recCr []uint8, stride int) uint8 {
	bestMode := vp8PredDC
	bestScore := int64(1<<63 - 1)
	modes, nModes := vp8CandidatePredModes(mbx, mby)
	for i := 0; i < nModes; i++ {
		mode := modes[i]
		predCb := predictChroma8(recCb, stride, mbx, mby, mode)
		predCr := predictChroma8(recCr, stride, mbx, mby, mode)
		score := scoreChroma8(readPixel, bounds, mbx, mby, predCb, true) +
			scoreChroma8(readPixel, bounds, mbx, mby, predCr, false)
		if score < bestScore {
			bestScore = score
			bestMode = mode
		}
	}
	return bestMode
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

func scoreLuma16(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, pred [256]uint8) int64 {
	var score int64
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			c := samplePixel(readPixel, bounds, mbx*16+x, mby*16+y)
			yy := rgbToLuma(c.R, c.G, c.B)
			score += squareInt(int(yy) - int(pred[y*16+x]))
		}
	}
	return score
}

func scoreChroma8(readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, pred [64]uint8, cb bool) int64 {
	var score int64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			target := chromaSample(readPixel, bounds, mbx*16+x*2, mby*16+y*2, cb)
			score += squareInt(int(target) - int(pred[y*8+x]))
		}
	}
	return score
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

func chromaSample(readPixel pixelReader, bounds image.Rectangle, x int, y int, cb bool) uint8 {
	sum := 0
	for yy := 0; yy < 2; yy++ {
		for xx := 0; xx < 2; xx++ {
			c := samplePixel(readPixel, bounds, x+xx, y+yy)
			u, v := rgbToChroma(c.R, c.G, c.B)
			if cb {
				sum += int(u)
			} else {
				sum += int(v)
			}
		}
	}
	return uint8((sum + 2) / 4)
}

func samplePixel(readPixel pixelReader, bounds image.Rectangle, x int, y int) color.NRGBA {
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
