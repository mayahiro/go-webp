package webp

import "image"

const (
	vp8ResidualBlocksPerMacroblock      = 25
	vp8ResidualBufferBudget             = 32 << 20
	vp8ResidualBlockEstimatedBytes      = 36
	vp8ResidualMacroblockEstimatedBytes = 8
	vp8ResidualBufferBytesPerMacroblock = vp8ResidualBlocksPerMacroblock*vp8ResidualBlockEstimatedBytes + vp8ResidualMacroblockEstimatedBytes
)

type vp8ResidualSink struct {
	encoder    *vp8BoolEncoder
	probs      *vp8TokenProbs
	stats      *vp8TokenStats
	buffer     *vp8ResidualBuffer
	macroblock *vp8MacroblockResiduals
}

func (s *vp8ResidualSink) writeBlock(plane int, context uint8, coeff vp8QuantizedBlock, start int) uint8 {
	if s == nil {
		if vp8HasNonZeroCoeff(coeff, start) {
			return 1
		}
		return 0
	}
	countLossyCounter(lossyCounterResidualBlocks, 1)
	if s.buffer != nil {
		return s.buffer.appendBlock(plane, context, coeff, start)
	}
	if s.macroblock != nil {
		return s.macroblock.appendBlock(plane, context, coeff, start)
	}
	if s.stats != nil {
		return vp8RecordBlockTokensFrom(s.stats, plane, context, coeff, start)
	}
	if s.encoder != nil {
		return encodeVP8BlockFromWithProbs(s.encoder, s.probs, plane, context, coeff, start)
	}
	if vp8HasNonZeroCoeff(coeff, start) {
		return 1
	}
	return 0
}

func (s *vp8ResidualSink) finishMacroblock(nonZero bool) {
	if s != nil && s.buffer != nil {
		s.buffer.finishMacroblock(nonZero)
	}
}

type vp8ResidualBlock struct {
	coeff   vp8QuantizedBlock
	plane   uint8
	context uint8
	start   uint8
}

type vp8ResidualMacroblock struct {
	blockEnd uint32
	nonZero  bool
}

type vp8ResidualBuffer struct {
	blocks      []vp8ResidualBlock
	macroblocks []vp8ResidualMacroblock
}

type vp8MacroblockResiduals struct {
	blocks [vp8ResidualBlocksPerMacroblock]vp8ResidualBlock
	count  int
}

func (b *vp8MacroblockResiduals) appendBlock(plane int, context uint8, coeff vp8QuantizedBlock, start int) uint8 {
	b.blocks[b.count] = vp8ResidualBlock{
		coeff:   coeff,
		plane:   uint8(plane),
		context: min(context, 2),
		start:   uint8(start),
	}
	b.count++
	if vp8HasNonZeroCoeff(coeff, start) {
		return 1
	}
	return 0
}

func (b *vp8MacroblockResiduals) record(stats *vp8TokenStats) {
	for i := 0; i < b.count; i++ {
		block := &b.blocks[i]
		vp8RecordBlockTokensFrom(stats, int(block.plane), block.context, block.coeff, int(block.start))
	}
	b.count = 0
}

func (b *vp8MacroblockResiduals) commit(sink *vp8ResidualSink) {
	if sink != nil {
		for i := 0; i < b.count; i++ {
			block := &b.blocks[i]
			sink.writeBlock(int(block.plane), block.context, block.coeff, int(block.start))
		}
	}
	b.count = 0
}

func (stats *vp8TokenStats) add(other *vp8TokenStats) {
	for plane := range stats {
		for band := range stats[plane] {
			for context := range stats[plane][band] {
				for node := range stats[plane][band][context] {
					stats[plane][band][context][node].zero += other[plane][band][context][node].zero
					stats[plane][band][context][node].one += other[plane][band][context][node].one
				}
			}
		}
	}
}

func newVP8ResidualBuffer(macroblockCount int) *vp8ResidualBuffer {
	return &vp8ResidualBuffer{
		blocks:      make([]vp8ResidualBlock, 0, macroblockCount*vp8ResidualBlocksPerMacroblock),
		macroblocks: make([]vp8ResidualMacroblock, 0, macroblockCount),
	}
}

