package webp

import (
	"image"
	"image/color"
	"math"
)

const vp8lColorTransformSearchSamples = 2048

type vp8lColorTransformSearchState struct {
	red          [nLiteralCodes]uint32
	blue         [nLiteralCodes]uint32
	coefficients [3][nLiteralCodes]uint32
}

func makeVP8LBlockColorTransformPlan(readPixel pixelReader, bounds image.Rectangle, width int, height int, base vp8lEncodingPlan, cfg vp8lEncodingConfig) (vp8lEncodingPlan, bool) {
	if base.colorIndexing || base.colorTransform || len(cfg.colorTransformBlockSizeBits) == 0 {
		return vp8lEncodingPlan{}, false
	}

	readBase := vp8lPlanPixelReader(readPixel, bounds, width, height, base)
	best := vp8lEncodingPlan{}
	bestBits := ^uint64(0)
	found := false
	for _, sizeBits := range cfg.colorTransformBlockSizeBits {
		elements, uniform, nonZero := vp8lChooseBlockColorTransformImage(readBase, bounds, width, height, sizeBits)
		if len(elements) == 0 || !nonZero {
			continue
		}
		transformWidth, transformHeight := vp8lTransformDimensions(width, height, sizeBits)
		candidate := base
		candidate.colorTransform = true
		candidate.colorSizeBits = sizeBits
		candidate.colorElement = elements[0]
		if !uniform {
			candidate.colorImage = elements
		}
		readTransformed := vp8lColorTransformReader(readBase, candidate.colorElement)
		if len(candidate.colorImage) != 0 {
			readTransformed = vp8lBlockColorTransformReader(readBase, bounds, sizeBits, candidate.colorImage, transformWidth)
		}
		candidate.analysis = analyzeImage(readTransformed, bounds)
		transformBounds := image.Rect(0, 0, transformWidth, transformHeight)
		candidate.colorAnalysis = analyzeImage(vp8lColorTransformImageReaderForPlan(candidate, transformWidth), transformBounds)
		candidateBits := vp8lPayloadBits(width, height, candidate)
		if candidateBits >= bestBits {
			continue
		}
		best = candidate
		bestBits = candidateBits
		found = true
	}
	return best, found
}

func vp8lChooseBlockColorTransformImage(readPixel pixelReader, bounds image.Rectangle, width int, height int, sizeBits uint8) ([]vp8lColorTransformElement, bool, bool) {
	transformWidth, transformHeight := vp8lTransformDimensions(width, height, sizeBits)
	if transformWidth == 0 || transformHeight == 0 {
		return nil, true, false
	}
	elements := make([]vp8lColorTransformElement, transformWidth*transformHeight)
	var state vp8lColorTransformSearchState
	uniform := true
	nonZero := false
	first := vp8lColorTransformElement{}
	for by := 0; by < transformHeight; by++ {
		for bx := 0; bx < transformWidth; bx++ {
			var left, above *vp8lColorTransformElement
			if bx > 0 {
				left = &elements[by*transformWidth+bx-1]
			}
			if by > 0 {
				above = &elements[(by-1)*transformWidth+bx]
			}
			element, red, blue := vp8lBestColorTransformForBlock(readPixel, bounds, sizeBits, bx, by, left, above, &state)
			index := by*transformWidth + bx
			elements[index] = element
			if index == 0 {
				first = element
			} else if element != first {
				uniform = false
			}
			nonZero = nonZero || element != (vp8lColorTransformElement{})
			vp8lAddColorHistogram(&state.red, &red)
			vp8lAddColorHistogram(&state.blue, &blue)
			state.coefficients[0][element.greenToRed]++
			state.coefficients[1][element.greenToBlue]++
			state.coefficients[2][element.redToBlue]++
		}
	}
	return elements, uniform, nonZero
}

