package webp

type vp8Reconstruction struct {
	y       []uint8
	cb      []uint8
	cr      []uint8
	yStride int
	cStride int
	mbw     int
	mbh     int
}

func reconstructVP8Frame(source vp8Source, cfg vp8LossyConfig, plan vp8FramePlan) vp8Reconstruction {
	yStride := plan.mbw * 16
	cStride := plan.mbw * 8
	ySize := yStride * plan.mbh * 16
	cSize := cStride * plan.mbh * 8
	pixels := make([]uint8, ySize+2*cSize)
	reconstruction := vp8Reconstruction{
		y:       pixels[:ySize],
		cb:      pixels[ySize : ySize+cSize],
		cr:      pixels[ySize+cSize:],
		yStride: yStride,
		cStride: cStride,
		mbw:     plan.mbw,
		mbh:     plan.mbh,
	}

	baseQuant := cfg.quant
	if cfg.trellis {
		baseQuant = baseQuant.withTrellis(&plan.tokenProbs)
		baseQuant.trellisPasses = cfg.trellisPasses
	}
	if cfg.dcDiffusion && plan.segmentation.useDCDiffusion() {
		baseQuant.dcDiffusion = newVP8DCDiffusion(plan.mbw)
	}
	upY := make([][4]uint8, plan.mbw)
	upUV := make([][4]uint8, plan.mbw)
	upY16 := make([]uint8, plan.mbw)
	for mby := 0; mby < plan.mbh; mby++ {
		var leftY [4]uint8
		var leftUV [4]uint8
		var leftY16 uint8
		for mbx := 0; mbx < plan.mbw; mbx++ {
			macroblock := mby*plan.mbw + mbx
			quant := plan.segmentation.quantForMacroblock(macroblock, baseQuant)
			mode := plan.modes[macroblock]
			processVP8LumaMB(source.readLuma, source.bounds, mbx, mby, reconstruction.y, yStride, quant, mode, &leftY, &upY[mbx], &leftY16, &upY16[mbx], nil)
			processVP8ChromaMB(source.readChroma, source.bounds, mbx, mby, reconstruction.cb, reconstruction.cr, cStride, quant, mode, &leftUV, &upUV[mbx], nil)
		}
	}
	return reconstruction
}

func (r vp8Reconstruction) clone() vp8Reconstruction {
	pixels := make([]uint8, len(r.y)+len(r.cb)+len(r.cr))
	copy(pixels, r.y)
	copy(pixels[len(r.y):], r.cb)
	copy(pixels[len(r.y)+len(r.cb):], r.cr)
	r.y = pixels[:len(r.y)]
	r.cb = pixels[len(r.y) : len(r.y)+len(r.cb)]
	r.cr = pixels[len(r.y)+len(r.cb):]
	return r
}

func applyVP8LoopFilter(reconstruction *vp8Reconstruction, filter vp8LoopFilter, segmentation *vp8Segmentation, modes []vp8MBMode, skipMap []bool) {
	if reconstruction == nil || filter.level == 0 {
		return
	}
	for mby := 0; mby < reconstruction.mbh; mby++ {
		for mbx := 0; mbx < reconstruction.mbw; mbx++ {
			macroblock := mby*reconstruction.mbw + mbx
			mode := modes[macroblock]
			level := vp8MacroblockFilterLevel(filter, segmentation, macroblock, !mode.useY16)
			if level == 0 {
				continue
			}
			interiorLimit, edgeLimit, hevThreshold := vp8FilterThresholds(level, filter.sharpness)
			filterInner := !mode.useY16 || skipMap == nil || !skipMap[macroblock]
			yOffset := mby*16*reconstruction.yStride + mbx*16
			if filter.simple {
				vp8FilterSimpleMacroblock(reconstruction.y, yOffset, reconstruction.yStride, mbx, mby, filterInner, edgeLimit)
				continue
			}
			cOffset := mby*8*reconstruction.cStride + mbx*8
			vp8FilterNormalMacroblock(reconstruction, yOffset, cOffset, mbx, mby, filterInner, interiorLimit, edgeLimit, hevThreshold)
		}
	}
}

func vp8MacroblockFilterLevel(filter vp8LoopFilter, segmentation *vp8Segmentation, macroblock int, y4 bool) int {
	level := filter.level
	if segmentation.enabled() && macroblock >= 0 && macroblock < len(segmentation.mapIDs) {
		segment := segmentation.mapIDs[macroblock]
		if segment < vp8SegmentCount {
			level = segmentation.segments[segment].filterLevel
		}
	}
	if filter.deltaEnabled {
		level += filter.refDeltas[0]
		if y4 {
			level += filter.modeDeltas[0]
		}
	}
	return clipInt(level, 0, 63)
}

