package webp

import (
	"image"
	"image/color"
)

func vp8lDistanceCodeForPositionDistance(distance int, width int) (int, bool) {
	if distance <= 0 {
		return 0, false
	}
	if code, ok := vp8lSpecialDistanceCode(distance, width); ok {
		return code, true
	}
	distanceCode := distance + 120
	if distanceCode > vp8lMaxDistanceCode {
		return 0, false
	}
	return distanceCode, true
}

func vp8lSpecialDistanceCode(distance int, width int) (int, bool) {
	if width >= 16 {
		return vp8lSpecialDistanceCodeFast(distance, width)
	}
	for i, offset := range vp8lDistanceMap {
		mapped := offset.x + offset.y*width
		if mapped == distance && mapped >= 1 {
			return i + 1, true
		}
	}
	return 0, false
}

func vp8lSpecialDistanceCodeFast(distance int, width int) (int, bool) {
	y := distance / width
	x := distance - y*width
	if y < len(vp8lSpecialDistanceCodeByOffset) && x <= 8 {
		if code := vp8lSpecialDistanceCodeByOffset[y][x+7]; code != 0 {
			return int(code), true
		}
	}
	y++
	x -= width
	if y < len(vp8lSpecialDistanceCodeByOffset) && x >= -7 {
		if code := vp8lSpecialDistanceCodeByOffset[y][x+7]; code != 0 {
			return int(code), true
		}
	}
	return 0, false
}

func vp8lPixelAt(readPixel pixelReader, bounds image.Rectangle, width int, pos int) color.NRGBA {
	return readPixel(bounds.Min.X+pos%width, bounds.Min.Y+pos/width)
}

func nrgbaManhattanDistance(a color.NRGBA, b color.NRGBA) int {
	return absInt(int(a.R)-int(b.R)) + absInt(int(a.G)-int(b.G)) + absInt(int(a.B)-int(b.B)) + absInt(int(a.A)-int(b.A))
}

type vp8lDistanceOffset struct {
	x int
	y int
}

var vp8lDistanceMap = [...]vp8lDistanceOffset{
	{0, 1}, {1, 0}, {1, 1}, {-1, 1}, {0, 2}, {2, 0}, {1, 2}, {-1, 2},
	{2, 1}, {-2, 1}, {2, 2}, {-2, 2}, {0, 3}, {3, 0}, {1, 3}, {-1, 3},
	{3, 1}, {-3, 1}, {2, 3}, {-2, 3}, {3, 2}, {-3, 2}, {0, 4}, {4, 0},
	{1, 4}, {-1, 4}, {4, 1}, {-4, 1}, {3, 3}, {-3, 3}, {2, 4}, {-2, 4},
	{4, 2}, {-4, 2}, {0, 5}, {3, 4}, {-3, 4}, {4, 3}, {-4, 3}, {5, 0},
	{1, 5}, {-1, 5}, {5, 1}, {-5, 1}, {2, 5}, {-2, 5}, {5, 2}, {-5, 2},
	{4, 4}, {-4, 4}, {3, 5}, {-3, 5}, {5, 3}, {-5, 3}, {0, 6}, {6, 0},
	{1, 6}, {-1, 6}, {6, 1}, {-6, 1}, {2, 6}, {-2, 6}, {6, 2}, {-6, 2},
	{4, 5}, {-4, 5}, {5, 4}, {-5, 4}, {3, 6}, {-3, 6}, {6, 3}, {-6, 3},
	{0, 7}, {7, 0}, {1, 7}, {-1, 7}, {5, 5}, {-5, 5}, {7, 1}, {-7, 1},
	{4, 6}, {-4, 6}, {6, 4}, {-6, 4}, {2, 7}, {-2, 7}, {7, 2}, {-7, 2},
	{3, 7}, {-3, 7}, {7, 3}, {-7, 3}, {5, 6}, {-5, 6}, {6, 5}, {-6, 5},
	{8, 0}, {4, 7}, {-4, 7}, {7, 4}, {-7, 4}, {8, 1}, {8, 2}, {6, 6},
	{-6, 6}, {8, 3}, {5, 7}, {-5, 7}, {7, 5}, {-7, 5}, {8, 4}, {6, 7},
	{-6, 7}, {7, 6}, {-7, 6}, {8, 5}, {7, 7}, {-7, 7}, {8, 6}, {8, 7},
}

