package webp

import (
	"image"
	"image/color"
)

func predictLuma16(rec []uint8, stride int, mbx int, mby int, mode uint8) luma16PredBlocks {
	x0 := mbx * 16
	y0 := mby * 16
	var pred luma16PredBlocks
	switch mode {
	case vp8PredVE:
		top := vp8ReconstructionRow(rec, stride, y0-1)[x0:]
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
					v := vp8ReconstructionAt(rec, stride, x0-1, y0+by*4+y)
					for x := 0; x < 4; x++ {
						block[y*4+x] = v
					}
				}
			}
		}
	case vp8PredTM:
		topLeft := int(vp8ReconstructionAt(rec, stride, x0-1, y0-1))
		for by := 0; by < 4; by++ {
			for bx := 0; bx < 4; bx++ {
				block := &pred[by*4+bx]
				for y := 0; y < 4; y++ {
					left := int(vp8ReconstructionAt(rec, stride, x0-1, y0+by*4+y))
					for x := 0; x < 4; x++ {
						top := int(vp8ReconstructionAt(rec, stride, x0+bx*4+x, y0-1))
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
		top := vp8ReconstructionRow(rec, stride, y0-1)[x0:]
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
					v := vp8ReconstructionAt(rec, stride, x0-1, y0+by*4+y)
					for x := 0; x < 4; x++ {
						block[y*4+x] = v
					}
				}
			}
		}
	case vp8PredTM:
		topLeft := int(vp8ReconstructionAt(rec, stride, x0-1, y0-1))
		for by := 0; by < 2; by++ {
			for bx := 0; bx < 2; bx++ {
				block := &pred[by*2+bx]
				for y := 0; y < 4; y++ {
					left := int(vp8ReconstructionAt(rec, stride, x0-1, y0+by*4+y))
					for x := 0; x < 4; x++ {
						top := int(vp8ReconstructionAt(rec, stride, x0+bx*4+x, y0-1))
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
			sum += int(vp8ReconstructionAt(rec, stride, x0+x, y0-1))
		}
		return uint8(sum / 16)
	case mby == 0:
		sum := 8
		for y := 0; y < 16; y++ {
			sum += int(vp8ReconstructionAt(rec, stride, x0-1, y0+y))
		}
		return uint8(sum / 16)
	default:
		sum := 16
		for x := 0; x < 16; x++ {
			sum += int(vp8ReconstructionAt(rec, stride, x0+x, y0-1))
		}
		for y := 0; y < 16; y++ {
			sum += int(vp8ReconstructionAt(rec, stride, x0-1, y0+y))
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
			sum += int(vp8ReconstructionAt(rec, stride, x0+x, y0-1))
		}
		return uint8(sum / 8)
	case mby == 0:
		sum := 4
		for y := 0; y < 8; y++ {
			sum += int(vp8ReconstructionAt(rec, stride, x0-1, y0+y))
		}
		return uint8(sum / 8)
	default:
		sum := 8
		for x := 0; x < 8; x++ {
			sum += int(vp8ReconstructionAt(rec, stride, x0+x, y0-1))
		}
		for y := 0; y < 8; y++ {
			sum += int(vp8ReconstructionAt(rec, stride, x0-1, y0+y))
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

func vp8LumaTargetRange(target *lumaTargetBlocks) int {
	minimum := 255
	maximum := 0
	for block := range target {
		for _, value := range target[block] {
			minimum = min(minimum, int(value))
			maximum = max(maximum, int(value))
		}
	}
	return maximum - minimum
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
	countLossyCounter(lossyCounterChromaFilterSamples, 1)
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
	countLossyCounter(lossyCounterChromaFilterSamples, 1)
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
	countLossyCounter(lossyCounterRGBToYUVConversions, 1)
	return rgbToLumaValue(r, g, b)
}

func rgbToLumaValue(r uint8, g uint8, b uint8) uint8 {
	luma := 16839*int(r) + 33059*int(g) + 6420*int(b)
	return uint8((luma + 1<<15 + 16<<16) >> 16)
}

func rgbToChroma(r uint8, g uint8, b uint8) (uint8, uint8) {
	countLossyCounter(lossyCounterRGBToYUVConversions, 1)
	return rgbToChromaValue(r, g, b)
}

func rgbToChromaValue(r uint8, g uint8, b uint8) (uint8, uint8) {
	cb := (-9719*int(r) - 19081*int(g) + 28800*int(b) + 1<<15 + 128<<16) >> 16
	cr := (28800*int(r) - 24116*int(g) - 4684*int(b) + 1<<15 + 128<<16) >> 16
	return uint8(clipInt(cb, 0, 255)), uint8(clipInt(cr, 0, 255))
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
	if dx >= 4 && x&15 == 12 && y&15 != 0 {
		y = y - (y & 15)
		if y == 0 {
			return 0x7f
		}
	}
	if xx >= stride {
		xx = stride - 1
	}
	return int(vp8ReconstructionAt(rec, stride, xx, y-1))
}

func luma4Left(rec []uint8, stride int, x int, y int, dy int) int {
	if x == 0 {
		return 0x81
	}
	return int(vp8ReconstructionAt(rec, stride, x-1, y+dy))
}

func luma4TopLeft(rec []uint8, stride int, x int, y int) int {
	if y == 0 {
		return 0x7f
	}
	if x == 0 {
		return 0x81
	}
	return int(vp8ReconstructionAt(rec, stride, x-1, y-1))
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
			sum += int(vp8ReconstructionAt(rec, stride, leftX+i, topY-1))
		}
		return filledBlock4(uint8(sum / 8))
	case mby == 0:
		sum := 4
		for j := 0; j < 8; j++ {
			sum += int(vp8ReconstructionAt(rec, stride, leftX-1, topY+j))
		}
		return filledBlock4(uint8(sum / 8))
	default:
		sum := 8
		for i := 0; i < 8; i++ {
			sum += int(vp8ReconstructionAt(rec, stride, leftX+i, topY-1))
		}
		for j := 0; j < 8; j++ {
			sum += int(vp8ReconstructionAt(rec, stride, leftX-1, topY+j))
		}
		return filledBlock4(uint8(sum / 16))
	}
}

func put4(dst []uint8, stride int, x int, y int, block [16]uint8) {
	for yy := 0; yy < 4; yy++ {
		row := vp8ReconstructionRow(dst, stride, y+yy)[x:]
		copy(row[:4], block[yy*4:yy*4+4])
	}
}

func vp8ReconstructionAt(rec []uint8, stride int, x int, y int) uint8 {
	return vp8ReconstructionRow(rec, stride, y)[x]
}

func vp8ReconstructionRow(rec []uint8, stride int, y int) []uint8 {
	rows := len(rec) / stride
	return rec[(y%rows)*stride:]
}
