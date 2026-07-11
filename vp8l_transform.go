package webp

import (
	"math"
	"sort"
)

type vp8lTransformKind uint8

const (
	vp8lTransformPredictor     vp8lTransformKind = 0
	vp8lTransformColor         vp8lTransformKind = 1
	vp8lTransformSubtractGreen vp8lTransformKind = 2
	vp8lTransformColorIndex    vp8lTransformKind = 3
)

type vp8lTransform struct {
	kind        vp8lTransformKind
	sizeBits    uint8
	paletteSize int
	image       vp8lImagePlan
}

type vp8lTransformCandidate struct {
	width       int
	height      int
	pixels      []uint32
	transforms  []vp8lTransform
	materialize func() []uint32
}

type vp8lColorTransformElement struct {
	greenToRed  uint8
	greenToBlue uint8
	redToBlue   uint8
}

func (t *vp8lTransform) writeTo(bits *vp8lBitSink) {
	bits.writeBits(1, 1)
	bits.writeBits(uint32(t.kind), 2)
	switch t.kind {
	case vp8lTransformPredictor, vp8lTransformColor:
		bits.writeBits(uint32(t.sizeBits-2), 3)
		t.image.writeTo(bits, false)
	case vp8lTransformSubtractGreen:
	case vp8lTransformColorIndex:
		bits.writeBits(uint32(t.paletteSize-1), 8)
		t.image.writeTo(bits, false)
	}
}

func vp8lForEachTransformCandidate(source []uint32, width int, height int, budget vp8lBudget, visit func(vp8lTransformCandidate)) {
	vp8lForEachTransformCandidateWorkspace(source, width, height, budget, nil, visit)
}

func vp8lForEachTransformCandidateWorkspace(source []uint32, width int, height int, budget vp8lBudget, workspace *vp8lTransformWorkspace, visit func(vp8lTransformCandidate)) {
	if budget.trySubtractGreen {
		pixels := vp8lSubtractGreenPlaneWorkspace(source, workspace, 0)
		materializeSubtract := func() []uint32 { return vp8lSubtractGreenPlane(source) }
		transform := vp8lTransform{kind: vp8lTransformSubtractGreen}
		visit(vp8lTransformCandidate{
			width:       width,
			height:      height,
			pixels:      pixels,
			transforms:  []vp8lTransform{transform},
			materialize: materializeSubtract,
		})
		if budget.tryCombined {
			vp8lForEachPredictorCandidate(pixels, width, height, budget, []vp8lTransform{transform}, materializeSubtract, workspace, 1, 2, visit)
		}
	}

	vp8lForEachPredictorCandidate(source, width, height, budget, nil, func() []uint32 { return source }, workspace, 0, 1, visit)

	if budget.tryColor {
		vp8lForEachColorTransformCandidate(source, width, height, budget, nil, func() []uint32 { return source }, workspace, 0, visit)
	}
	if budget.tryPalette {
		vp8lForEachPaletteCandidate(source, width, height, budget, workspace, visit)
	}
}

func vp8lVisitCombinedTransformCandidates(candidate vp8lTransformCandidate, budget vp8lBudget, workspace *vp8lTransformWorkspace, slot int, visit func(vp8lTransformCandidate)) {
	if !budget.tryCombined {
		return
	}
	if budget.trySubtractGreen && !vp8lTransformsContain(candidate.transforms, vp8lTransformSubtractGreen) {
		transforms := append(append([]vp8lTransform(nil), candidate.transforms...), vp8lTransform{kind: vp8lTransformSubtractGreen})
		materialize := candidate.materialize
		visit(vp8lTransformCandidate{
			width:      candidate.width,
			height:     candidate.height,
			pixels:     vp8lSubtractGreenPlaneWorkspace(candidate.pixels, workspace, slot),
			transforms: transforms,
			materialize: func() []uint32 {
				return vp8lSubtractGreenPlane(materialize())
			},
		})
	}
	if budget.tryColor {
		vp8lForEachColorTransformCandidate(candidate.pixels, candidate.width, candidate.height, budget, candidate.transforms, candidate.materialize, workspace, slot, visit)
	}
}