var vp8lSpecialDistanceCodeByOffset = func() [8][16]uint8 {
	var table [8][16]uint8
	for i, offset := range vp8lDistanceMap {
		if offset.y < 0 || offset.y >= len(table) || offset.x < -7 || offset.x > 8 {
			continue
		}
		table[offset.y][offset.x+7] = uint8(i + 1)
	}
	return table
}()

func vp8lPredictorFromNeighbors(mode uint8, left color.NRGBA, top color.NRGBA, topRight color.NRGBA, topLeft color.NRGBA) color.NRGBA {
	switch mode {
	case 0:
		return color.NRGBA{A: 255}
	case 1:
		return left
	case 2:
		return top
	case 3:
		return topRight
	case 4:
		return topLeft
	case 5:
		return averageNRGBA(averageNRGBA(left, topRight), top)
	case 6:
		return averageNRGBA(left, topLeft)
	case 7:
		return averageNRGBA(left, top)
	case 8:
		return averageNRGBA(topLeft, top)
	case 9:
		return averageNRGBA(top, topRight)
	case 10:
		return averageNRGBA(averageNRGBA(left, topLeft), averageNRGBA(top, topRight))
	case 11:
		return selectPredictorNRGBA(left, top, topLeft)
	case 12:
		return clampAddSubtractFullNRGBA(left, top, topLeft)
	case 13:
		return clampAddSubtractHalfNRGBA(averageNRGBA(left, top), topLeft)
	default:
		return color.NRGBA{A: 255}
	}
}

func averageNRGBA(a color.NRGBA, b color.NRGBA) color.NRGBA {
	return color.NRGBA{
		R: averageUint8(a.R, b.R),
		G: averageUint8(a.G, b.G),
		B: averageUint8(a.B, b.B),
		A: averageUint8(a.A, b.A),
	}
}

func averageUint8(a uint8, b uint8) uint8 {
	return uint8((uint16(a) + uint16(b)) / 2)
}

func selectPredictorNRGBA(left color.NRGBA, top color.NRGBA, topLeft color.NRGBA) color.NRGBA {
	pAlpha := int(left.A) + int(top.A) - int(topLeft.A)
	pRed := int(left.R) + int(top.R) - int(topLeft.R)
	pGreen := int(left.G) + int(top.G) - int(topLeft.G)
	pBlue := int(left.B) + int(top.B) - int(topLeft.B)
	pLeft := absInt(pAlpha-int(left.A)) + absInt(pRed-int(left.R)) + absInt(pGreen-int(left.G)) + absInt(pBlue-int(left.B))
	pTop := absInt(pAlpha-int(top.A)) + absInt(pRed-int(top.R)) + absInt(pGreen-int(top.G)) + absInt(pBlue-int(top.B))
	if pLeft < pTop {
		return left
	}
	return top
}

func clampAddSubtractFullNRGBA(a color.NRGBA, b color.NRGBA, c color.NRGBA) color.NRGBA {
	return color.NRGBA{
		R: clampUint8(int(a.R) + int(b.R) - int(c.R)),
		G: clampUint8(int(a.G) + int(b.G) - int(c.G)),
		B: clampUint8(int(a.B) + int(b.B) - int(c.B)),
		A: clampUint8(int(a.A) + int(b.A) - int(c.A)),
	}
}

func clampAddSubtractHalfNRGBA(a color.NRGBA, b color.NRGBA) color.NRGBA {
	return color.NRGBA{
		R: clampUint8(int(a.R) + (int(a.R)-int(b.R))/2),
		G: clampUint8(int(a.G) + (int(a.G)-int(b.G))/2),
		B: clampUint8(int(a.B) + (int(a.B)-int(b.B))/2),
		A: clampUint8(int(a.A) + (int(a.A)-int(b.A))/2),
	}
}

func clampUint8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
