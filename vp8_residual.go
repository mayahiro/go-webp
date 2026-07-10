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
	encoder *vp8BoolEncoder
	probs   *vp8TokenProbs
	stats   *vp8TokenStats
	buffer  *vp8ResidualBuffer
}

func (s *vp8ResidualSink) writeBlock(plane int, context uint8, coeff [16]int, start int) uint8 {
	if s == nil {
		if vp8HasNonZeroCoeff(coeff, start) {
			return 1
		}
		return 0
	}
	if s.buffer != nil {
		return s.buffer.appendBlock(plane, context, coeff, start)
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
	coeff   [16]int16
	plane   uint8
	context uint8
	start   uint8
}

func (b vp8ResidualBlock) unpackCoeff() [16]int {
	var coeff [16]int
	for i, value := range b.coeff {
		coeff[i] = int(value)
	}
	return coeff
}

type vp8ResidualMacroblock struct {
	blockEnd uint32
	nonZero  bool
}

type vp8ResidualBuffer struct {
	blocks      []vp8ResidualBlock
	macroblocks []vp8ResidualMacroblock
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

func (b *vp8ResidualBuffer) appendBlock(plane int, context uint8, coeff [16]int, start int) uint8 {
	block := vp8ResidualBlock{
		plane:   uint8(plane),
		context: min(context, 2),
		start:   uint8(start),
	}
	// Quantization clamps VP8 coefficient levels to [-2047, 2047].
	for i, value := range coeff {
		block.coeff[i] = int16(value)
	}
	b.blocks = append(b.blocks, block)
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

func (b *vp8ResidualBuffer) skipMap(enabled bool) []bool {
	if !enabled {
		return nil
	}
	skipMap := make([]bool, len(b.macroblocks))
	skipped := 0
	for i, macroblock := range b.macroblocks {
		if !macroblock.nonZero {
			skipMap[i] = true
			skipped++
		}
	}
	if !vp8ShouldUseMacroblockSkip(len(skipMap), skipped) {
		return nil
	}
	return skipMap
}

func (b *vp8ResidualBuffer) tokenStats(skipMap []bool) vp8TokenStats {
	var stats vp8TokenStats
	blockStart := 0
	for i, macroblock := range b.macroblocks {
		blockEnd := int(macroblock.blockEnd)
		if skipMap == nil || !skipMap[i] {
			for _, block := range b.blocks[blockStart:blockEnd] {
				coeff := block.unpackCoeff()
				vp8RecordBlockTokensFrom(&stats, int(block.plane), block.context, coeff, int(block.start))
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
				coeff := block.unpackCoeff()
				encodeVP8BlockFromWithProbs(enc, probs, int(block.plane), block.context, coeff, int(block.start))
			}
		}
		blockStart = blockEnd
	}
	return enc.bytes()
}

func collectVP8ResidualBuffer(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, quant vp8Quant, modes []vp8MBMode, work *vp8EncodeBuffers) *vp8ResidualBuffer {
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

	buffer := newVP8ResidualBuffer(mbw * mbh)
	sink := vp8ResidualSink{buffer: buffer}
	for mby := 0; mby < mbh; mby++ {
		var leftY [4]uint8
		var leftUV [4]uint8
		var leftY16 uint8
		for mbx := 0; mbx < mbw; mbx++ {
			mode := modes[mby*mbw+mbx]
			lumaNZ := processVP8LumaMB(readLuma, bounds, mbx, mby, work.recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], &sink)
			chromaNZ := processVP8ChromaMB(readChroma, bounds, mbx, mby, work.recCb, work.recCr, cStride, quant, mode, &leftUV, &upUV[mbx], &sink)
			buffer.finishMacroblock(lumaNZ || chromaNZ)
		}
	}
	return buffer
}
