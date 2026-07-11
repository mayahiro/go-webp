package webp

func vp8lVisitBlockColorCandidates(
	source []uint32,
	width int,
	height int,
	budget vp8lBudget,
	prefix []vp8lTransform,
	materializeSource func() []uint32,
	materializeSourceWorkspace func(*vp8lTransformWorkspace, int) []uint32,
	workspace *vp8lTransformWorkspace,
	slot int,
	visit func(vp8lTransformCandidate),
) {
	counters := budget.counters
	for _, sizeBits := range budget.colorSizeBits {
		elements, transformWidth, transformHeight := vp8lChooseBlockColorElements(source, width, height, sizeBits, budget.colorSearchSamples)
		if len(elements) == 0 || vp8lAllColorElementsEqual(elements) {
			continue
		}
		pixels := vp8lApplyBlockColorTransformWorkspace(source, width, height, sizeBits, elements, transformWidth, workspace, slot)
		transform := vp8lBlockColorTransform(sizeBits, elements, transformWidth, transformHeight)
		transforms := append(append([]vp8lTransform(nil), prefix...), transform)
		materializeColor := func() []uint32 {
			counters.recordRematerialization(len(source))
			return vp8lApplyBlockColorTransform(materializeSource(), width, height, sizeBits, elements, transformWidth)
		}
		visit(vp8lTransformCandidate{
			width:       width,
			height:      height,
			pixels:      pixels,
			transforms:  transforms,
			materialize: materializeColor,
			materializeWorkspace: func(workspace *vp8lTransformWorkspace, slot int) []uint32 {
				source := materializeSourceWorkspace(workspace, vp8lAlternateTransformSlot(slot))
				counters.recordWorkspaceMaterialization(len(source))
				return vp8lApplyBlockColorTransformWorkspace(source, width, height, sizeBits, elements, transformWidth, workspace, slot)
			},
		})
	}
}

func vp8lChooseBlockColorElements(source []uint32, width int, height int, sizeBits uint8, sampleLimit int) ([]vp8lColorTransformElement, int, int) {
	transformWidth, transformHeight := vp8lTransformDimensions(width, height, sizeBits)
	elements := make([]vp8lColorTransformElement, transformWidth*transformHeight)
	var accumulatedRed [nLiteralCodes]uint32
	var accumulatedBlue [nLiteralCodes]uint32
	previous := vp8lColorTransformElement{}
	for blockY := range transformHeight {
		for blockX := range transformWidth {
			above := vp8lColorTransformElement{}
			if blockY > 0 {
				above = elements[(blockY-1)*transformWidth+blockX]
			}
			element, red, blue := vp8lBestBlockColorElement(
				source, width, height, sizeBits, blockX, blockY, sampleLimit,
				previous, above, &accumulatedRed, &accumulatedBlue,
			)
			elements[blockY*transformWidth+blockX] = element
			previous = element
			vp8lAddColorCounts(&accumulatedRed, &red)
			vp8lAddColorCounts(&accumulatedBlue, &blue)
		}
	}
	return elements, transformWidth, transformHeight
}

func vp8lBestBlockColorElement(
	source []uint32,
	width int,
	height int,
	sizeBits uint8,
	blockX int,
	blockY int,
	sampleLimit int,
	previous vp8lColorTransformElement,
	above vp8lColorTransformElement,
	accumulatedRed *[nLiteralCodes]uint32,
	accumulatedBlue *[nLiteralCodes]uint32,
) (vp8lColorTransformElement, [nLiteralCodes]uint32, [nLiteralCodes]uint32) {
	bestRed := 0
	bestRedCounts := vp8lBlockColorCounts(source, width, height, sizeBits, blockX, blockY, sampleLimit, 0, 0, false)
	bestRedCost := vp8lColorPredictionCost(&bestRedCounts, accumulatedRed)
	bestRedCost += vp8lColorCoefficientBias(0, int(int8(previous.greenToRed)), int(int8(above.greenToRed)))
	for _, delta := range [...]int{32, 16, 8, 4, 2, 1} {
		for _, offset := range [...]int{-1, 1} {
			candidate := bestRed + offset*delta
			counts := vp8lBlockColorCounts(source, width, height, sizeBits, blockX, blockY, sampleLimit, candidate, 0, false)
			cost := vp8lColorPredictionCost(&counts, accumulatedRed)
			cost += vp8lColorCoefficientBias(candidate, int(int8(previous.greenToRed)), int(int8(above.greenToRed)))
			if cost < bestRedCost {
				bestRed = candidate
				bestRedCounts = counts
				bestRedCost = cost
			}
		}
	}

	bestGreenBlue, bestRedBlue := 0, 0
	bestBlueCounts := vp8lBlockColorCounts(source, width, height, sizeBits, blockX, blockY, sampleLimit, 0, 0, true)
	bestBlueCost := vp8lColorPredictionCost(&bestBlueCounts, accumulatedBlue)
	bestBlueCost += vp8lColorCoefficientBias(0, int(int8(previous.greenToBlue)), int(int8(above.greenToBlue)))
	bestBlueCost += vp8lColorCoefficientBias(0, int(int8(previous.redToBlue)), int(int8(above.redToBlue)))
	deltas := [...]int{16, 16, 8, 4, 2, 2, 2}
	directions := [...][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}, {-1, -1}, {-1, 1}, {1, -1}, {1, 1}}
	for _, delta := range deltas {
		for _, direction := range directions {
			greenBlue := bestGreenBlue + direction[0]*delta
			redBlue := bestRedBlue + direction[1]*delta
			counts := vp8lBlockColorCounts(source, width, height, sizeBits, blockX, blockY, sampleLimit, greenBlue, redBlue, true)
			cost := vp8lColorPredictionCost(&counts, accumulatedBlue)
			cost += vp8lColorCoefficientBias(greenBlue, int(int8(previous.greenToBlue)), int(int8(above.greenToBlue)))
			cost += vp8lColorCoefficientBias(redBlue, int(int8(previous.redToBlue)), int(int8(above.redToBlue)))
			if cost < bestBlueCost {
				bestGreenBlue = greenBlue
				bestRedBlue = redBlue
				bestBlueCounts = counts
				bestBlueCost = cost
			}
		}
	}

	return vp8lColorTransformElement{
		greenToRed:  uint8(int8(bestRed)),
		greenToBlue: uint8(int8(bestGreenBlue)),
		redToBlue:   uint8(int8(bestRedBlue)),
	}, bestRedCounts, bestBlueCounts
}