func vp8FilterThresholds(level int, sharpness int) (int, int, int) {
	interiorLimit := level
	if sharpness > 0 {
		if sharpness > 4 {
			interiorLimit >>= 2
		} else {
			interiorLimit >>= 1
		}
		interiorLimit = minInt(interiorLimit, 9-sharpness)
	}
	interiorLimit = maxInt(interiorLimit, 1)
	hevThreshold := 0
	if level >= 40 {
		hevThreshold = 2
	} else if level >= 15 {
		hevThreshold = 1
	}
	return interiorLimit, 2*level + interiorLimit, hevThreshold
}

func vp8FilterSimpleMacroblock(y []uint8, offset int, stride int, mbx int, mby int, filterInner bool, edgeLimit int) {
	if mbx > 0 {
		vp8FilterSimpleEdge(y, offset, 1, stride, 16, edgeLimit+4)
	}
	if filterInner {
		for x := 4; x < 16; x += 4 {
			vp8FilterSimpleEdge(y, offset+x, 1, stride, 16, edgeLimit)
		}
	}
	if mby > 0 {
		vp8FilterSimpleEdge(y, offset, stride, 1, 16, edgeLimit+4)
	}
	if filterInner {
		for row := 4; row < 16; row += 4 {
			vp8FilterSimpleEdge(y, offset+row*stride, stride, 1, 16, edgeLimit)
		}
	}
}

func vp8FilterNormalMacroblock(reconstruction *vp8Reconstruction, yOffset int, cOffset int, mbx int, mby int, filterInner bool, interiorLimit int, edgeLimit int, hevThreshold int) {
	if mbx > 0 {
		vp8FilterNormalEdge(reconstruction.y, yOffset, 1, reconstruction.yStride, 16, edgeLimit+4, interiorLimit, hevThreshold, true)
		vp8FilterNormalEdge(reconstruction.cb, cOffset, 1, reconstruction.cStride, 8, edgeLimit+4, interiorLimit, hevThreshold, true)
		vp8FilterNormalEdge(reconstruction.cr, cOffset, 1, reconstruction.cStride, 8, edgeLimit+4, interiorLimit, hevThreshold, true)
	}
	if filterInner {
		for x := 4; x < 16; x += 4 {
			vp8FilterNormalEdge(reconstruction.y, yOffset+x, 1, reconstruction.yStride, 16, edgeLimit, interiorLimit, hevThreshold, false)
		}
		vp8FilterNormalEdge(reconstruction.cb, cOffset+4, 1, reconstruction.cStride, 8, edgeLimit, interiorLimit, hevThreshold, false)
		vp8FilterNormalEdge(reconstruction.cr, cOffset+4, 1, reconstruction.cStride, 8, edgeLimit, interiorLimit, hevThreshold, false)
	}
	if mby > 0 {
		vp8FilterNormalEdge(reconstruction.y, yOffset, reconstruction.yStride, 1, 16, edgeLimit+4, interiorLimit, hevThreshold, true)
		vp8FilterNormalEdge(reconstruction.cb, cOffset, reconstruction.cStride, 1, 8, edgeLimit+4, interiorLimit, hevThreshold, true)
		vp8FilterNormalEdge(reconstruction.cr, cOffset, reconstruction.cStride, 1, 8, edgeLimit+4, interiorLimit, hevThreshold, true)
	}
	if filterInner {
		for row := 4; row < 16; row += 4 {
			vp8FilterNormalEdge(reconstruction.y, yOffset+row*reconstruction.yStride, reconstruction.yStride, 1, 16, edgeLimit, interiorLimit, hevThreshold, false)
		}
		vp8FilterNormalEdge(reconstruction.cb, cOffset+4*reconstruction.cStride, reconstruction.cStride, 1, 8, edgeLimit, interiorLimit, hevThreshold, false)
		vp8FilterNormalEdge(reconstruction.cr, cOffset+4*reconstruction.cStride, reconstruction.cStride, 1, 8, edgeLimit, interiorLimit, hevThreshold, false)
	}
}

