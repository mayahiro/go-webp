package webp

import "image"

var (
	ycbcrToVP8LumaTable   = makeYCbCrToVP8LumaTable()
	ycbcrToVP8ChromaTable = makeYCbCrToVP8ChromaTable()
)

func makeYCbCrToVP8LumaTable() [256]uint8 {
	var table [256]uint8
	for value := range table {
		table[value] = uint8(16 + (value*219+127)/255)
	}
	return table
}

func makeYCbCrToVP8ChromaTable() [256]uint8 {
	var table [256]uint8
	for value := range table {
		delta := value - 128
		if delta < 0 {
			table[value] = uint8(128 - (-delta*224+127)/255)
		} else {
			table[value] = uint8(128 + (delta*224+127)/255)
		}
	}
	return table
}

func ycbcrToVP8Luma(y uint8) uint8 {
	countLossyCounter(lossyCounterYCbCrDirectConversions, 1)
	return ycbcrToVP8LumaTable[y]
}

func ycbcrToVP8Chroma(cb uint8, cr uint8) (uint8, uint8) {
	countLossyCounter(lossyCounterYCbCrDirectConversions, 1)
	return ycbcrToVP8ChromaTable[cb], ycbcrToVP8ChromaTable[cr]
}

func ycbcrToVP8ChromaAt(cb []uint8, cr []uint8, offset int) (uint8, uint8) {
	countLossyCounter(lossyCounterYCbCrDirectConversions, 1)
	return ycbcrToVP8ChromaTable[cb[offset]], ycbcrToVP8ChromaTable[cr[offset]]
}

func ycbcrChromaReader(img *image.YCbCr) chromaReader {
	cbPix := img.Cb
	crPix := img.Cr
	stride := img.CStride
	minX := img.Rect.Min.X
	minY := img.Rect.Min.Y

	switch img.SubsampleRatio {
	case image.YCbCrSubsampleRatio422:
		minChromaX := minX / 2
		return func(x int, y int) (uint8, uint8) {
			return ycbcrToVP8ChromaAt(cbPix, crPix, (y-minY)*stride+x/2-minChromaX)
		}
	case image.YCbCrSubsampleRatio420:
		minChromaX := minX / 2
		minChromaY := minY / 2
		return func(x int, y int) (uint8, uint8) {
			return ycbcrToVP8ChromaAt(cbPix, crPix, (y/2-minChromaY)*stride+x/2-minChromaX)
		}
	case image.YCbCrSubsampleRatio440:
		minChromaY := minY / 2
		return func(x int, y int) (uint8, uint8) {
			return ycbcrToVP8ChromaAt(cbPix, crPix, (y/2-minChromaY)*stride+x-minX)
		}
	case image.YCbCrSubsampleRatio411:
		minChromaX := minX / 4
		return func(x int, y int) (uint8, uint8) {
			return ycbcrToVP8ChromaAt(cbPix, crPix, (y-minY)*stride+x/4-minChromaX)
		}
	case image.YCbCrSubsampleRatio410:
		minChromaX := minX / 4
		minChromaY := minY / 2
		return func(x int, y int) (uint8, uint8) {
			return ycbcrToVP8ChromaAt(cbPix, crPix, (y/2-minChromaY)*stride+x/4-minChromaX)
		}
	default:
		return func(x int, y int) (uint8, uint8) {
			return ycbcrToVP8ChromaAt(cbPix, crPix, (y-minY)*stride+x-minX)
		}
	}
}