func vp8lBestColorTransformForBlock(readPixel pixelReader, bounds image.Rectangle, sizeBits uint8, blockX int, blockY int, left *vp8lColorTransformElement, above *vp8lColorTransformElement, state *vp8lColorTransformSearchState) (vp8lColorTransformElement, [nLiteralCodes]uint32, [nLiteralCodes]uint32) {
	x0 := bounds.Min.X + blockX*(1<<sizeBits)
	y0 := bounds.Min.Y + blockY*(1<<sizeBits)
	x1 := minInt(x0+(1<<sizeBits), bounds.Max.X)
	y1 := minInt(y0+(1<<sizeBits), bounds.Max.Y)
	estimate := vp8lEstimateColorTransform(readPixel, x0, y0, x1, y1)

	leftRed, aboveRed := uint8(0), uint8(0)
	if left != nil {
		leftRed = left.greenToRed
	}
	if above != nil {
		aboveRed = above.greenToRed
	}
	redCandidates := vp8lColorCoefficientCandidates(estimate.greenToRed, leftRed, aboveRed)
	bestRed := redCandidates.values[0]
	bestRedScore := -math.MaxFloat64
	var bestRedHistogram [nLiteralCodes]uint32
	for i := 0; i < redCandidates.n; i++ {
		coefficient := redCandidates.values[i]
		element := vp8lColorTransformElement{greenToRed: coefficient}
		histogram := vp8lColorTransformBlockHistogram(readPixel, x0, y0, x1, y1, element, false)
		score := vp8lColorHistogramConcentration(&state.red, &histogram)
		score += vp8lEntropyTerm(state.coefficients[0][coefficient]+1) - vp8lEntropyTerm(state.coefficients[0][coefficient])
		score += vp8lColorNeighborBonus(coefficient, leftRed, aboveRed, left != nil, above != nil)
		if score > bestRedScore {
			bestRedScore = score
			bestRed = coefficient
			bestRedHistogram = histogram
		}
	}

	bestGreenToBlue := estimate.greenToBlue
	bestRedToBlue := estimate.redToBlue
	for range 2 {
		leftValue, aboveValue := uint8(0), uint8(0)
		if left != nil {
			leftValue = left.greenToBlue
		}
		if above != nil {
			aboveValue = above.greenToBlue
		}
		candidates := vp8lColorCoefficientCandidates(bestGreenToBlue, leftValue, aboveValue)
		bestScore := -math.MaxFloat64
		for i := 0; i < candidates.n; i++ {
			coefficient := candidates.values[i]
			element := vp8lColorTransformElement{greenToRed: bestRed, greenToBlue: coefficient, redToBlue: bestRedToBlue}
			histogram := vp8lColorTransformBlockHistogram(readPixel, x0, y0, x1, y1, element, true)
			score := vp8lColorHistogramConcentration(&state.blue, &histogram)
			score += vp8lEntropyTerm(state.coefficients[1][coefficient]+1) - vp8lEntropyTerm(state.coefficients[1][coefficient])
			score += vp8lColorNeighborBonus(coefficient, leftValue, aboveValue, left != nil, above != nil)
			if score > bestScore {
				bestScore = score
				bestGreenToBlue = coefficient
			}
		}

		leftValue, aboveValue = 0, 0
		if left != nil {
			leftValue = left.redToBlue
		}
		if above != nil {
			aboveValue = above.redToBlue
		}
		candidates = vp8lColorCoefficientCandidates(bestRedToBlue, leftValue, aboveValue)
		bestScore = -math.MaxFloat64
		for i := 0; i < candidates.n; i++ {
			coefficient := candidates.values[i]
			element := vp8lColorTransformElement{greenToRed: bestRed, greenToBlue: bestGreenToBlue, redToBlue: coefficient}
			histogram := vp8lColorTransformBlockHistogram(readPixel, x0, y0, x1, y1, element, true)
			score := vp8lColorHistogramConcentration(&state.blue, &histogram)
			score += vp8lEntropyTerm(state.coefficients[2][coefficient]+1) - vp8lEntropyTerm(state.coefficients[2][coefficient])
			score += vp8lColorNeighborBonus(coefficient, leftValue, aboveValue, left != nil, above != nil)
			if score > bestScore {
				bestScore = score
				bestRedToBlue = coefficient
			}
		}
	}

	element := vp8lColorTransformElement{
		greenToRed:  bestRed,
		greenToBlue: bestGreenToBlue,
		redToBlue:   bestRedToBlue,
	}
	blueHistogram := vp8lColorTransformBlockHistogram(readPixel, x0, y0, x1, y1, element, true)
	return element, bestRedHistogram, blueHistogram
}

