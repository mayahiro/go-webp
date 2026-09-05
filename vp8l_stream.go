package webp

import (
	"fmt"
)

type vp8lPixelStream func(visit func(uint32))

type vp8lTokenStream func(visit func(vp8lToken))

type vp8lStreamingPlan struct {
	width       int
	height      int
	alpha       bool
	transforms  []vp8lTransform
	group       vp8lCodeGroup
	tokens      vp8lTokenStream
	payloadBits uint64
}

func (p *vp8lStreamingPlan) payloadBitLen() uint64 {
	return p.payloadBits
}

func (p *vp8lStreamingPlan) writeTo(bits *vp8lBitSink) {
	writeVP8LPrefix(bits, p.width, p.height, p.alpha, p.transforms)
	bits.writeBits(0, 1) // no color cache
	bits.writeBits(0, 1) // no meta-prefix image
	p.group.writeHeaders(bits)
	p.tokens(func(token vp8lToken) {
		switch token.kind() {
		case vp8lTokenLiteral:
			pixel := token.literal()
			p.group.green.writeSymbol(bits, int(uint8(pixel>>8)))
			p.group.red.writeSymbol(bits, int(uint8(pixel>>16)))
			p.group.blue.writeSymbol(bits, int(uint8(pixel)))
			p.group.alpha.writeSymbol(bits, int(uint8(pixel>>24)))
		case vp8lTokenCopy:
			lengthPrefix := vp8lPrefixCode(token.copyLength())
			distancePrefix := vp8lDistancePrefixCode(token.distanceCode())
			p.group.green.writeSymbol(bits, nLiteralCodes+lengthPrefix.code)
			bits.writeBits(lengthPrefix.extra, lengthPrefix.extraBits)
			p.group.distance.writeSymbol(bits, distancePrefix.code)
			bits.writeBits(distancePrefix.extra, distancePrefix.extraBits)
		}
	})
}

func searchVP8LStreaming(source vp8lSource, mode Mode) (*vp8lStreamingPlan, error) {
	if source.width <= 0 || source.height <= 0 || source.readRow == nil {
		return nil, fmt.Errorf("webp: invalid VP8L source")
	}

	alpha, table, paletteOK := vp8lStreamingSourceInfo(source)
	var best *vp8lStreamingPlan
	consider := func(transforms []vp8lTransform, stream vp8lPixelStream, imageWidth int) {
		for _, tokens := range []vp8lTokenStream{
			vp8lLiteralTokenStream(stream),
			vp8lGreedyTokenStream(stream, imageWidth),
		} {
			group, dataBits := vp8lAnalyzeStreamingTokens(tokens)
			candidate := newVP8LStreamingPlan(source.width, source.height, alpha, transforms, group, dataBits, tokens)
			if best == nil || candidate.payloadBitLen() < best.payloadBitLen() {
				best = candidate
			}
		}
	}

	directStream := vp8lSourcePixelStream(source, nil)
	consider(nil, directStream, source.width)

	if mode != ModeFast {
		consider(
			[]vp8lTransform{{kind: vp8lTransformSubtractGreen}},
			vp8lSourcePixelStream(source, vp8lSubtractGreenPixel),
			source.width,
		)
		for _, predictorMode := range vp8lStreamingPredictorModes(mode) {
			modes, transformWidth, transformHeight := vp8lUniformPredictorImage(source.width, source.height, 9, predictorMode)
			transform := vp8lPredictorTransform(9, modes, transformWidth, transformHeight)
			consider([]vp8lTransform{transform}, vp8lPredictorPixelStream(source, predictorMode), source.width)
		}
	}

	if paletteOK {
		for _, candidateTable := range vp8lPaletteOrders(table) {
			transform := vp8lStreamingPaletteTransform(candidateTable)
			stream, indexedWidth := vp8lPalettePixelStream(source, candidateTable)
			consider([]vp8lTransform{transform}, stream, indexedWidth)
		}
	}
	return best, nil
}