func vp8ResidualBufferFits(mbw int, mbh int) bool {
	if mbw <= 0 || mbh <= 0 {
		return false
	}
	maxMacroblocks := vp8ResidualBufferBudget / vp8ResidualBufferBytesPerMacroblock
	return mbw <= maxMacroblocks && mbh <= maxMacroblocks/mbw
}

func (b *vp8ResidualBuffer) appendBlock(plane int, context uint8, coeff vp8QuantizedBlock, start int) uint8 {
	b.blocks = append(b.blocks, vp8ResidualBlock{
		coeff:   coeff,
		plane:   uint8(plane),
		context: min(context, 2),
		start:   uint8(start),
	})
	if vp8HasNonZeroCoeff(coeff, start) {
		return 1
	}
	return 0
}

func (b *vp8ResidualBuffer) finishMacroblock(nonZero bool) {
	b.macroblocks = append(b.macroblocks, vp8ResidualMacroblock{
		blockEnd: uint32(len(b.blocks)),
		nonZero:  nonZero,
	})
}

func (b *vp8ResidualBuffer) candidateSkipMap(enabled bool) []bool {
	return b.candidateSkipMapInto(enabled, nil)
}

func (b *vp8ResidualBuffer) candidateSkipMapInto(enabled bool, skipMap []bool) []bool {
	if !enabled {
		return nil
	}
	countLossyCounter(lossyCounterSkipCandidates, uint64(len(b.macroblocks)))
	if cap(skipMap) < len(b.macroblocks) {
		skipMap = make([]bool, len(b.macroblocks))
	} else {
		skipMap = skipMap[:len(b.macroblocks)]
		clear(skipMap)
	}
	skipped := 0
	for i, macroblock := range b.macroblocks {
		if !macroblock.nonZero {
			skipMap[i] = true
			skipped++
		}
	}
	if skipped == 0 {
		return nil
	}
	return skipMap
}

func (b *vp8ResidualBuffer) chooseEntropyPlan(updateTokenProbs bool, candidateSkipMap []bool) (vp8TokenProbs, []bool) {
	noSkipStats := b.tokenStats(nil)
	noSkipProbs := chooseVP8TokenProbsConfig(&noSkipStats, updateTokenProbs)
	bestCost := b.entropyPlanBitCost(&noSkipProbs, nil)
	bestProbs := noSkipProbs
	var bestSkipMap []bool

	if candidateSkipMap != nil {
		skipStats := b.tokenStats(candidateSkipMap)
		skipProbs := chooseVP8TokenProbsConfig(&skipStats, updateTokenProbs)
		if cost := b.entropyPlanBitCost(&skipProbs, candidateSkipMap); cost < bestCost {
			bestProbs = skipProbs
			bestSkipMap = candidateSkipMap
		}
	}
	return bestProbs, bestSkipMap
}

func (b *vp8ResidualBuffer) entropyPlanBitCost(probs *vp8TokenProbs, skipMap []bool) int64 {
	cost := vp8TokenProbUpdateBitCost(probs) + b.tokenBitCost(probs, skipMap)
	if skipMap == nil {
		return cost + 256
	}
	skipProb := vp8SkipProbability(skipMap)
	cost += 9 * 256
	for _, skipped := range skipMap {
		cost += vp8BitCost(skipProb, skipped)
	}
	return cost
}

func (b *vp8ResidualBuffer) tokenBitCost(probs *vp8TokenProbs, skipMap []bool) int64 {
	var cost int64
	blockStart := 0
	for i, macroblock := range b.macroblocks {
		blockEnd := int(macroblock.blockEnd)
		if skipMap == nil || !skipMap[i] {
			for _, block := range b.blocks[blockStart:blockEnd] {
				cost += vp8BlockBitCostFromWithProbs(probs, int(block.plane), block.context, block.coeff, int(block.start))
			}
		}
		blockStart = blockEnd
	}
	return cost
}