func vp8lUniformPredictorImage(width int, height int, sizeBits uint8, mode uint8) ([]uint8, int, int) {
	transformWidth, transformHeight := vp8lTransformDimensions(width, height, sizeBits)
	modes := make([]uint8, transformWidth*transformHeight)
	for i := range modes {
		modes[i] = mode
	}
	return modes, transformWidth, transformHeight
}

func vp8lPredictorTransform(sizeBits uint8, modes []uint8, width int, height int) vp8lTransform {
	pixels := make([]uint32, len(modes))
	for i, mode := range modes {
		pixels[i] = 0xff000000 | uint32(mode)<<8
	}
	return vp8lTransform{
		kind:     vp8lTransformPredictor,
		sizeBits: sizeBits,
		image:    buildVP8LLiteralImagePlan(pixels, width, height),
	}
}

func vp8lChooseBlockPredictors(source []uint32, width int, height int, sizeBits uint8, candidates []uint8) ([]uint8, int, int) {
	transformWidth, transformHeight := vp8lTransformDimensions(width, height, sizeBits)
	modes := make([]uint8, transformWidth*transformHeight)
	for blockY := range transformHeight {
		for blockX := range transformWidth {
			bestMode := candidates[0]
			bestScore := math.Inf(1)
			for _, mode := range candidates {
				counts := vp8lPredictorBlockCounts(source, width, height, sizeBits, blockX, blockY, mode)
				score := vp8lPredictorLocalCost(&counts)
				if blockX > 0 && modes[blockY*transformWidth+blockX-1] == mode {
					score -= 4
				}
				if blockY > 0 && modes[(blockY-1)*transformWidth+blockX] == mode {
					score -= 4
				}
				if score < bestScore {
					bestScore = score
					bestMode = mode
				}
			}
			modes[blockY*transformWidth+blockX] = bestMode
		}
	}
	return modes, transformWidth, transformHeight
}

func vp8lPredictorBlockCounts(source []uint32, width int, height int, sizeBits uint8, blockX int, blockY int, mode uint8) [4][nLiteralCodes]uint32 {
	x0 := blockX << sizeBits
	y0 := blockY << sizeBits
	x1 := minInt(x0+1<<sizeBits, width)
	y1 := minInt(y0+1<<sizeBits, height)
	var counts [4][256]uint32
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			residual := vp8lSubPixels(source[y*width+x], vp8lPredictPixel(source, width, height, x, y, mode))
			counts[0][uint8(residual>>8)]++
			counts[1][uint8(residual>>16)]++
			counts[2][uint8(residual)]++
			counts[3][uint8(residual>>24)]++
		}
	}
	return counts
}

func vp8lPredictorLocalCost(counts *[4][nLiteralCodes]uint32) float64 {
	var score float64
	for channel := range counts {
		score += vp8lHistogramEntropy(counts[channel][:])
	}
	return score
}

func vp8lHistogramEntropy(counts []uint32) float64 {
	var total uint64
	for _, count := range counts {
		total += uint64(count)
	}
	if total < 2 {
		return 0
	}
	score := float64(total) * math.Log2(float64(total))
	for _, count := range counts {
		if count > 1 {
			score -= float64(count) * math.Log2(float64(count))
		}
	}
	return score
}

func vp8lApplyPredictor(source []uint32, width int, height int, sizeBits uint8, modes []uint8, transformWidth int) []uint32 {
	result := make([]uint32, len(source))
	vp8lApplyPredictorInto(result, source, width, height, sizeBits, modes, transformWidth)
	return result
}

func vp8lApplyPredictorWorkspace(source []uint32, width int, height int, sizeBits uint8, modes []uint8, transformWidth int, workspace *vp8lTransformWorkspace, slot int) []uint32 {
	result := workspace.pixels(slot, len(source))
	vp8lApplyPredictorInto(result, source, width, height, sizeBits, modes, transformWidth)
	return result
}

