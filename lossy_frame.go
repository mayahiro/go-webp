package webp

import (
	"fmt"
	"image"
	"math"
)

type vp8LossyConfig struct {
	qIndex                 int
	quant                  vp8Quant
	quantDeltas            vp8QuantDeltas
	quantBias              vp8QuantBias
	filter                 vp8LoopFilter
	filterLevelDelta       int
	disableLoopFilter      bool
	rd                     vp8RDConfig
	rdYLambdaScale         int
	rdUVLambdaScale        int
	tryY4                  bool
	trySkip                bool
	updateTokenProb        bool
	bufferResiduals        bool
	commitWinningResiduals bool
	defaultFrameIncumbent  bool
	materializeSource      bool
	maxSegments            int
	segmentStrength        int
	textureStrength        int
	rdPasses               int
	trellis                bool
	trellisPasses          int
	dcDiffusion            bool
	sharpYUV               bool
	parallelAlpha          bool
	y4SearchStride         int
	y4FlatnessLimit        int
	y4RefinementBeamWidth  int
	forceDCPrediction      bool
}

func vp8LossyConfigForModeQuality(mode Mode, quality int) vp8LossyConfig {
	return vp8LossyConfigForQIndex(mode, qualityToVP8QIndex(quality))
}

func vp8LossyConfigForQIndex(mode Mode, qIndex int) vp8LossyConfig {
	qIndex = clipInt(qIndex, 0, 127)
	quality := vp8QualityProfileForQIndex(qIndex)
	if mode == ModeFast || mode == ModeLowMemory {
		quality = vp8ConservativeQualityProfileForQIndex(qIndex)
	}
	return makeVP8LossyConfig(quality, vp8EffortProfileForModeQIndex(mode, qIndex))
}

func encodeVP8KeyFrame(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, width int, height int, qIndex int) ([]byte, error) {
	return encodeVP8KeyFrameConfig(readLuma, readChroma, bounds, width, height, vp8LossyConfigForQIndex(ModeDefault, qIndex))
}

func encodeVP8KeyFrameConfig(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, width int, height int, cfg vp8LossyConfig) ([]byte, error) {
	return encodeVP8KeyFrameSource(vp8Source{
		bounds:     bounds,
		width:      width,
		height:     height,
		readLuma:   readLuma,
		readChroma: readChroma,
	}, cfg)
}

func encodeVP8KeyFrameSource(source vp8Source, cfg vp8LossyConfig) ([]byte, error) {
	source.cancel.check()
	cfg = cfg.withAdjustedLoopFilter()
	mbw := (source.width + 15) >> 4
	mbh := (source.height + 15) >> 4
	work := newVP8EncodeBuffers(mbw, mbh)
	plan := makeVP8FramePlan(source, cfg, work)
	firstPart, residualPart, err := encodeVP8FramePartitions(source, cfg, work, plan)
	source.cancel.check()
	if err != nil {
		return nil, err
	}
	return assembleVP8KeyFrame(source.width, source.height, firstPart, residualPart), nil
}

func (cfg vp8LossyConfig) withAdjustedLoopFilter() vp8LossyConfig {
	if cfg.disableLoopFilter {
		cfg.filter = vp8LoopFilter{}
		return cfg
	}
	cfg.filter.level = clipInt(cfg.filter.level+cfg.filterLevelDelta, 0, 63)
	if cfg.filter.level == 0 {
		cfg.filter.deltaEnabled = false
		cfg.filter.refDeltas = [4]int{}
		cfg.filter.modeDeltas = [4]int{}
	}
	return cfg
}

type vp8FramePlan struct {
	mbw            int
	mbh            int
	modes          []vp8MBMode
	tokenProbs     vp8TokenProbs
	skipMap        []bool
	skipProb       uint8
	residualBuffer *vp8ResidualBuffer
	segmentation   vp8Segmentation
}