func vp8lBlockColorCounts(source []uint32, width int, height int, sizeBits uint8, blockX int, blockY int, sampleLimit int, first int, second int, blue bool) [nLiteralCodes]uint32 {
	x0 := blockX << sizeBits
	y0 := blockY << sizeBits
	x1 := minInt(x0+1<<sizeBits, width)
	y1 := minInt(y0+1<<sizeBits, height)
	blockWidth := x1 - x0
	area := blockWidth * (y1 - y0)
	step := 1
	if sampleLimit > 0 && area > sampleLimit {
		step = vp8lDivRoundUp(area, sampleLimit)
	}
	var counts [nLiteralCodes]uint32
	for position := 0; position < area; position += step {
		x := x0 + position%blockWidth
		y := y0 + position/blockWidth
		pixel := source[y*width+x]
		value := uint8(pixel>>16) - vp8lColorDelta(uint8(int8(first)), uint8(pixel>>8))
		if blue {
			value = uint8(pixel) - vp8lColorDelta(uint8(int8(first)), uint8(pixel>>8)) - vp8lColorDelta(uint8(int8(second)), uint8(pixel>>16))
		}
		counts[value]++
	}
	return counts
}

func vp8lColorPredictionCost(counts *[nLiteralCodes]uint32, accumulated *[nLiteralCodes]uint32) float64 {
	var combined [nLiteralCodes]uint32
	for symbol := range combined {
		combined[symbol] = counts[symbol] + accumulated[symbol]
	}
	cost := vp8lHistogramEntropy(counts[:]) + vp8lHistogramEntropy(combined[:])
	cost -= 0.3 * float64(counts[0])
	weight := 0.24
	for symbol := 1; symbol < 16; symbol++ {
		cost -= weight * float64(counts[symbol]+counts[256-symbol])
		weight *= 0.6
	}
	return cost
}

func vp8lColorCoefficientBias(coefficient int, previous int, above int) float64 {
	var bias float64
	if uint8(coefficient) == uint8(previous) {
		bias -= 3
	}
	if uint8(coefficient) == uint8(above) {
		bias -= 3
	}
	if coefficient == 0 {
		bias -= 3
	}
	return bias
}

func vp8lAddColorCounts(accumulated *[nLiteralCodes]uint32, counts *[nLiteralCodes]uint32) {
	for symbol, count := range counts {
		accumulated[symbol] += count
	}
}

func vp8lApplyBlockColorTransform(source []uint32, width int, height int, sizeBits uint8, elements []vp8lColorTransformElement, transformWidth int) []uint32 {
	result := make([]uint32, len(source))
	vp8lApplyBlockColorTransformInto(result, source, width, height, sizeBits, elements, transformWidth)
	return result
}

func vp8lApplyBlockColorTransformWorkspace(source []uint32, width int, height int, sizeBits uint8, elements []vp8lColorTransformElement, transformWidth int, workspace *vp8lTransformWorkspace, slot int) []uint32 {
	result := workspace.pixels(slot, len(source))
	vp8lApplyBlockColorTransformInto(result, source, width, height, sizeBits, elements, transformWidth)
	return result
}

func vp8lApplyBlockColorTransformInto(result []uint32, source []uint32, width int, height int, sizeBits uint8, elements []vp8lColorTransformElement, transformWidth int) {
	for y := range height {
		for x := range width {
			element := elements[(y>>sizeBits)*transformWidth+(x>>sizeBits)]
			result[y*width+x] = vp8lApplyColorElement(source[y*width+x], element)
		}
	}
}

func vp8lApplyColorElement(pixel uint32, element vp8lColorTransformElement) uint32 {
	green := uint8(pixel >> 8)
	redSource := uint8(pixel >> 16)
	red := redSource - vp8lColorDelta(element.greenToRed, green)
	blue := uint8(pixel) - vp8lColorDelta(element.greenToBlue, green) - vp8lColorDelta(element.redToBlue, redSource)
	return pixel&0xff00ff00 | uint32(red)<<16 | uint32(blue)
}

func vp8lBlockColorTransform(sizeBits uint8, elements []vp8lColorTransformElement, width int, height int) vp8lTransform {
	pixels := make([]uint32, len(elements))
	for i, element := range elements {
		pixels[i] = 0xff000000 | uint32(element.redToBlue)<<16 | uint32(element.greenToBlue)<<8 | uint32(element.greenToRed)
	}
	return vp8lTransform{
		kind:     vp8lTransformColor,
		sizeBits: sizeBits,
		image:    buildVP8LLiteralImagePlan(pixels, width, height),
	}
}

func vp8lAllColorElementsEqual(elements []vp8lColorTransformElement) bool {
	for _, element := range elements[1:] {
		if element != elements[0] {
			return false
		}
	}
	return true
}
