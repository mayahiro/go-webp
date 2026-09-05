package webp

import (
	"image"
	"image/color"
)

const (
	vp8SourcePlaneMaxBytes = 32 << 20
	vp8lMaxSourceBytes     = 32 << 20
)

type pixelReader func(x int, y int) color.NRGBA
type lumaReader func(x int, y int) uint8
type chromaReader func(x int, y int) (uint8, uint8)

type pixelPlane struct {
	pixels []color.NRGBA
	bounds image.Rectangle
	width  int
}

func materializePixelPlane(readPixel pixelReader, bounds image.Rectangle, width int, height int, maxBytes uint64) (pixelPlane, bool) {
	total := uint64(width) * uint64(height)
	if total == 0 || total > maxBytes/4 {
		return pixelPlane{}, false
	}
	pixels := make([]color.NRGBA, int(total))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixels[y*width+x] = readPixel(bounds.Min.X+x, bounds.Min.Y+y)
		}
	}
	return pixelPlane{pixels: pixels, bounds: bounds, width: width}, true
}

func (p pixelPlane) pixel(x int, y int) color.NRGBA {
	return p.pixels[(y-p.bounds.Min.Y)*p.width+x-p.bounds.Min.X]
}

func standardImageSupportsConcurrentRead(m image.Image) bool {
	switch m.(type) {
	case *image.NRGBA, *image.NRGBA64, *image.RGBA, *image.RGBA64, *image.Gray, *image.Gray16, *image.YCbCr, *image.Paletted, *image.Uniform:
		return true
	default:
		return false
	}
}

type encoderSource struct {
	image  image.Image
	bounds image.Rectangle
	width  int
	height int
}

func newEncoderSource(m image.Image) encoderSource {
	bounds := m.Bounds()
	return encoderSource{
		image:  m,
		bounds: bounds,
		width:  bounds.Dx(),
		height: bounds.Dy(),
	}
}

func (s encoderSource) pixels() pixelReader {
	return pixelReaderFor(s.image)
}

func (s encoderSource) readLuma() lumaReader {
	return lumaReaderFor(s.image)
}

func (s encoderSource) readChroma() chromaReader {
	return chromaReaderFor(s.image)
}

type vp8Source struct {
	bounds     image.Rectangle
	width      int
	height     int
	readLuma   lumaReader
	readChroma chromaReader
	plane      vp8SourcePlane
}

type vp8SourcePlane struct {
	data   []uint8
	width  int
	minX   int
	minY   int
	cbBase int
	crBase int
}

func newVP8Source(source encoderSource, materialize bool) vp8Source {
	result := vp8Source{
		bounds: source.bounds,
		width:  source.width,
		height: source.height,
	}
	total := source.width * source.height
	if !materialize || total <= 0 || total > vp8SourcePlaneMaxBytes/3 {
		result.readLuma = instrumentLossyLumaReader(source.readLuma())
		result.readChroma = instrumentLossyChromaReader(source.readChroma())
		return result
	}
	data := make([]uint8, total*3)
	countLossyCounter(lossyCounterPreparedSourceBytes, uint64(len(data)))
	plane := vp8SourcePlane{
		data:   data,
		width:  source.width,
		minX:   source.bounds.Min.X,
		minY:   source.bounds.Min.Y,
		cbBase: total,
		crBase: total * 2,
	}
	if _, ok := source.image.(*image.YCbCr); ok {
		readLuma := instrumentLossyLumaReader(source.readLuma())
		readChroma := instrumentLossyChromaReader(source.readChroma())
		for y := source.bounds.Min.Y; y < source.bounds.Max.Y; y++ {
			row := (y - source.bounds.Min.Y) * source.width
			for x := source.bounds.Min.X; x < source.bounds.Max.X; x++ {
				index := row + x - source.bounds.Min.X
				data[index] = readLuma(x, y)
				data[plane.cbBase+index], data[plane.crBase+index] = readChroma(x, y)
			}
		}
	} else {
		readPixel := instrumentLossyPixelReader(source.pixels())
		for y := source.bounds.Min.Y; y < source.bounds.Max.Y; y++ {
			row := (y - source.bounds.Min.Y) * source.width
			for x := source.bounds.Min.X; x < source.bounds.Max.X; x++ {
				index := row + x - source.bounds.Min.X
				pixel := readPixel(x, y)
				data[index] = rgbToLuma(pixel.R, pixel.G, pixel.B)
				data[plane.cbBase+index], data[plane.crBase+index] = rgbToChroma(pixel.R, pixel.G, pixel.B)
			}
		}
	}
	result.plane = plane
	result.readLuma = plane.luma
	result.readChroma = plane.chroma
	return result
}

func (p vp8SourcePlane) luma(x int, y int) uint8 {
	return p.data[(y-p.minY)*p.width+x-p.minX]
}