func (b *vp8ResidualBuffer) tokenStats(skipMap []bool) vp8TokenStats {
	var stats vp8TokenStats
	blockStart := 0
	for i, macroblock := range b.macroblocks {
		blockEnd := int(macroblock.blockEnd)
		if skipMap == nil || !skipMap[i] {
			for _, block := range b.blocks[blockStart:blockEnd] {
				vp8RecordBlockTokensFrom(&stats, int(block.plane), block.context, block.coeff, int(block.start))
			}
		}
		blockStart = blockEnd
	}
	return stats
}

func (b *vp8ResidualBuffer) encodeWithSkipMap(probs *vp8TokenProbs, skipMap []bool, capacity int) []byte {
	enc := newVP8BoolEncoderWithCapacity(capacity)
	blockStart := 0
	for i, macroblock := range b.macroblocks {
		blockEnd := int(macroblock.blockEnd)
		if skipMap == nil || !skipMap[i] {
			for _, block := range b.blocks[blockStart:blockEnd] {
				encodeVP8BlockFromWithProbs(enc, probs, int(block.plane), block.context, block.coeff, int(block.start))
			}
		}
		blockStart = blockEnd
	}
	return enc.bytes()
}

func collectVP8ResidualBuffer(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, baseQuant vp8Quant, segmentation *vp8Segmentation, modes []vp8MBMode, work *vp8EncodeBuffers) *vp8ResidualBuffer {
	countLossyCounter(lossyCounterResidualCollectionPasses, 1)
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

	buffer := work.resetResidualBuffer(mbw * mbh)
	sink := vp8ResidualSink{buffer: buffer}
	for mby := 0; mby < mbh; mby++ {
		var leftY [4]uint8
		var leftUV [4]uint8
		var leftY16 uint8
		for mbx := 0; mbx < mbw; mbx++ {
			macroblock := mby*mbw + mbx
			quant := segmentation.quantForMacroblock(macroblock, baseQuant)
			mode := modes[macroblock]
			lumaNZ := processVP8LumaMB(readLuma, bounds, mbx, mby, work.recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], &sink)
			chromaNZ := processVP8ChromaMB(readChroma, bounds, mbx, mby, work.recCb, work.recCr, cStride, quant, mode, &leftUV, &upUV[mbx], &sink)
			buffer.finishMacroblock(lumaNZ || chromaNZ)
		}
	}
	return buffer
}

func collectVP8SkipAndTokenStats(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, baseQuant vp8Quant, segmentation *vp8Segmentation, modes []vp8MBMode, work *vp8EncodeBuffers) (vp8TokenStats, []bool) {
	countLossyCounter(lossyCounterSkipCandidates, uint64(mbw*mbh))
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

	var stats vp8TokenStats
	var skippedStats vp8TokenStats
	var macroblockResiduals vp8MacroblockResiduals
	sink := vp8ResidualSink{macroblock: &macroblockResiduals}
	skipMap := work.resetSkipMap(mbw * mbh)
	skipped := 0
	for mby := 0; mby < mbh; mby++ {
		var leftY [4]uint8
		var leftUV [4]uint8
		var leftY16 uint8
		for mbx := 0; mbx < mbw; mbx++ {
			macroblock := mby*mbw + mbx
			quant := segmentation.quantForMacroblock(macroblock, baseQuant)
			mode := modes[macroblock]
			lumaNZ := processVP8LumaMB(readLuma, bounds, mbx, mby, work.recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], &sink)
			chromaNZ := processVP8ChromaMB(readChroma, bounds, mbx, mby, work.recCb, work.recCr, cStride, quant, mode, &leftUV, &upUV[mbx], &sink)
			if lumaNZ || chromaNZ {
				macroblockResiduals.record(&stats)
				continue
			}
			macroblockResiduals.record(&skippedStats)
			skipMap[macroblock] = true
			skipped++
		}
	}
	if !vp8ShouldUseMacroblockSkip(len(skipMap), skipped) {
		stats.add(&skippedStats)
		return stats, nil
	}
	return stats, skipMap
}