func newVP8LStreamingPlan(width int, height int, alpha bool, transforms []vp8lTransform, group vp8lCodeGroup, dataBits uint64, tokens vp8lTokenStream) *vp8lStreamingPlan {
	plan := &vp8lStreamingPlan{
		width:      width,
		height:     height,
		alpha:      alpha,
		transforms: append([]vp8lTransform(nil), transforms...),
		group:      group,
		tokens:     tokens,
	}
	counter := vp8lBitCounter()
	writeVP8LPrefix(counter, width, height, alpha, transforms)
	counter.writeBits(0, 1)
	counter.writeBits(0, 1)
	group.writeHeaders(counter)
	plan.payloadBits = counter.bitLen + dataBits
	return plan
}

func vp8lAnalyzeStreamingTokens(tokens vp8lTokenStream) (vp8lCodeGroup, uint64) {
	var counts vp8lLiteralCounts
	var extraBits uint64
	tokens(func(token vp8lToken) {
		switch token.kind() {
		case vp8lTokenLiteral:
			counts.observe(token.literal())
		case vp8lTokenCopy:
			lengthPrefix := vp8lPrefixCode(token.copyLength())
			distancePrefix := vp8lDistancePrefixCode(token.distanceCode())
			counts.green[nLiteralCodes+lengthPrefix.code]++
			counts.distance[distancePrefix.code]++
			extraBits += uint64(lengthPrefix.extraBits + distancePrefix.extraBits)
		}
	})
	group, dataBits := counts.codeGroupAndDataBits()
	return group, dataBits + extraBits
}

func vp8lLiteralTokenStream(stream vp8lPixelStream) vp8lTokenStream {
	return func(visit func(vp8lToken)) {
		stream(func(pixel uint32) {
			visit(vp8lLiteralToken(pixel))
		})
	}
}

func vp8lGreedyTokenStream(stream vp8lPixelStream, width int) vp8lTokenStream {
	return func(visit func(vp8lToken)) {
		previous := make([]uint32, width)
		current := make([]uint32, width)
		filled := 0
		hasPrevious := false
		stream(func(pixel uint32) {
			current[filled] = pixel
			filled++
			if filled != width {
				return
			}
			vp8lEmitGreedyRow(current, previous, hasPrevious, width, visit)
			previous, current = current, previous
			filled = 0
			hasPrevious = true
		})
		if filled != 0 {
			vp8lEmitGreedyRow(current[:filled], previous[:filled], hasPrevious, width, visit)
		}
	}
}

func vp8lEmitGreedyRow(current []uint32, previous []uint32, hasPrevious bool, distance int, visit func(vp8lToken)) {
	for x := 0; x < len(current); {
		maxLength := minInt(vp8lMaxBackwardRefLength, len(current)-x)
		runLength := 0
		if x > 0 && current[x-1] == current[x] {
			runLength = 1
			for runLength < maxLength && current[x+runLength] == current[x] {
				runLength++
			}
		}
		rowLength := 0
		if hasPrevious {
			for rowLength < maxLength && previous[x+rowLength] == current[x+rowLength] {
				rowLength++
			}
		}
		length := rowLength
		matchDistance := distance
		if runLength >= rowLength {
			length = runLength
			matchDistance = 1
		}
		if length >= vp8lMinBackwardRefLength {
			distanceCode, ok := vp8lDistanceCodeForPositionDistance(matchDistance, distance)
			if ok {
				visit(vp8lCopyToken(length, distanceCode))
				x += length
				continue
			}
		}
		visit(vp8lLiteralToken(current[x]))
		x++
	}
}

func vp8lSourcePixelStream(source vp8lSource, transform func(uint32) uint32) vp8lPixelStream {
	return func(visit func(uint32)) {
		row := make([]uint32, source.width)
		for y := range source.height {
			source.readRow(y, row)
			for _, pixel := range row {
				if transform != nil {
					pixel = transform(pixel)
				}
				visit(pixel)
			}
		}
	}
}

