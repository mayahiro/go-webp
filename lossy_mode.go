package webp

import (
	"image"
	"math"
)

func analyzeVP8Modes(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, quant vp8Quant, work *vp8EncodeBuffers) []vp8MBMode {
	return analyzeVP8ModesConfig(readLuma, readChroma, bounds, mbw, mbh, vp8LossyConfigForQIndex(ModeDefault, quant.qIndex), work)
}

func analyzeVP8ModesConfig(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, cfg vp8LossyConfig, work *vp8EncodeBuffers) []vp8MBMode {
	return analyzeVP8ModesConfigWithSegmentation(readLuma, readChroma, bounds, mbw, mbh, cfg, work, nil)
}

func analyzeVP8ModesConfigWithSegmentation(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, cfg vp8LossyConfig, work *vp8EncodeBuffers, segmentation *vp8Segmentation) []vp8MBMode {
	return analyzeVP8ModesConfigWithSink(readLuma, readChroma, bounds, mbw, mbh, cfg, work, segmentation, nil, nil)
}

func analyzeVP8ModesConfigWithSink(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, cfg vp8LossyConfig, work *vp8EncodeBuffers, segmentation *vp8Segmentation, tokenProbs *vp8TokenProbs, sink *vp8ResidualSink) []vp8MBMode {
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
	baseQuant := cfg.quant
	if cfg.trellis {
		baseQuant = baseQuant.withTrellis(tokenProbs)
		baseQuant.trellisPasses = cfg.trellisPasses
	}
	for mby := 0; mby < mbh; mby++ {
		var leftPred [4]uint8
		var leftY [4]uint8
		var leftUV [4]uint8
		var leftY16 uint8
		for mbx := 0; mbx < mbw; mbx++ {
			macroblock := mby*mbw + mbx
			quant := segmentation.quantForMacroblock(macroblock, baseQuant)
			rd := segmentation.rdForMacroblock(macroblock, cfg.rd)
			lumaTarget := makeLumaTargetMB(readLuma, bounds, mbx, mby)
			lumaBlocks := &lumaTarget.blocks
			chromaTarget := makeChromaTargetMB(readChroma, bounds, mbx, mby)
			chromaMode := uint8(vp8PredDC)
			if !cfg.forceDCPrediction {
				chromaMode = chooseVP8ChromaModeFromTargetWithProbs(&chromaTarget, mbx, mby, recCb, recCr, cStride, quant, rd, tokenProbs, &leftUV, &upUV[mbx])
			}
			mode := vp8MBMode{cMode: chromaMode}
			savedLeftPred := leftPred
			savedUpPred := upPred[mbx]
			savedLeftY := leftY
			savedUpY := upY[mbx]
			savedLeftY16 := leftY16
			savedUpY16 := upY16[mbx]

			y16Mode := uint8(vp8PredDC)
			y16Score := int64(0)
			if !cfg.forceDCPrediction {
				y16Mode, y16Score = chooseVP8Y16ModeWithProbs(lumaBlocks, mbx, mby, recY, yStride, quant, rd, tokenProbs, &leftY, &upY[mbx], &leftY16, &upY16[mbx])
			}
			tryY4 := cfg.tryY4 && (cfg.y4SearchStride <= 1 || macroblock%cfg.y4SearchStride == 0)
			if tryY4 && cfg.y4FlatnessLimit > 0 && vp8LumaTargetRange(lumaBlocks) < cfg.y4FlatnessLimit {
				tryY4 = false
			}
			if tryY4 {
				var y4Residuals vp8MacroblockResiduals
				var y4ResidualTarget *vp8MacroblockResiduals
				if sink != nil {
					y4ResidualTarget = &y4Residuals
				}
				y4Score := int64(0)
				y4NZ := false
				if cfg.y4RefinementBeamWidth > 1 && tokenProbs != nil {
					y4Score, y4NZ = chooseVP8Y4ModesBeam(lumaBlocks, mbx, mby, recY, yStride, quant, rd, tokenProbs, y4ResidualTarget, &leftPred, &upPred[mbx], &leftY, &upY[mbx], &mode, cfg.y4RefinementBeamWidth)
				} else {
					y4Score, y4NZ = chooseVP8Y4Modes(lumaBlocks, mbx, mby, recY, yStride, quant, rd, tokenProbs, y4ResidualTarget, &leftPred, &upPred[mbx], &leftY, &upY[mbx], &mode)
				}
				if y16Score > y4Score {
					countLossyCounter(lossyCounterY4MacroblocksSelected, 1)
					y4Residuals.commit(sink)
					chromaNZ := processVP8ChromaTargetMB(&chromaTarget, mbx, mby, recCb, recCr, cStride, quant, mode, &leftUV, &upUV[mbx], sink)
					sink.finishMacroblock(y4NZ || chromaNZ)
					modes[macroblock] = mode
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
			lumaNZ := processVP8Luma16MB(readLuma, bounds, mbx, mby, recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], sink)
			chromaNZ := processVP8ChromaTargetMB(&chromaTarget, mbx, mby, recCb, recCr, cStride, quant, mode, &leftUV, &upUV[mbx], sink)
			sink.finishMacroblock(lumaNZ || chromaNZ)
			modes[macroblock] = mode
		}
	}
	return modes
}

func collectVP8TokenStats(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, quant vp8Quant, modes []vp8MBMode, work *vp8EncodeBuffers) vp8TokenStats {
	return collectVP8TokenStatsConfig(readLuma, readChroma, bounds, mbw, mbh, quant, nil, modes, work, nil)
}

func collectVP8TokenStatsConfig(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, baseQuant vp8Quant, segmentation *vp8Segmentation, modes []vp8MBMode, work *vp8EncodeBuffers, skipMap []bool) vp8TokenStats {
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
			macroblock := mby*mbw + mbx
			quant := segmentation.quantForMacroblock(macroblock, baseQuant)
			mode := modes[macroblock]
			if skipMap != nil && skipMap[macroblock] {
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

func analyzeVP8MacroblockSkips(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, mbw int, mbh int, baseQuant vp8Quant, segmentation *vp8Segmentation, modes []vp8MBMode, work *vp8EncodeBuffers) []bool {
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

	skipMap := work.resetSkipMap(mbw * mbh)
	skipCount := 0
	for mby := 0; mby < mbh; mby++ {
		var leftY [4]uint8
		var leftUV [4]uint8
		var leftY16 uint8
		for mbx := 0; mbx < mbw; mbx++ {
			macroblock := mby*mbw + mbx
			quant := segmentation.quantForMacroblock(macroblock, baseQuant)
			mode := modes[macroblock]
			lumaNZ := processVP8LumaMB(readLuma, bounds, mbx, mby, work.recY, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], nil)
			chromaNZ := processVP8ChromaMB(readChroma, bounds, mbx, mby, work.recCb, work.recCr, cStride, quant, mode, &leftUV, &upUV[mbx], nil)
			if !lumaNZ && !chromaNZ {
				skipMap[macroblock] = true
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
	return encodeVP8ResidualsConfig(readLuma, readChroma, bounds, width, height, mbw, mbh, quant, nil, modes, work, tokenProbs, nil)
}

func encodeVP8ResidualsConfig(readLuma lumaReader, readChroma chromaReader, bounds image.Rectangle, width int, height int, mbw int, mbh int, baseQuant vp8Quant, segmentation *vp8Segmentation, modes []vp8MBMode, work *vp8EncodeBuffers, tokenProbs *vp8TokenProbs, skipMap []bool) []byte {
	yStride := mbw * 16
	cStride := mbw * 8
	recY := work.recY
	recCb := work.recCb
	recCr := work.recCr

	enc := newVP8BoolEncoderWithCapacity(vp8ResidualPartitionCapacity(width, height, baseQuant.qIndex))
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
			macroblock := mby*mbw + mbx
			quant := segmentation.quantForMacroblock(macroblock, baseQuant)
			mode := modes[macroblock]
			if skipMap != nil && skipMap[macroblock] {
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
	return processVP8Luma4MB(readLuma, bounds, mbx, mby, recY, stride, quant, left, up, mode, sink)
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
			context := nz + up[bx]
			coeff := quant.quantizeY1(residual, vp8PlaneY1SansY2, context)
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

	y2Context := *leftY16 + *upY16
	y2Coeff := quant.quantizeY2(forwardWHT4(y2Input), y2Context)
	y16NZ := sink.writeBlock(vp8PlaneY2, y2Context, y2Coeff, 0)
	hasNZ := y16NZ != 0
	*leftY16 = y16NZ
	*upY16 = y16NZ
	y2Recon := inverseWHT4(dequantizeVP8Block(y2Coeff, quant.y2DC, quant.y2AC))

	for by := 0; by < 4; by++ {
		nz := left[by]
		for bx := 0; bx < 4; bx++ {
			index := by*4 + bx
			context := nz + up[bx]
			coeff := quant.quantizeY1AC(transformed[index], context)
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
	var diffusion vp8DCDiffusionMacroblock
	if quant.dcDiffusion != nil {
		diffusion = quant.dcDiffusion.beginMacroblock(mbx, cb)
	}
	for by := 0; by < 2; by++ {
		nz := left[base+by]
		for bx := 0; bx < 2; bx++ {
			x := mbx*8 + bx*4
			y := mby*8 + by*4
			pred := pred8[by*2+bx]
			residual := chromaResidualBlockFromTarget(target, bx, by, pred)
			transformed := forwardDCT4(residual)
			if quant.dcDiffusion != nil {
				transformed[0] = diffusion.correct(by*2+bx, transformed[0], quant.uvDC)
			}
			coeff := quant.quantizeUVTransformed(transformed)
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
	diffusion.finish()
	return hasNZ
}

func chooseVP8Y4Modes(target *lumaTargetBlocks, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, tokenProbs *vp8TokenProbs, residuals *vp8MacroblockResiduals, leftPred *[4]uint8, upPred *[4]uint8, leftNZ *[4]uint8, upNZ *[4]uint8, mode *vp8MBMode) (int64, bool) {
	score := rd.lumaScore(0, vp8BitCost(145, false))
	hasNZ := false
	for by := 0; by < 4; by++ {
		p := leftPred[by]
		nz := leftNZ[by]
		for bx := 0; bx < 4; bx++ {
			countLossyCounter(lossyCounterY4BlocksConsidered, 1)
			x := mbx*16 + bx*4
			y := mby*16 + by*4
			luma := &(*target)[by*4+bx]
			blockMode, blockScore, blockNZ, recon := chooseVP8Y4ModeWithProbs(luma, x, y, recY, stride, quant, rd, tokenProbs, residuals, upPred[bx], p, nz+upNZ[bx])
			mode.y4Modes[by*4+bx] = blockMode
			put4(recY, stride, x, y, recon)
			score += blockScore
			p = blockMode
			nz = blockNZ
			hasNZ = hasNZ || blockNZ != 0
			upPred[bx] = blockMode
			upNZ[bx] = blockNZ
		}
		leftPred[by] = p
		leftNZ[by] = nz
	}
	return score, hasNZ
}

const vp8Y4MaxBeamWidth = 2

type vp8Y4BeamState struct {
	score    int64
	recon    [16 * 16]uint8
	modes    [16]uint8
	nonZero  [16]uint8
	contexts [16]uint8
	coeffs   [16]vp8QuantizedBlock
	greedy   bool
	leftPred [4]uint8
	upPred   [4]uint8
	leftNZ   [4]uint8
	upNZ     [4]uint8
}

func chooseVP8Y4ModesBeam(target *lumaTargetBlocks, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, tokenProbs *vp8TokenProbs, residuals *vp8MacroblockResiduals, leftPred *[4]uint8, upPred *[4]uint8, leftNZ *[4]uint8, upNZ *[4]uint8, mode *vp8MBMode, beamWidth int) (int64, bool) {
	beamWidth = clipInt(beamWidth, 1, vp8Y4MaxBeamWidth)
	beam := [vp8Y4MaxBeamWidth]vp8Y4BeamState{{
		score:    rd.lumaScore(0, vp8BitCost(145, false)),
		greedy:   true,
		leftPred: *leftPred,
		upPred:   *upPred,
		leftNZ:   *leftNZ,
		upNZ:     *upNZ,
	}}
	beamCount := 1
	x0 := mbx * 16
	y0 := mby * 16
	for block := 0; block < 16; block++ {
		countLossyCounter(lossyCounterY4BlocksConsidered, 1)
		by := block / 4
		bx := block % 4
		x := x0 + bx*4
		y := y0 + by*4
		luma := &(*target)[block]
		var next [vp8Y4MaxBeamWidth]vp8Y4BeamState
		nextCount := 0
		var greedyCandidate vp8Y4BeamState
		hasGreedyCandidate := false
		for parentIndex := 0; parentIndex < beamCount; parentIndex++ {
			parent := &beam[parentIndex]
			putVP8Y4BeamReconstruction(recY, stride, x0, y0, &parent.recon)
			neighbors := makeLuma4Neighbors(recY, stride, x, y)
			targetTexture := int64(0)
			if rd.textureLambda > 0 {
				targetTexture = vp8WeightedHadamard(luma)
			}
			topMode := parent.upPred[bx]
			leftMode := parent.leftPred[by]
			context := parent.leftNZ[by] + parent.upNZ[bx]
			var blockScores [vp8NumPredModes]int64
			var blockNZs [vp8NumPredModes]uint8
			var reconstructions [vp8NumPredModes][16]uint8
			var coefficients [vp8NumPredModes]vp8QuantizedBlock
			localBestMode := uint8(0)
			localBestScore := int64(1<<63 - 1)
			for candidateMode := uint8(0); candidateMode < vp8NumPredModes; candidateMode++ {
				blockScore, blockNZ, recon, coeff := scoreVP8Y4ModeCandidate(luma, &neighbors, targetTexture, quant, rd, tokenProbs, topMode, leftMode, context, candidateMode)
				blockScores[candidateMode] = blockScore
				blockNZs[candidateMode] = blockNZ
				reconstructions[candidateMode] = recon
				coefficients[candidateMode] = coeff
				if blockScore < localBestScore {
					localBestScore = blockScore
					localBestMode = candidateMode
				}
			}
			for candidateMode := uint8(0); candidateMode < vp8NumPredModes; candidateMode++ {
				candidateScore := parent.score + blockScores[candidateMode]
				isGreedy := parent.greedy && candidateMode == localBestMode
				rank := nextCount
				for i := 0; i < nextCount; i++ {
					if candidateScore < next[i].score {
						rank = i
						break
					}
				}
				if rank >= beamWidth && !isGreedy {
					continue
				}
				candidate := *parent
				candidate.score = candidateScore
				candidate.greedy = isGreedy
				candidate.modes[block] = candidateMode
				candidate.nonZero[block] = blockNZs[candidateMode]
				candidate.contexts[block] = context
				candidate.coeffs[block] = coefficients[candidateMode]
				candidate.leftPred[by] = candidateMode
				candidate.upPred[bx] = candidateMode
				candidate.leftNZ[by] = blockNZs[candidateMode]
				candidate.upNZ[bx] = blockNZs[candidateMode]
				putVP8Y4BeamBlock(&candidate.recon, bx, by, reconstructions[candidateMode])
				if candidate.greedy {
					greedyCandidate = candidate
					hasGreedyCandidate = true
				}
				if rank >= beamWidth {
					continue
				}
				if nextCount < beamWidth {
					nextCount++
				}
				for i := nextCount - 1; i > rank; i-- {
					next[i] = next[i-1]
				}
				next[rank] = candidate
			}
		}
		if hasGreedyCandidate {
			containsGreedy := false
			for i := 0; i < nextCount; i++ {
				containsGreedy = containsGreedy || next[i].greedy
			}
			if !containsGreedy {
				if nextCount < beamWidth {
					nextCount++
				}
				next[nextCount-1] = greedyCandidate
				for i := nextCount - 1; i > 0 && next[i].score < next[i-1].score; i-- {
					next[i], next[i-1] = next[i-1], next[i]
				}
			}
		}
		beam = next
		beamCount = nextCount
	}

	winner := &beam[0]
	putVP8Y4BeamReconstruction(recY, stride, x0, y0, &winner.recon)
	mode.y4Modes = winner.modes
	*leftPred = winner.leftPred
	*upPred = winner.upPred
	*leftNZ = winner.leftNZ
	*upNZ = winner.upNZ
	hasNZ := false
	for block := 0; block < 16; block++ {
		if residuals != nil {
			residuals.appendBlock(vp8PlaneY1SansY2, winner.contexts[block], winner.coeffs[block], 0)
		}
		hasNZ = hasNZ || winner.nonZero[block] != 0
	}
	return winner.score, hasNZ
}

func putVP8Y4BeamBlock(dst *[16 * 16]uint8, bx int, by int, block [16]uint8) {
	for y := 0; y < 4; y++ {
		start := (by*4+y)*16 + bx*4
		copy(dst[start:start+4], block[y*4:y*4+4])
	}
}

func putVP8Y4BeamReconstruction(dst []uint8, stride int, x int, y int, recon *[16 * 16]uint8) {
	for row := 0; row < 16; row++ {
		dstRow := vp8ReconstructionRow(dst, stride, y+row)[x:]
		copy(dstRow[:16], recon[row*16:row*16+16])
	}
}

func chooseVP8Y4Mode(target *[16]uint8, x int, y int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, topPred uint8, leftPred uint8, context uint8) (uint8, int64, uint8, [16]uint8) {
	return chooseVP8Y4ModeWithProbs(target, x, y, recY, stride, quant, rd, nil, nil, topPred, leftPred, context)
}

func chooseVP8Y4ModeWithProbs(target *[16]uint8, x int, y int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, tokenProbs *vp8TokenProbs, residuals *vp8MacroblockResiduals, topPred uint8, leftPred uint8, context uint8) (uint8, int64, uint8, [16]uint8) {
	bestMode := uint8(vp8PredDC)
	bestScore := int64(1<<63 - 1)
	bestNZ := uint8(0)
	var bestRecon [16]uint8
	var bestCoeff vp8QuantizedBlock
	neighbors := makeLuma4Neighbors(recY, stride, x, y)
	targetTexture := int64(0)
	if rd.textureLambda > 0 {
		targetTexture = vp8WeightedHadamard(target)
	}
	for mode := uint8(0); mode < vp8NumPredModes; mode++ {
		score, blockNZ, recon, coeff := scoreVP8Y4ModeCandidate(target, &neighbors, targetTexture, quant, rd, tokenProbs, topPred, leftPred, context, mode)
		if score < bestScore {
			bestScore = score
			bestMode = mode
			bestRecon = recon
			bestCoeff = coeff
			if blockNZ != 0 {
				bestNZ = 1
			} else {
				bestNZ = 0
			}
		}
	}
	if residuals != nil {
		residuals.appendBlock(vp8PlaneY1SansY2, context, bestCoeff, 0)
	}
	return bestMode, bestScore, bestNZ, bestRecon
}

func scoreVP8Y4ModeCandidate(target *[16]uint8, neighbors *luma4Neighbors, targetTexture int64, quant vp8Quant, rd vp8RDConfig, tokenProbs *vp8TokenProbs, topPred uint8, leftPred uint8, context uint8, mode uint8) (int64, uint8, [16]uint8, vp8QuantizedBlock) {
	countLossyCounter(lossyCounterY4ModesScored, 1)
	pred := predictLuma4WithNeighbors(neighbors, mode)
	residual := lumaResidualBlockFromTarget(target, pred)
	coeff := quant.quantizeY1(residual, vp8PlaneY1SansY2, context)
	recon := reconstructVP8Block(pred, coeff, quant.y1DC, quant.y1AC)
	distortion := rd.lumaDistortionWithTargetTexture(target, recon, targetTexture)
	blockBitCost, hasNZ := vp8BlockBitCostAndNonZeroWithProbsPtr(tokenProbs, vp8PlaneY1SansY2, context, &coeff)
	bitCost := vp8Y4ModeCost(topPred, leftPred, mode) + blockBitCost
	blockNZ := uint8(0)
	if hasNZ {
		blockNZ = 1
	}
	return rd.lumaScore(distortion, bitCost), blockNZ, recon, coeff
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
	return chooseVP8Y16ModeWithProbs(target, mbx, mby, recY, stride, quant, rd, nil, left, up, leftY16, upY16)
}

func chooseVP8Y16ModeWithProbs(target *lumaTargetBlocks, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, tokenProbs *vp8TokenProbs, left *[4]uint8, up *[4]uint8, leftY16 *uint8, upY16 *uint8) (uint8, int64) {
	bestMode := vp8PredDC
	bestScore := int64(1<<63 - 1)
	var targetTexture [16]int64
	if rd.textureLambda > 0 {
		for i := range targetTexture {
			targetTexture[i] = vp8WeightedHadamard(&(*target)[i])
		}
	}
	modes, nModes := vp8CandidatePredModes(mbx, mby)
	for i := 0; i < nModes; i++ {
		countLossyCounter(lossyCounterY16ModesScored, 1)
		mode := modes[i]
		score := scoreLuma16RD(target, &targetTexture, mbx, mby, recY, stride, quant, rd, tokenProbs, left, up, *leftY16+*upY16, mode)
		if score < bestScore {
			bestScore = score
			bestMode = mode
		}
	}
	return bestMode, bestScore
}

func scoreLuma16RD(target *lumaTargetBlocks, targetTexture *[16]int64, mbx int, mby int, recY []uint8, stride int, quant vp8Quant, rd vp8RDConfig, tokenProbs *vp8TokenProbs, left *[4]uint8, up *[4]uint8, y16Context uint8, mode uint8) int64 {
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

	y2Coeff := quant.quantizeY2(forwardWHT4(y2Input), y16Context)
	y2Recon := inverseWHT4(dequantizeVP8Block(y2Coeff, quant.y2DC, quant.y2AC))
	bitCost := vp8BitCost(145, true) + vp8Y16ModeCost(mode) + vp8BlockBitCostWithProbs(tokenProbs, vp8PlaneY2, y16Context, y2Coeff)
	var distortion int64
	localLeft := *left
	localUp := *up
	for by := 0; by < 4; by++ {
		nz := localLeft[by]
		for bx := 0; bx < 4; bx++ {
			index := by*4 + bx
			coeff := quant.quantizeY1AC(transformed[index], nz+localUp[bx])
			blockBitCost, blockNZ := vp8BlockBitCostFromWithProbsAndNonZeroPtr(tokenProbs, vp8PlaneY1WithY2, nz+localUp[bx], &coeff, 1)
			bitCost += blockBitCost
			reconCoeff := dequantizeVP8Block(coeff, 0, quant.y1AC)
			reconCoeff[0] = y2Recon[index]
			recon := inverseDCT4(pred16[index], reconCoeff)
			distortion += rd.lumaDistortionWithTargetTexture(&(*target)[index], recon, targetTexture[index])
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
	return chooseVP8ChromaModeFromTargetWithProbs(target, mbx, mby, recCb, recCr, stride, quant, rd, nil, left, up)
}

func chooseVP8ChromaModeFromTargetWithProbs(target *chromaTargetMB, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, rd vp8RDConfig, tokenProbs *vp8TokenProbs, left *[4]uint8, up *[4]uint8) uint8 {
	bestMode := vp8PredDC
	bestScore := int64(1<<63 - 1)
	modes, nModes := vp8CandidatePredModes(mbx, mby)
	for i := 0; i < nModes; i++ {
		countLossyCounter(lossyCounterChromaModesScored, 1)
		mode := modes[i]
		score := scoreChromaRD(target, mbx, mby, recCb, recCr, stride, quant, rd, tokenProbs, left, up, mode)
		if score < bestScore {
			bestScore = score
			bestMode = mode
		}
	}
	return bestMode
}

func scoreChromaRD(target *chromaTargetMB, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, quant vp8Quant, rd vp8RDConfig, tokenProbs *vp8TokenProbs, left *[4]uint8, up *[4]uint8, mode uint8) int64 {
	localLeft := *left
	localUp := *up
	bitCost := vp8ChromaModeCost(mode)
	distortion, cbBits := scoreChromaPlaneRD(target.cb[:], mbx, mby, recCb, stride, quant, tokenProbs, &localLeft, &localUp, mode, true)
	crDistortion, crBits := scoreChromaPlaneRD(target.cr[:], mbx, mby, recCr, stride, quant, tokenProbs, &localLeft, &localUp, mode, false)
	return rd.chromaScore(distortion+crDistortion, bitCost+cbBits+crBits)
}

func scoreChromaPlaneRD(target []uint8, mbx int, mby int, rec []uint8, stride int, quant vp8Quant, tokenProbs *vp8TokenProbs, left *[4]uint8, up *[4]uint8, mode uint8, cb bool) (int64, int64) {
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
			coeff := quant.quantizeUV(residual, nz+up[base+bx])
			blockBitCost, blockNZ := vp8BlockBitCostAndNonZeroWithProbsPtr(tokenProbs, vp8PlaneUV, nz+up[base+bx], &coeff)
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