func makeVP8FramePlan(source vp8Source, cfg vp8LossyConfig, work *vp8EncodeBuffers) vp8FramePlan {
	countLossyCounter(lossyCounterFilterCandidates, 1)
	setLossyCounter(lossyCounterSelectedFilterLevel, uint64(cfg.filter.level))
	mbw := (source.width + 15) >> 4
	mbh := (source.height + 15) >> 4
	countLossyCounter(lossyCounterMacroblocks, uint64(mbw*mbh))
	segmentation := makeVP8Segmentation(source.readLuma, source.bounds, mbw, mbh, cfg)
	useResidualBuffer := cfg.bufferResiduals && (cfg.trySkip || cfg.updateTokenProb) && vp8ResidualBufferFits(mbw, mbh)
	pass := runVP8ModePass(source, cfg, work, mbw, mbh, &segmentation, nil, useResidualBuffer)
	tokenProbs, skipMap := analyzeVP8ModePassEntropy(source, cfg, work, mbw, mbh, &segmentation, pass)
	for rdPass := 1; rdPass < cfg.rdPasses && (tokenProbs != vp8DefaultTokenProbs || cfg.trellis || cfg.dcDiffusion); rdPass++ {
		source.cancel.check()
		pass = runVP8ModePass(source, cfg, work, mbw, mbh, &segmentation, &tokenProbs, useResidualBuffer)
		tokenProbs, skipMap = analyzeVP8ModePassEntropy(source, cfg, work, mbw, mbh, &segmentation, pass)
	}
	countLossySkippedMacroblocks(skipMap)
	skipProb := vp8SkipProbability(skipMap)
	return vp8FramePlan{
		mbw:            mbw,
		mbh:            mbh,
		modes:          pass.modes,
		tokenProbs:     tokenProbs,
		skipMap:        skipMap,
		skipProb:       skipProb,
		residualBuffer: pass.residualBuffer,
		segmentation:   segmentation,
	}
}

func encodeVP8FramePartitions(source vp8Source, cfg vp8LossyConfig, work *vp8EncodeBuffers, plan vp8FramePlan) ([]byte, []byte, error) {
	firstPart, residualPart, _, err := encodeVP8FramePartitionsLimit(source, cfg, work, plan, vp8FirstPartitionMax)
	return firstPart, residualPart, err
}

func encodeVP8FramePartitionsLimit(source vp8Source, cfg vp8LossyConfig, work *vp8EncodeBuffers, plan vp8FramePlan, firstPartitionLimit int) ([]byte, []byte, vp8FirstPartitionFallbackStage, error) {
	firstPart, cfg, plan, fallback, err := vp8FirstPartitionWithFallback(source, cfg, work, plan, firstPartitionLimit)
	if err != nil {
		return nil, nil, fallback, err
	}
	var residualPart []byte
	if plan.residualBuffer != nil {
		residualPart = plan.residualBuffer.encodeWithSkipMap(&plan.tokenProbs, plan.skipMap, vp8ResidualPartitionCapacity(source.width, source.height, cfg.qIndex))
	} else {
		clear(work.recY)
		residualPart = encodeVP8ResidualsConfig(source.readLuma, source.readChroma, source.bounds, source.width, source.height, plan.mbw, plan.mbh, cfg.quant, &plan.segmentation, plan.modes, work, &plan.tokenProbs, plan.skipMap)
	}
	return firstPart, residualPart, fallback, nil
}

func assembleVP8KeyFrame(width int, height int, firstPart []byte, residualPart []byte) []byte {
	frameLen := 10 + len(firstPart) + len(residualPart)
	frame := make([]byte, 0, frameLen)

	tag := uint32(len(firstPart))<<5 | 1<<4
	frame = append(frame, byte(tag), byte(tag>>8), byte(tag>>16))
	frame = append(frame, 0x9d, 0x01, 0x2a)
	frame = append(frame, byte(width), byte(width>>8), byte(height), byte(height>>8))
	frame = append(frame, firstPart...)
	frame = append(frame, residualPart...)
	return frame
}

func vp8FrameFirstPartitionBytes(frame []byte) int {
	if len(frame) < 3 {
		return 0
	}
	tag := uint32(frame[0]) | uint32(frame[1])<<8 | uint32(frame[2])<<16
	return int(tag >> 5)
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
	recY           []uint8
	recCb          []uint8
	recCr          []uint8
	top            *vp8TopBuffers
	skipMap        []bool
	residualBuffer *vp8ResidualBuffer
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
	ySize := yStride * minInt(mbh*16, 32)
	cSize := cStride * minInt(mbh*8, 16)
	rec := make([]uint8, ySize+2*cSize)
	work := &vp8EncodeBuffers{
		recY:  rec[:ySize],
		recCb: rec[ySize : ySize+cSize],
		recCr: rec[ySize+cSize:],
	}
	up := make([][4]uint8, 3*mbw)
	work.top = &vp8TopBuffers{
		modes:  make([]vp8MBMode, mbw*mbh),
		upPred: up[:mbw],
		upY:    up[mbw : 2*mbw],
		upUV:   up[2*mbw:],
		upY16:  make([]uint8, mbw),
	}
	return work
}

