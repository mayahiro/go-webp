package webp

import (
	"errors"
	"fmt"
	"image"
	"image/color"
)

var errVP8LSourceLimit = errors.New("webp: VP8L search workspace exceeds limit")

type vp8lSource struct {
	width    int
	height   int
	paletted bool
	readRow  func(y int, dst []uint32)
	cancel   *encodeCancellation
}

func newVP8LSource(source encoderSource, readPixel pixelReader) vp8lSource {
	_, paletted := source.image.(*image.Paletted)
	// Standard readers finish each row without calling user code. Check once per
	// row there, and before each pixel for potentially slow custom image methods.
	if source.cancel != nil && !standardImageSupportsConcurrentRead(source.image) {
		readPixel = source.cancel.pixels(readPixel)
	}
	return vp8lSource{
		width:    source.width,
		height:   source.height,
		paletted: paletted,
		cancel:   source.cancel,
		readRow: func(y int, dst []uint32) {
			source.cancel.check()
			sourceY := source.bounds.Min.Y + y
			for x := range source.width {
				dst[x] = vp8lPackPixel(readPixel(source.bounds.Min.X+x, sourceY))
			}
		},
	}
}

func (s vp8lSource) materialize(maxBytes uint64) ([]uint32, bool, error) {
	if s.width <= 0 || s.height <= 0 || s.readRow == nil {
		return nil, false, fmt.Errorf("webp: invalid VP8L source")
	}
	total := uint64(s.width) * uint64(s.height)
	if total > uint64(vp8lMaxInt()) || total > maxBytes/4 {
		return nil, false, errVP8LSourceLimit
	}
	pixels := make([]uint32, int(total))
	alpha := false
	for y := range s.height {
		row := pixels[y*s.width : (y+1)*s.width]
		s.readRow(y, row)
		for _, pixel := range row {
			alpha = alpha || uint8(pixel>>24) != 255
		}
	}
	return pixels, alpha, nil
}

func vp8lPackPixel(pixel color.NRGBA) uint32 {
	return uint32(pixel.A)<<24 | uint32(pixel.R)<<16 | uint32(pixel.G)<<8 | uint32(pixel.B)
}

func vp8lUnpackPixel(pixel uint32) color.NRGBA {
	return color.NRGBA{
		R: uint8(pixel >> 16),
		G: uint8(pixel >> 8),
		B: uint8(pixel),
		A: uint8(pixel >> 24),
	}
}

func vp8lMaxInt() int {
	return int(^uint(0) >> 1)
}