func vp8lApplyPredictorInto(result []uint32, source []uint32, width int, height int, sizeBits uint8, modes []uint8, transformWidth int) {
	for y := range height {
		for x := range width {
			mode := modes[(y>>sizeBits)*transformWidth+(x>>sizeBits)]
			result[y*width+x] = vp8lSubPixels(source[y*width+x], vp8lPredictPixel(source, width, height, x, y, mode))
		}
	}
}

func vp8lPredictPixel(source []uint32, width int, height int, x int, y int, mode uint8) uint32 {
	if x == 0 && y == 0 {
		return 0xff000000
	}
	if y == 0 {
		return source[y*width+x-1]
	}
	if x == 0 {
		return source[(y-1)*width+x]
	}
	left := vp8lUnpackPixel(source[y*width+x-1])
	top := vp8lUnpackPixel(source[(y-1)*width+x])
	topLeft := vp8lUnpackPixel(source[(y-1)*width+x-1])
	topRightIndex := (y-1)*width + x + 1
	if x == width-1 {
		topRightIndex = y * width
	}
	topRight := vp8lUnpackPixel(source[topRightIndex])
	return vp8lPackPixel(vp8lPredictorFromNeighbors(mode, left, top, topRight, topLeft))
}

func vp8lSubtractGreenPlane(source []uint32) []uint32 {
	result := make([]uint32, len(source))
	vp8lSubtractGreenPlaneInto(result, source)
	return result
}

func vp8lSubtractGreenPlaneWorkspace(source []uint32, workspace *vp8lTransformWorkspace, slot int) []uint32 {
	result := workspace.pixels(slot, len(source))
	vp8lSubtractGreenPlaneInto(result, source)
	return result
}

func vp8lSubtractGreenPlaneInto(result []uint32, source []uint32) {
	for i, pixel := range source {
		green := uint8(pixel >> 8)
		result[i] = uint32(uint8(pixel>>24))<<24 |
			uint32(uint8(pixel>>16)-green)<<16 |
			uint32(green)<<8 |
			uint32(uint8(pixel)-green)
	}
}

func vp8lSubPixels(a uint32, b uint32) uint32 {
	return uint32(uint8(a>>24)-uint8(b>>24))<<24 |
		uint32(uint8(a>>16)-uint8(b>>16))<<16 |
		uint32(uint8(a>>8)-uint8(b>>8))<<8 |
		uint32(uint8(a)-uint8(b))
}

func vp8lForEachColorTransformCandidate(source []uint32, width int, height int, budget vp8lBudget, prefix []vp8lTransform, materializeSource func() []uint32, workspace *vp8lTransformWorkspace, slot int, visit func(vp8lTransformCandidate)) {
	for _, element := range vp8lColorTransformCandidates(source, budget) {
		pixels := vp8lApplyColorTransformWorkspace(source, element, workspace, slot)
		transformWidth, transformHeight := vp8lTransformDimensions(width, height, 9)
		transformPixels := make([]uint32, transformWidth*transformHeight)
		encodedElement := 0xff000000 | uint32(element.redToBlue)<<16 | uint32(element.greenToBlue)<<8 | uint32(element.greenToRed)
		for i := range transformPixels {
			transformPixels[i] = encodedElement
		}
		transform := vp8lTransform{
			kind:     vp8lTransformColor,
			sizeBits: 9,
			image:    buildVP8LLiteralImagePlan(transformPixels, transformWidth, transformHeight),
		}
		transforms := append(append([]vp8lTransform(nil), prefix...), transform)
		materializeColor := func() []uint32 {
			return vp8lApplyColorTransform(materializeSource(), element)
		}
		visit(vp8lTransformCandidate{
			width:       width,
			height:      height,
			pixels:      pixels,
			transforms:  transforms,
			materialize: materializeColor,
		})
	}
}