func (w *vp8EncodeBuffers) resetSkipMap(macroblockCount int) []bool {
	if cap(w.skipMap) < macroblockCount {
		w.skipMap = make([]bool, macroblockCount)
	} else {
		w.skipMap = w.skipMap[:macroblockCount]
		clear(w.skipMap)
	}
	return w.skipMap
}

func (w *vp8EncodeBuffers) resetResidualBuffer(macroblockCount int) *vp8ResidualBuffer {
	if w.residualBuffer == nil {
		w.residualBuffer = newVP8ResidualBuffer(macroblockCount)
	} else {
		w.residualBuffer.blocks = w.residualBuffer.blocks[:0]
		w.residualBuffer.macroblocks = w.residualBuffer.macroblocks[:0]
	}
	return w.residualBuffer
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
	bitCount := 2 + 80 + 11 + 2 + 12 + 1 + 8*8 + 4*8*3*11*9 + 1 + mbw*mbh*(2+1+16*7+3)
	capacity := (bitCount+7)/8 + 4
	if capacity > vp8FirstPartitionMax {
		return vp8FirstPartitionMax
	}
	return capacity
}

func vp8FirstPartition(mbw int, mbh int, qIndex int, quantDeltas vp8QuantDeltas, filter vp8LoopFilter, segmentation *vp8Segmentation, modes []vp8MBMode, tokenProbs vp8TokenProbs, skipMap []bool, skipProb uint8) ([]byte, error) {
	return vp8FirstPartitionWithLimit(mbw, mbh, qIndex, quantDeltas, filter, segmentation, modes, tokenProbs, skipMap, skipProb, vp8FirstPartitionMax)
}

func vp8FirstPartitionWithLimit(mbw int, mbh int, qIndex int, quantDeltas vp8QuantDeltas, filter vp8LoopFilter, segmentation *vp8Segmentation, modes []vp8MBMode, tokenProbs vp8TokenProbs, skipMap []bool, skipProb uint8, limit int) ([]byte, error) {
	enc := newVP8BoolEncoderWithCapacity(vp8FirstPartitionCapacity(mbw, mbh))
	writeVP8FirstPartitionSyntax(enc, mbw, mbh, qIndex, quantDeltas, filter, segmentation, modes, tokenProbs, skipMap, skipProb)
	data := enc.bytes()
	if len(data) > limit {
		return nil, fmt.Errorf("webp: lossy image is too large for the simple VP8 first partition")
	}
	if segmentation.enabled() {
		countLossyCounter(lossyCounterSegmentMapBits, uint64(mbw*mbh*2))
	}
	countLossyCounter(lossyCounterFirstPartitionBits, uint64(len(data))*8)
	firstPart := make([]byte, len(data))
	copy(firstPart, data)
	return firstPart, nil
}

func vp8FirstPartitionSize(mbw int, mbh int, qIndex int, quantDeltas vp8QuantDeltas, filter vp8LoopFilter, segmentation *vp8Segmentation, modes []vp8MBMode, tokenProbs vp8TokenProbs, skipMap []bool, skipProb uint8) int {
	counter := newVP8BoolCounter()
	writeVP8FirstPartitionSyntax(counter, mbw, mbh, qIndex, quantDeltas, filter, segmentation, modes, tokenProbs, skipMap, skipProb)
	return counter.size()
}

func writeVP8FirstPartitionSyntax(enc *vp8BoolEncoder, mbw int, mbh int, qIndex int, quantDeltas vp8QuantDeltas, filter vp8LoopFilter, segmentation *vp8Segmentation, modes []vp8MBMode, tokenProbs vp8TokenProbs, skipMap []bool, skipProb uint8) {
	writeVP8Literal(enc, 0, 1) // color space
	writeVP8Literal(enc, 0, 1) // pixel clamp
	writeVP8SegmentationHeader(enc, segmentation)
	enc.writeBitEqualProb(filter.simple) // loop filter type
	writeVP8Literal(enc, uint32(filter.level), 6)
	writeVP8Literal(enc, uint32(filter.sharpness), 3)
	writeVP8LoopFilterDeltas(enc, filter)
	writeVP8Literal(enc, 0, 2)              // one token partition
	writeVP8Literal(enc, uint32(qIndex), 7) // base quantizer index
	writeVP8OptionalSignedLiteral(enc, quantDeltas.y1DC, 4)
	writeVP8OptionalSignedLiteral(enc, quantDeltas.y2DC, 4)
	writeVP8OptionalSignedLiteral(enc, quantDeltas.y2AC, 4)
	writeVP8OptionalSignedLiteral(enc, quantDeltas.uvDC, 4)
	writeVP8OptionalSignedLiteral(enc, quantDeltas.uvAC, 4)
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
			macroblock := mby*mbw + mbx
			if segmentation.enabled() {
				writeVP8SegmentID(enc, segmentation, macroblock)
			}
			if skipMap != nil {
				enc.writeBit(skipProb, skipMap[macroblock])
			}
			mode := modes[macroblock]
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
	bias   vp8QuantBias

	trellisProbs  *vp8TokenProbs
	trellisPasses int
	dcDiffusion   *vp8DCDiffusion
}