func vp8FilterSimpleEdge(pixels []uint8, offset int, edgeStep int, advance int, size int, threshold int) {
	threshold = 2*threshold + 1
	for i := 0; i < size; i++ {
		p := offset + i*advance
		if 4*absInt(int(pixels[p-edgeStep])-int(pixels[p]))+absInt(int(pixels[p-2*edgeStep])-int(pixels[p+edgeStep])) <= threshold {
			vp8Filter2(pixels, p, edgeStep)
		}
	}
}

func vp8FilterNormalEdge(pixels []uint8, offset int, edgeStep int, advance int, size int, threshold int, interiorLimit int, hevThreshold int, macroblockEdge bool) {
	threshold = 2*threshold + 1
	for i := 0; i < size; i++ {
		p := offset + i*advance
		if !vp8NeedsNormalFilter(pixels, p, edgeStep, threshold, interiorLimit) {
			continue
		}
		highEdgeVariance := absInt(int(pixels[p-2*edgeStep])-int(pixels[p-edgeStep])) > hevThreshold || absInt(int(pixels[p+edgeStep])-int(pixels[p])) > hevThreshold
		if highEdgeVariance {
			vp8Filter2(pixels, p, edgeStep)
		} else if macroblockEdge {
			vp8Filter6(pixels, p, edgeStep)
		} else {
			vp8Filter4(pixels, p, edgeStep)
		}
	}
}

func vp8NeedsNormalFilter(pixels []uint8, p int, step int, threshold int, interiorLimit int) bool {
	if 4*absInt(int(pixels[p-step])-int(pixels[p]))+absInt(int(pixels[p-2*step])-int(pixels[p+step])) > threshold {
		return false
	}
	return absInt(int(pixels[p-4*step])-int(pixels[p-3*step])) <= interiorLimit &&
		absInt(int(pixels[p-3*step])-int(pixels[p-2*step])) <= interiorLimit &&
		absInt(int(pixels[p-2*step])-int(pixels[p-step])) <= interiorLimit &&
		absInt(int(pixels[p+3*step])-int(pixels[p+2*step])) <= interiorLimit &&
		absInt(int(pixels[p+2*step])-int(pixels[p+step])) <= interiorLimit &&
		absInt(int(pixels[p+step])-int(pixels[p])) <= interiorLimit
}

func vp8Filter2(pixels []uint8, p int, step int) {
	p1 := int(pixels[p-2*step])
	p0 := int(pixels[p-step])
	q0 := int(pixels[p])
	q1 := int(pixels[p+step])
	a := 3*(q0-p0) + clipInt(p1-q1, -128, 127)
	a1 := clipInt((a+4)>>3, -16, 15)
	a2 := clipInt((a+3)>>3, -16, 15)
	pixels[p-step] = uint8(clipInt(p0+a2, 0, 255))
	pixels[p] = uint8(clipInt(q0-a1, 0, 255))
}

func vp8Filter4(pixels []uint8, p int, step int) {
	p1 := int(pixels[p-2*step])
	p0 := int(pixels[p-step])
	q0 := int(pixels[p])
	q1 := int(pixels[p+step])
	a := 3 * (q0 - p0)
	a1 := clipInt((a+4)>>3, -16, 15)
	a2 := clipInt((a+3)>>3, -16, 15)
	a3 := (a1 + 1) >> 1
	pixels[p-2*step] = uint8(clipInt(p1+a3, 0, 255))
	pixels[p-step] = uint8(clipInt(p0+a2, 0, 255))
	pixels[p] = uint8(clipInt(q0-a1, 0, 255))
	pixels[p+step] = uint8(clipInt(q1-a3, 0, 255))
}

func vp8Filter6(pixels []uint8, p int, step int) {
	p2 := int(pixels[p-3*step])
	p1 := int(pixels[p-2*step])
	p0 := int(pixels[p-step])
	q0 := int(pixels[p])
	q1 := int(pixels[p+step])
	q2 := int(pixels[p+2*step])
	a := clipInt(3*(q0-p0)+clipInt(p1-q1, -128, 127), -128, 127)
	a1 := (27*a + 63) >> 7
	a2 := (18*a + 63) >> 7
	a3 := (9*a + 63) >> 7
	pixels[p-3*step] = uint8(clipInt(p2+a3, 0, 255))
	pixels[p-2*step] = uint8(clipInt(p1+a2, 0, 255))
	pixels[p-step] = uint8(clipInt(p0+a1, 0, 255))
	pixels[p] = uint8(clipInt(q0-a1, 0, 255))
	pixels[p+step] = uint8(clipInt(q1-a2, 0, 255))
	pixels[p+2*step] = uint8(clipInt(q2-a3, 0, 255))
}