func vp8lEstimateColorTransform(readPixel pixelReader, x0 int, y0 int, x1 int, y1 int) vp8lColorTransformElement {
	width := x1 - x0
	height := y1 - y0
	area := width * height
	step := vp8lColorTransformSampleStep(area)
	var gg, rr, gr, greenRed, greenBlue, redBlue float64
	for pos := 0; pos < area; pos += step {
		x := x0 + pos%width
		y := y0 + pos/width
		pixel := readPixel(x, y)
		g := float64(int8(pixel.G))
		r := float64(int8(pixel.R))
		b := float64(int8(pixel.B))
		gg += g * g
		rr += r * r
		gr += g * r
		greenRed += g * r
		greenBlue += g * b
		redBlue += r * b
	}
	greenToRed := vp8lColorCoefficient(32 * greenRed / nonZeroFloat(gg))
	determinant := gg*rr - gr*gr
	greenToBlue := uint8(0)
	redToBlue := uint8(0)
	if math.Abs(determinant) > 1e-9 {
		greenToBlue = vp8lColorCoefficient(32 * (greenBlue*rr - redBlue*gr) / determinant)
		redToBlue = vp8lColorCoefficient(32 * (redBlue*gg - greenBlue*gr) / determinant)
	} else if gg >= rr && gg > 0 {
		greenToBlue = vp8lColorCoefficient(32 * greenBlue / gg)
	} else if rr > 0 {
		redToBlue = vp8lColorCoefficient(32 * redBlue / rr)
	}
	return vp8lColorTransformElement{greenToRed: greenToRed, greenToBlue: greenToBlue, redToBlue: redToBlue}
}

func nonZeroFloat(value float64) float64 {
	if value == 0 {
		return 1
	}
	return value
}

func vp8lColorCoefficient(value float64) uint8 {
	coefficient := int(math.Round(value))
	if coefficient < -128 {
		coefficient = -128
	} else if coefficient > 127 {
		coefficient = 127
	}
	return uint8(int8(coefficient))
}

type vp8lColorCoefficientSet struct {
	values [12]uint8
	n      int
}

func (s *vp8lColorCoefficientSet) add(value uint8) {
	for _, existing := range s.values[:s.n] {
		if existing == value {
			return
		}
	}
	s.values[s.n] = value
	s.n++
}

func vp8lColorCoefficientCandidates(center uint8, left uint8, above uint8) vp8lColorCoefficientSet {
	var candidates vp8lColorCoefficientSet
	candidates.add(0)
	candidates.add(left)
	candidates.add(above)
	centerValue := int(int8(center))
	for _, offset := range [...]int{0, -16, 16, -8, 8, -4, 4, -32, 32} {
		value := centerValue + offset
		if value < -128 {
			value = -128
		} else if value > 127 {
			value = 127
		}
		candidates.add(uint8(int8(value)))
	}
	return candidates
}

func vp8lColorTransformBlockHistogram(readPixel pixelReader, x0 int, y0 int, x1 int, y1 int, element vp8lColorTransformElement, blue bool) [nLiteralCodes]uint32 {
	width := x1 - x0
	area := width * (y1 - y0)
	step := vp8lColorTransformSampleStep(area)
	var histogram [nLiteralCodes]uint32
	for pos := 0; pos < area; pos += step {
		x := x0 + pos%width
		y := y0 + pos/width
		transformed := applyVP8LColorTransform(readPixel(x, y), element)
		value := transformed.R
		if blue {
			value = transformed.B
		}
		histogram[value]++
	}
	return histogram
}

func vp8lColorTransformSampleStep(area int) int {
	if area <= vp8lColorTransformSearchSamples {
		return 1
	}
	return divRoundUp(area, vp8lColorTransformSearchSamples)
}

func vp8lColorHistogramConcentration(accumulated *[nLiteralCodes]uint32, block *[nLiteralCodes]uint32) float64 {
	var score float64
	for symbol, count := range block {
		if count == 0 {
			continue
		}
		previous := accumulated[symbol]
		score += vp8lEntropyTerm(previous+count) - vp8lEntropyTerm(previous)
	}
	return score
}

func vp8lColorNeighborBonus(value uint8, left uint8, above uint8, hasLeft bool, hasAbove bool) float64 {
	var bonus float64
	if hasLeft && value == left {
		bonus += 0.5
	}
	if hasAbove && value == above {
		bonus += 0.5
	}
	return bonus
}

func vp8lAddColorHistogram(accumulated *[nLiteralCodes]uint32, block *[nLiteralCodes]uint32) {
	for symbol, count := range block {
		accumulated[symbol] += count
	}
}

func vp8lBlockColorTransformReader(readPixel pixelReader, bounds image.Rectangle, sizeBits uint8, elements []vp8lColorTransformElement, transformWidth int) pixelReader {
	return func(x int, y int) color.NRGBA {
		blockX := (x - bounds.Min.X) >> sizeBits
		blockY := (y - bounds.Min.Y) >> sizeBits
		element := elements[blockY*transformWidth+blockX]
		return applyVP8LColorTransform(readPixel(x, y), element)
	}
}