func (p vp8SourcePlane) chroma(x int, y int) (uint8, uint8) {
	index := (y-p.minY)*p.width + x - p.minX
	return p.data[p.cbBase+index], p.data[p.crBase+index]
}

func (s vp8Source) materialized() bool {
	return len(s.plane.data) != 0
}

func (s *vp8Source) applySharpChroma(readPixel pixelReader) {
	if !s.materialized() || readPixel == nil {
		return
	}
	halfWidth := (s.width + 1) >> 1
	halfHeight := (s.height + 1) >> 1
	selected := make([]uint8, halfWidth*halfHeight*2)
	selectedCr := selected[halfWidth*halfHeight:]
	for by := 0; by < halfHeight; by++ {
		for bx := 0; bx < halfWidth; bx++ {
			x := bx * 2
			y := by * 2
			baselineCb, baselineCr := chromaSamplePair(s.readChroma, s.bounds, x, y)
			meanCb, meanCr := s.meanChroma2x2(x, y)
			block := s.chromaRGBBlock(readPixel, x, y)
			bestCb, bestCr := baselineCb, baselineCr
			bestScore := block.score(bestCb, bestCr)
			centers := [2][2]uint8{{baselineCb, baselineCr}, {meanCb, meanCr}}
			for _, center := range centers {
				for cbDelta := -2; cbDelta <= 2; cbDelta++ {
					cb := uint8(clipInt(int(center[0])+cbDelta, 0, 255))
					for crDelta := -2; crDelta <= 2; crDelta++ {
						cr := uint8(clipInt(int(center[1])+crDelta, 0, 255))
						score := block.score(cb, cr)
						if score < bestScore {
							bestCb, bestCr, bestScore = cb, cr, score
						}
					}
				}
			}
			index := by*halfWidth + bx
			selected[index] = bestCb
			selectedCr[index] = bestCr
		}
	}
	for y := 0; y < s.height; y++ {
		for x := 0; x < s.width; x++ {
			selectedIndex := (y>>1)*halfWidth + (x >> 1)
			planeIndex := y*s.width + x
			s.plane.data[s.plane.cbBase+planeIndex] = selected[selectedIndex]
			s.plane.data[s.plane.crBase+planeIndex] = selectedCr[selectedIndex]
		}
	}
}

func (s *vp8Source) meanChroma2x2(x int, y int) (uint8, uint8) {
	cbSum, crSum := 0, 0
	for yy := 0; yy < 2; yy++ {
		for xx := 0; xx < 2; xx++ {
			cb, cr := sampleChroma(s.readChroma, s.bounds, x+xx, y+yy)
			cbSum += int(cb)
			crSum += int(cr)
		}
	}
	return uint8((cbSum + 2) >> 2), uint8((crSum + 2) >> 2)
}

type vp8ChromaRGBBlock struct {
	pixels [4]color.NRGBA
	luma   [4]uint8
	n      int
}

func (s *vp8Source) chromaRGBBlock(readPixel pixelReader, x int, y int) vp8ChromaRGBBlock {
	var block vp8ChromaRGBBlock
	for yy := 0; yy < 2 && y+yy < s.height; yy++ {
		for xx := 0; xx < 2 && x+xx < s.width; xx++ {
			planeIndex := (y+yy)*s.width + x + xx
			block.luma[block.n] = s.plane.data[planeIndex]
			block.pixels[block.n] = readPixel(s.bounds.Min.X+x+xx, s.bounds.Min.Y+y+yy)
			block.n++
		}
	}
	return block
}

func (block *vp8ChromaRGBBlock) score(cb uint8, cr uint8) uint64 {
	countLossyCounter(lossyCounterSharpChromaCandidates, 1)
	var score uint64
	for i := range block.n {
		r, g, b := vp8YUVToRGB(block.luma[i], cb, cr)
		pixel := block.pixels[i]
		dr := int(r) - int(pixel.R)
		dg := int(g) - int(pixel.G)
		db := int(b) - int(pixel.B)
		score += uint64(dr*dr + dg*dg + db*db)
	}
	return score
}

func vp8YUVToRGB(y uint8, cb uint8, cr uint8) (uint8, uint8, uint8) {
	r := ((int(y) * 19077) >> 8) + ((int(cr) * 26149) >> 8) - 14234
	g := ((int(y) * 19077) >> 8) - ((int(cb) * 6419) >> 8) - ((int(cr) * 13320) >> 8) + 8708
	b := ((int(y) * 19077) >> 8) + ((int(cb) * 33050) >> 8) - 17685
	return uint8(clipInt(r>>6, 0, 255)), uint8(clipInt(g>>6, 0, 255)), uint8(clipInt(b>>6, 0, 255))
}