type vp8QuantDeltas struct {
	y1DC int
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

var vp8AC2QuantTable = [...]int{
	8, 8, 9, 10, 12, 13, 15, 17,
	18, 20, 21, 23, 24, 26, 27, 29,
	31, 32, 34, 35, 37, 38, 40, 41,
	43, 44, 46, 48, 49, 51, 52, 54,
	55, 57, 58, 60, 62, 63, 65, 66,
	68, 69, 71, 72, 74, 75, 77, 79,
	80, 82, 83, 85, 86, 88, 89, 93,
	96, 99, 102, 105, 108, 111, 114, 117,
	120, 124, 127, 130, 133, 136, 139, 142,
	145, 148, 151, 155, 158, 161, 164, 167,
	170, 173, 176, 179, 184, 189, 193, 198,
	203, 207, 212, 217, 221, 226, 230, 235,
	240, 244, 249, 254, 258, 263, 268, 274,
	280, 286, 292, 299, 305, 311, 317, 323,
	330, 336, 342, 348, 354, 362, 370, 379,
	385, 393, 401, 409, 416, 424, 432, 440,
}

func vp8QuantForIndex(qIndex int) vp8Quant {
	return vp8QuantForIndexDeltas(qIndex, vp8QuantDeltas{})
}

func vp8QuantForIndexDeltas(qIndex int, deltas vp8QuantDeltas) vp8Quant {
	return vp8QuantForIndexDeltasBias(qIndex, deltas, vp8NeutralQuantBias())
}

func vp8QuantForIndexDeltasBias(qIndex int, deltas vp8QuantDeltas, bias vp8QuantBias) vp8Quant {
	qIndex = clipInt(qIndex, 0, 127)
	y1DCIndex := clipInt(qIndex+deltas.y1DC, 0, 127)
	y2DCIndex := clipInt(qIndex+deltas.y2DC, 0, 127)
	y2ACIndex := clipInt(qIndex+deltas.y2AC, 0, 127)
	uvDCIndex := clipInt(qIndex+deltas.uvDC, 0, 117)
	uvACIndex := clipInt(qIndex+deltas.uvAC, 0, 127)
	return vp8Quant{
		qIndex: qIndex,
		y1DC:   vp8DCQuantTable[y1DCIndex],
		y1AC:   vp8ACQuantTable[qIndex],
		y2DC:   maxInt(vp8DCQuantTable[y2DCIndex]*2, 8),
		y2AC:   vp8AC2QuantTable[y2ACIndex],
		uvDC:   vp8DCQuantTable[uvDCIndex],
		uvAC:   vp8ACQuantTable[uvACIndex],
		bias:   bias,
	}
}

func qualityToVP8QIndex(quality int) int {
	quality = clipInt(quality, 1, 100)
	compression := float64(quality) / 100
	linearCompression := compression * (2.0 / 3.0)
	if compression >= 0.75 {
		linearCompression = 2*compression - 1
	}
	qIndex := int(127 * (1 - math.Cbrt(linearCompression)))
	return clipInt(qIndex, 0, 127)
}

type vp8RDConfig struct {
	yLambda       int64
	uvLambda      int64
	textureLambda int64
}

func newVP8RDConfig(quant vp8Quant) vp8RDConfig {
	return newVP8RDConfigScaled(quant, 256, 256)
}

func newVP8RDConfigScaled(quant vp8Quant, yScale int, uvScale int) vp8RDConfig {
	return newVP8RDConfigScaledTexture(quant, yScale, uvScale, 0)
}

func newVP8RDConfigScaledTexture(quant vp8Quant, yScale int, uvScale int, textureStrength int) vp8RDConfig {
	yScale = maxInt(yScale, 1)
	uvScale = maxInt(uvScale, 1)
	return vp8RDConfig{
		yLambda:       max(vp8RDLambda(quant.y1AC)*int64(yScale)/256, 1),
		uvLambda:      max(vp8RDLambda(quant.uvAC)*int64(uvScale)/256, 1),
		textureLambda: int64(max(textureStrength, 0) * quant.y1AC >> 5),
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
