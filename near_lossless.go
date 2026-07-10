package webp

import (
	"image/color"
)

const (
	nearLosslessMinDimension  = 64
	nearLosslessPlaneMaxBytes = 32 << 20
)

func newNearLosslessReader(source encoderSource, quality int) pixelReader {
	bits := nearLosslessQuantizationBits(quality)
	readPixel := source.pixels()
	if bits == 0 || nearLosslessKeepsSmallImage(source.width, source.height) {
		return readPixel
	}

	plane, ok := materializePixelPlane(readPixel, source.bounds, source.width, source.height, nearLosslessPlaneMaxBytes)
	if !ok {
		return nearLosslessSinglePassReader(readPixel, source, bits)
	}
	scratch := make([]color.NRGBA, source.width*3)
	for passBits := bits; passBits > 0; passBits-- {
		applyNearLosslessPass(plane.pixels, source.width, source.height, passBits, scratch)
	}
	return plane.pixel
}

func nearLosslessKeepsSmallImage(width int, height int) bool {
	return width < nearLosslessMinDimension && height < nearLosslessMinDimension || height < 3
}

func applyNearLosslessPass(pixels []color.NRGBA, width int, height int, bits int, scratch []color.NRGBA) {
	if width == 0 || height == 0 {
		return
	}
	previous := scratch[:width]
	current := scratch[width : width*2]
	next := scratch[width*2 : width*3]
	copy(current, pixels[:width])
	if height > 1 {
		copy(next, pixels[width:width*2])
	}

	limit := 1 << bits
	step := uint8(limit)
	for y := 0; y < height; y++ {
		if y > 0 && y < height-1 {
			copy(next, pixels[(y+1)*width:(y+2)*width])
			for x := 1; x < width-1; x++ {
				pixel := current[x]
				if nearLosslessRGBSmooth(pixel, current[x-1], current[x+1], previous[x], next[x], limit) {
					continue
				}
				pixel.R = quantizeNearLosslessChannel(pixel.R, step)
				pixel.G = quantizeNearLosslessChannel(pixel.G, step)
				pixel.B = quantizeNearLosslessChannel(pixel.B, step)
				pixels[y*width+x] = pixel
			}
		}
		previous, current, next = current, next, previous
	}
}

func nearLosslessSinglePassReader(readPixel pixelReader, source encoderSource, bits int) pixelReader {
	limit := 1 << bits
	step := uint8(limit)
	return func(x int, y int) color.NRGBA {
		pixel := readPixel(x, y)
		if x == source.bounds.Min.X || x == source.bounds.Max.X-1 ||
			y == source.bounds.Min.Y || y == source.bounds.Max.Y-1 {
			return pixel
		}
		if nearLosslessRGBSmooth(
			pixel,
			readPixel(x-1, y),
			readPixel(x+1, y),
			readPixel(x, y-1),
			readPixel(x, y+1),
			limit,
		) {
			return pixel
		}
		pixel.R = quantizeNearLosslessChannel(pixel.R, step)
		pixel.G = quantizeNearLosslessChannel(pixel.G, step)
		pixel.B = quantizeNearLosslessChannel(pixel.B, step)
		return pixel
	}
}

func nearLosslessRGBSmooth(center color.NRGBA, left color.NRGBA, right color.NRGBA, above color.NRGBA, below color.NRGBA, limit int) bool {
	return nearLosslessRGBNear(center, left, limit) &&
		nearLosslessRGBNear(center, right, limit) &&
		nearLosslessRGBNear(center, above, limit) &&
		nearLosslessRGBNear(center, below, limit)
}

func nearLosslessRGBNear(a color.NRGBA, b color.NRGBA, limit int) bool {
	return absInt(int(a.R)-int(b.R)) < limit &&
		absInt(int(a.G)-int(b.G)) < limit &&
		absInt(int(a.B)-int(b.B)) < limit
}

func nearLosslessQuantizationBits(quality int) int {
	if quality >= 100 {
		return 0
	}
	if quality < 0 {
		quality = 0
	}
	return 5 - quality/20
}

func quantizeNearLosslessChannel(value uint8, step uint8) uint8 {
	if step <= 1 {
		return value
	}
	mask := int(step) - 1
	biased := int(value) + mask/2 + ((int(value) / int(step)) & 1)
	if biased > 255 {
		return 255
	}
	return uint8(biased &^ mask)
}