func vp8lSubtractGreenPixel(pixel uint32) uint32 {
	green := uint8(pixel >> 8)
	return uint32(uint8(pixel>>24))<<24 |
		uint32(uint8(pixel>>16)-green)<<16 |
		uint32(green)<<8 |
		uint32(uint8(pixel)-green)
}

func vp8lPredictorPixelStream(source vp8lSource, mode uint8) vp8lPixelStream {
	return func(visit func(uint32)) {
		previous := make([]uint32, source.width)
		current := make([]uint32, source.width)
		for y := range source.height {
			source.readRow(y, current)
			for x, pixel := range current {
				predictor := uint32(0xff000000)
				switch {
				case x == 0 && y == 0:
				case y == 0:
					predictor = current[x-1]
				case x == 0:
					predictor = previous[x]
				default:
					topRight := current[0]
					if x < source.width-1 {
						topRight = previous[x+1]
					}
					predictor = vp8lPackPixel(vp8lPredictorFromNeighbors(
						mode,
						vp8lUnpackPixel(current[x-1]),
						vp8lUnpackPixel(previous[x]),
						vp8lUnpackPixel(topRight),
						vp8lUnpackPixel(previous[x-1]),
					))
				}
				visit(vp8lSubPixels(pixel, predictor))
			}
			previous, current = current, previous
		}
	}
}

func vp8lStreamingPredictorModes(mode Mode) []uint8 {
	switch mode {
	case ModeBestCompression:
		return []uint8{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}
	case ModeDefault, ModeBalanced, ModeNearLossless:
		return []uint8{1, 2, 12, 3, 4, 7, 11, 13}
	default:
		return []uint8{1, 2, 12}
	}
}

func vp8lStreamingSourceInfo(source vp8lSource) (bool, []uint32, bool) {
	index := make(map[uint32]struct{}, 256)
	table := make([]uint32, 0, 256)
	paletteOK := true
	alpha := false
	row := make([]uint32, source.width)
	for y := range source.height {
		source.readRow(y, row)
		for _, pixel := range row {
			alpha = alpha || uint8(pixel>>24) != 255
			if !paletteOK {
				continue
			}
			if _, exists := index[pixel]; exists {
				continue
			}
			if len(table) == 256 {
				paletteOK = false
				table = nil
				index = nil
				continue
			}
			index[pixel] = struct{}{}
			table = append(table, pixel)
		}
	}
	return alpha, table, paletteOK && len(table) != 0
}

func vp8lStreamingPalette(source vp8lSource) ([]uint32, bool) {
	_, table, ok := vp8lStreamingSourceInfo(source)
	return table, ok
}

func vp8lStreamingPaletteTransform(table []uint32) vp8lTransform {
	deltas := make([]uint32, len(table))
	for i, pixel := range table {
		if i == 0 {
			deltas[i] = pixel
		} else {
			deltas[i] = vp8lSubPixels(pixel, table[i-1])
		}
	}
	return vp8lTransform{
		kind:        vp8lTransformColorIndex,
		paletteSize: len(table),
		image:       buildVP8LLiteralImagePlan(deltas, len(deltas), 1),
	}
}

func vp8lPalettePixelStream(source vp8lSource, table []uint32) (vp8lPixelStream, int) {
	index := make(map[uint32]uint8, len(table))
	for i, pixel := range table {
		index[pixel] = uint8(i)
	}
	widthBits := vp8lColorIndexWidthBits(len(table))
	groupSize := 1 << widthBits
	bitsPerIndex := 8 / groupSize
	indexedWidth := vp8lDivRoundUp(source.width, groupSize)
	return func(visit func(uint32)) {
		row := make([]uint32, source.width)
		for y := range source.height {
			source.readRow(y, row)
			for x := range indexedWidth {
				var packed uint8
				for i := range groupSize {
					sourceX := x*groupSize + i
					if sourceX >= source.width {
						break
					}
					packed |= index[row[sourceX]] << uint(i*bitsPerIndex)
				}
				visit(0xff000000 | uint32(packed)<<8)
			}
		}
	}, indexedWidth
}