func vp8lColorTransformCandidates(source []uint32, budget vp8lBudget) []vp8lColorTransformElement {
	estimate := vp8lEstimateColorTransform(source)
	values := []vp8lColorTransformElement{
		estimate,
		{greenToRed: estimate.greenToRed},
		{greenToBlue: estimate.greenToBlue},
		{redToBlue: estimate.redToBlue},
		{greenToRed: 32},
		{greenToBlue: 32},
		{redToBlue: 32},
		{greenToRed: 32, greenToBlue: 32},
	}
	if len(budget.predictorModes) == 14 {
		for _, delta := range []int{-16, 16} {
			values = append(values, vp8lColorTransformElement{
				greenToRed:  uint8(int8(int(int8(estimate.greenToRed)) + delta)),
				greenToBlue: uint8(int8(int(int8(estimate.greenToBlue)) + delta)),
				redToBlue:   uint8(int8(int(int8(estimate.redToBlue)) + delta)),
			})
		}
	}
	result := make([]vp8lColorTransformElement, 0, len(values))
	for _, value := range values {
		if value == (vp8lColorTransformElement{}) {
			continue
		}
		duplicate := false
		for _, existing := range result {
			if existing == value {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result = append(result, value)
		}
	}
	return result
}

func vp8lEstimateColorTransform(source []uint32) vp8lColorTransformElement {
	step := 1
	if len(source) > 2048 {
		step = len(source) / 2048
	}
	var gg, rr, gr, greenRed, greenBlue, redBlue float64
	for i := 0; i < len(source); i += step {
		pixel := source[i]
		green := float64(int8(pixel >> 8))
		red := float64(int8(pixel >> 16))
		blue := float64(int8(pixel))
		gg += green * green
		rr += red * red
		gr += green * red
		greenRed += green * red
		greenBlue += green * blue
		redBlue += red * blue
	}
	greenToRed := vp8lColorCoefficient(32 * greenRed / vp8lNonZeroFloat(gg))
	determinant := gg*rr - gr*gr
	var greenToBlue, redToBlue uint8
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

func vp8lColorCoefficient(value float64) uint8 {
	coefficient := int(math.Round(value))
	if coefficient < -128 {
		coefficient = -128
	} else if coefficient > 127 {
		coefficient = 127
	}
	return uint8(int8(coefficient))
}

func vp8lNonZeroFloat(value float64) float64 {
	if value == 0 {
		return 1
	}
	return value
}

func vp8lApplyColorTransform(source []uint32, element vp8lColorTransformElement) []uint32 {
	result := make([]uint32, len(source))
	vp8lApplyColorTransformInto(result, source, element)
	return result
}

func vp8lApplyColorTransformWorkspace(source []uint32, element vp8lColorTransformElement, workspace *vp8lTransformWorkspace, slot int) []uint32 {
	result := workspace.pixels(slot, len(source))
	vp8lApplyColorTransformInto(result, source, element)
	return result
}

func vp8lApplyColorTransformInto(result []uint32, source []uint32, element vp8lColorTransformElement) {
	for i, pixel := range source {
		result[i] = vp8lApplyColorElement(pixel, element)
	}
}

func vp8lColorDelta(transform uint8, channel uint8) uint8 {
	return uint8((int(int8(transform)) * int(int8(channel))) >> 5)
}

func vp8lForEachPaletteCandidate(source []uint32, width int, height int, budget vp8lBudget, workspace *vp8lTransformWorkspace, visit func(vp8lTransformCandidate)) {
	table, ok := vp8lPalette(source)
	if !ok {
		return
	}
	tables := [][]uint32{table}
	sorted := append([]uint32(nil), table...)
	sort.Slice(sorted, func(i int, j int) bool { return sorted[i] < sorted[j] })
	if !vp8lUint32SlicesEqual(table, sorted) {
		tables = append(tables, sorted)
	}
	for _, candidateTable := range tables {
		indexed, indexedWidth := vp8lApplyPaletteWorkspace(source, width, height, candidateTable, workspace, 0)
		deltas := make([]uint32, len(candidateTable))
		for i, pixel := range candidateTable {
			if i == 0 {
				deltas[i] = pixel
			} else {
				deltas[i] = vp8lSubPixels(pixel, candidateTable[i-1])
			}
		}
		paletteTransform := vp8lTransform{
			kind:        vp8lTransformColorIndex,
			paletteSize: len(candidateTable),
			image:       buildVP8LLiteralImagePlan(deltas, len(deltas), 1),
		}
		candidate := vp8lTransformCandidate{
			width:      indexedWidth,
			height:     height,
			pixels:     indexed,
			transforms: []vp8lTransform{paletteTransform},
			materialize: func() []uint32 {
				pixels, _ := vp8lApplyPalette(source, width, height, candidateTable)
				return pixels
			},
		}
		visit(candidate)
		if !budget.tryCombined || indexedWidth*height < 4 {
			continue
		}
		modes, transformWidth, transformHeight := vp8lChooseBlockPredictors(indexed, indexedWidth, height, 4, budget.predictorModes)
		if len(modes) > 1 && !vp8lAllBytesEqual(modes) {
			pixels := vp8lApplyPredictorWorkspace(indexed, indexedWidth, height, 4, modes, transformWidth, workspace, 1)
			transforms := []vp8lTransform{paletteTransform, vp8lPredictorTransform(4, modes, transformWidth, transformHeight)}
			materialize := candidate.materialize
			visit(vp8lTransformCandidate{
				width:      indexedWidth,
				height:     height,
				pixels:     pixels,
				transforms: transforms,
				materialize: func() []uint32 {
					return vp8lApplyPredictor(materialize(), indexedWidth, height, 4, modes, transformWidth)
				},
			})
		}
	}
}

func vp8lPalette(source []uint32) ([]uint32, bool) {
	index := make(map[uint32]uint8, 256)
	table := make([]uint32, 0, 256)
	for _, pixel := range source {
		if _, exists := index[pixel]; exists {
			continue
		}
		if len(table) == 256 {
			return nil, false
		}
		index[pixel] = uint8(len(table))
		table = append(table, pixel)
	}
	return table, len(table) != 0
}

func vp8lApplyPalette(source []uint32, width int, height int, table []uint32) ([]uint32, int) {
	return vp8lApplyPaletteWorkspace(source, width, height, table, nil, 0)
}

func vp8lApplyPaletteWorkspace(source []uint32, width int, height int, table []uint32, workspace *vp8lTransformWorkspace, slot int) ([]uint32, int) {
	index := make(map[uint32]uint8, len(table))
	for i, pixel := range table {
		index[pixel] = uint8(i)
	}
	widthBits := vp8lColorIndexWidthBits(len(table))
	indexedWidth := vp8lDivRoundUp(width, 1<<widthBits)
	result := workspace.pixels(slot, indexedWidth*height)
	groupSize := 1 << widthBits
	bitsPerIndex := 8 / groupSize
	for y := range height {
		for x := range indexedWidth {
			var packed uint8
			for i := range groupSize {
				sourceX := x*groupSize + i
				if sourceX >= width {
					break
				}
				packed |= index[source[y*width+sourceX]] << uint(i*bitsPerIndex)
			}
			result[y*indexedWidth+x] = 0xff000000 | uint32(packed)<<8
		}
	}
	return result, indexedWidth
}

func vp8lColorIndexWidthBits(size int) uint8 {
	switch {
	case size <= 2:
		return 3
	case size <= 4:
		return 2
	case size <= 16:
		return 1
	default:
		return 0
	}
}

func vp8lTransformDimensions(width int, height int, sizeBits uint8) (int, int) {
	return vp8lDivRoundUp(width, 1<<sizeBits), vp8lDivRoundUp(height, 1<<sizeBits)
}

func vp8lDivRoundUp(value int, divisor int) int {
	return (value + divisor - 1) / divisor
}

func vp8lAllBytesEqual(values []uint8) bool {
	for _, value := range values[1:] {
		if value != values[0] {
			return false
		}
	}
	return true
}

func vp8lUint32SlicesEqual(a []uint32, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
