package webp

import (
	"image"
	"image/color"
)

const (
	vp8lAutoMinColorIndexPixels                      = 512 * 512
	vp8lAutoFastColorIndexMaxBitsPerPixelNumerator   = 1
	vp8lAutoFastColorIndexMaxBitsPerPixelDenominator = 4
	vp8lAutoMaxSamples                               = 2048
	vp8lAutoSampleUniqueCap                          = 257
	vp8lAutoGradientMaxAdjacentDelta                 = 48
	vp8lAutoPhotoLikeMinSampleColors                 = 128
)

type vp8lAutoLosslessReason int

const (
	vp8lAutoLosslessReasonBalanced vp8lAutoLosslessReason = iota
	vp8lAutoLosslessReasonLargeLowColor
	vp8lAutoLosslessReasonHugeImage
	vp8lAutoLosslessReasonPaletteLike
	vp8lAutoLosslessReasonFlat
	vp8lAutoLosslessReasonUILike
	vp8lAutoLosslessReasonGradientLike
	vp8lAutoLosslessReasonPhotoLike
	vp8lAutoLosslessReasonAlphaHeavy
)

func vp8lAutoLosslessMode(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int) Mode {
	mode, _ := vp8lSelectAutoLossless(m, readPixel, bounds, width, height, false)
	return mode
}

func vp8lAutoLosslessProfile(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int) (Mode, vp8lAutoLosslessReason) {
	return vp8lSelectAutoLossless(m, readPixel, bounds, width, height, true)
}

func vp8lSelectAutoLossless(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int, explain bool) (Mode, vp8lAutoLosslessReason) {
	total := width * height
	if total >= 4096*4096 {
		return ModeLowMemory, vp8lAutoLosslessReasonHugeImage
	}
	sampledLowColor := total >= vp8lAutoMinColorIndexPixels && vp8lSampleUniqueColors(readPixel, bounds, width) <= 16
	verifiedLowColor := false
	if sampledLowColor {
		source := newVP8LSource(encoderSource{image: m, bounds: bounds, width: width, height: height}, readPixel)
		alpha, table, paletteOK := vp8lStreamingSourceInfo(source)
		verifiedLowColor = paletteOK && len(table) <= 16
		if verifiedLowColor {
			directBits := vp8lStreamingLiteralPlanBits(source, alpha, nil, vp8lSourcePixelStream(source, nil), width)
			paletteBits := vp8lAutoPalettePlanBits(source, alpha, table)
			if paletteBits*4 <= directBits && vp8lAutoFastColorIndexPayloadIsSmall(paletteBits, total) {
				return ModeFast, vp8lAutoLosslessReasonLargeLowColor
			}
		}
	}
	// Diagnostic image classification does not affect the selected encoding mode.
	if !explain {
		return ModeBalanced, vp8lAutoLosslessReasonBalanced
	}
	return ModeBalanced, vp8lAutoLosslessBalancedReason(m, readPixel, bounds, width, sampledLowColor, verifiedLowColor)
}

func vp8lAutoPalettePlanBits(source vp8lSource, alpha bool, table []uint32) uint64 {
	best := ^uint64(0)
	for _, candidateTable := range vp8lPaletteOrders(table) {
		transform := vp8lStreamingPaletteTransform(candidateTable)
		stream, indexedWidth := vp8lPalettePixelStream(source, candidateTable)
		bits := vp8lStreamingLiteralPlanBits(source, alpha, []vp8lTransform{transform}, stream, indexedWidth)
		if bits < best {
			best = bits
		}
	}
	return best
}

func vp8lStreamingLiteralPlanBits(source vp8lSource, alpha bool, transforms []vp8lTransform, stream vp8lPixelStream, width int) uint64 {
	tokens := vp8lLiteralTokenStream(stream)
	group, dataBits := vp8lAnalyzeStreamingTokens(tokens)
	return newVP8LStreamingPlan(source.width, source.height, alpha, transforms, group, dataBits, tokens).payloadBitLen()
}

func vp8lAutoFastColorIndexPayloadIsSmall(colorIndexBits uint64, totalPixels int) bool {
	return colorIndexBits*vp8lAutoFastColorIndexMaxBitsPerPixelDenominator <= uint64(totalPixels)*vp8lAutoFastColorIndexMaxBitsPerPixelNumerator
}

func vp8lAutoLosslessBalancedReason(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, sampledLowColor bool, verifiedLowColor bool) vp8lAutoLosslessReason {
	if sampledLowColor && !verifiedLowColor {
		return vp8lAutoLosslessReasonBalanced
	}
	if _, ok := m.(*image.Paletted); ok {
		return vp8lAutoLosslessReasonPaletteLike
	}
	stats := vp8lAutoSampleStatsFor(readPixel, bounds, width)
	if stats.samples == 0 {
		return vp8lAutoLosslessReasonBalanced
	}
	if stats.alphaNonOpaque*2 >= stats.samples {
		return vp8lAutoLosslessReasonAlphaHeavy
	}
	if stats.unique <= 1 {
		return vp8lAutoLosslessReasonFlat
	}
	if verifiedLowColor && stats.unique <= 16 {
		return vp8lAutoLosslessReasonUILike
	}
	if stats.adjacentCount > 0 && stats.adjacentDelta <= uint64(stats.adjacentCount*vp8lAutoGradientMaxAdjacentDelta) {
		return vp8lAutoLosslessReasonGradientLike
	}
	if stats.unique >= vp8lAutoPhotoLikeMinSampleColors {
		return vp8lAutoLosslessReasonPhotoLike
	}
	return vp8lAutoLosslessReasonBalanced
}

type vp8lAutoSampleStats struct {
	samples        int
	unique         int
	alphaNonOpaque int
	adjacentDelta  uint64
	adjacentCount  int
}

func vp8lAutoSampleStatsFor(readPixel pixelReader, bounds image.Rectangle, width int) vp8lAutoSampleStats {
	total := bounds.Dx() * bounds.Dy()
	if total == 0 {
		return vp8lAutoSampleStats{}
	}
	step := 1
	if total > vp8lAutoMaxSamples {
		step = total / vp8lAutoMaxSamples
	}
	seen := make(map[color.NRGBA]struct{}, 64)
	var stats vp8lAutoSampleStats
	for pos := 0; pos < total && stats.samples < vp8lAutoMaxSamples; pos += step {
		x := bounds.Min.X + pos%width
		y := bounds.Min.Y + pos/width
		p := readPixel(x, y)
		if len(seen) < vp8lAutoSampleUniqueCap {
			seen[p] = struct{}{}
		}
		if p.A != 255 {
			stats.alphaNonOpaque++
		}
		if x+1 < bounds.Max.X {
			stats.adjacentDelta += uint64(nrgbaManhattanDistance(p, readPixel(x+1, y)))
			stats.adjacentCount++
		}
		if y+1 < bounds.Max.Y {
			stats.adjacentDelta += uint64(nrgbaManhattanDistance(p, readPixel(x, y+1)))
			stats.adjacentCount++
		}
		stats.samples++
	}
	stats.unique = len(seen)
	return stats
}

func vp8lSampleUniqueColors(readPixel pixelReader, bounds image.Rectangle, width int) int {
	total := bounds.Dx() * bounds.Dy()
	if total == 0 {
		return 0
	}
	step := 1
	if total > vp8lAutoMaxSamples {
		step = total / vp8lAutoMaxSamples
	}
	seen := make(map[color.NRGBA]struct{}, 32)
	samples := 0
	for pos := 0; pos < total && samples < vp8lAutoMaxSamples; pos += step {
		seen[vp8lPixelAt(readPixel, bounds, width, pos)] = struct{}{}
		if len(seen) > 16 {
			return len(seen)
		}
		samples++
	}
	return len(seen)
}
