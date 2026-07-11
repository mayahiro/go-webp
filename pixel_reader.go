package webp

import (
	"image"
	"image/color"
)

func pixelReaderFor(m image.Image) pixelReader {
	switch img := m.(type) {
	case *image.NRGBA:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) color.NRGBA {
			i := (y-minY)*stride + (x-minX)*4
			return color.NRGBA{
				R: pix[i+0],
				G: pix[i+1],
				B: pix[i+2],
				A: pix[i+3],
			}
		}
	case *image.NRGBA64:
		return func(x int, y int) color.NRGBA {
			return nrgbaFromNRGBA64(img.NRGBA64At(x, y))
		}
	case *image.RGBA:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) color.NRGBA {
			i := (y-minY)*stride + (x-minX)*4
			a := pix[i+3]
			if a == 255 {
				return color.NRGBA{R: pix[i+0], G: pix[i+1], B: pix[i+2], A: 255}
			}
			return nrgbaFromRGBA(pix[i+0], pix[i+1], pix[i+2], a)
		}
	case *image.RGBA64:
		return func(x int, y int) color.NRGBA {
			return nrgbaFromRGBA64(img.RGBA64At(x, y))
		}
	case *image.Gray:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) color.NRGBA {
			gray := pix[(y-minY)*stride+x-minX]
			return color.NRGBA{R: gray, G: gray, B: gray, A: 255}
		}
	case *image.YCbCr:
		yPix := img.Y
		cbPix := img.Cb
		crPix := img.Cr
		return func(x int, y int) color.NRGBA {
			yy := yPix[img.YOffset(x, y)]
			ci := img.COffset(x, y)
			cb := cbPix[ci]
			cr := crPix[ci]
			r, g, b := color.YCbCrToRGB(yy, cb, cr)
			return color.NRGBA{R: r, G: g, B: b, A: 255}
		}
	case *image.Paletted:
		if len(img.Palette) == 0 {
			return func(x int, y int) color.NRGBA {
				return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			}
		}
		palette := make([]color.NRGBA, len(img.Palette))
		for i, c := range img.Palette {
			palette[i] = color.NRGBAModel.Convert(c).(color.NRGBA)
		}
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) color.NRGBA {
			return palette[pix[(y-minY)*stride+x-minX]]
		}
	case *image.Uniform:
		c := color.NRGBAModel.Convert(img.C).(color.NRGBA)
		return func(int, int) color.NRGBA {
			return c
		}
	case image.RGBA64Image:
		return func(x int, y int) color.NRGBA {
			return nrgbaFromRGBA64(img.RGBA64At(x, y))
		}
	default:
		return func(x int, y int) color.NRGBA {
			return color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA)
		}
	}
}

func lumaReaderFor(m image.Image) lumaReader {
	switch img := m.(type) {
	case *image.NRGBA:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) uint8 {
			i := (y-minY)*stride + (x-minX)*4
			return rgbToLuma(pix[i+0], pix[i+1], pix[i+2])
		}
	case *image.RGBA:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) uint8 {
			i := (y-minY)*stride + (x-minX)*4
			if pix[i+3] == 255 {
				return rgbToLuma(pix[i+0], pix[i+1], pix[i+2])
			}
			c := nrgbaFromRGBA(pix[i+0], pix[i+1], pix[i+2], pix[i+3])
			return rgbToLuma(c.R, c.G, c.B)
		}
	case *image.Gray:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) uint8 {
			value := pix[(y-minY)*stride+x-minX]
			return rgbToLuma(value, value, value)
		}
	case *image.YCbCr:
		yPix := img.Y
		cbPix := img.Cb
		crPix := img.Cr
		return func(x int, y int) uint8 {
			yy := yPix[img.YOffset(x, y)]
			ci := img.COffset(x, y)
			cb := cbPix[ci]
			cr := crPix[ci]
			r, g, b := color.YCbCrToRGB(yy, cb, cr)
			return rgbToLuma(r, g, b)
		}
	case *image.Paletted:
		if len(img.Palette) == 0 {
			readPixel := pixelReaderFor(m)
			return func(x int, y int) uint8 {
				c := readPixel(x, y)
				return rgbToLuma(c.R, c.G, c.B)
			}
		}
		palette := make([]uint8, len(img.Palette))
		for i, c := range img.Palette {
			nrgba := color.NRGBAModel.Convert(c).(color.NRGBA)
			palette[i] = rgbToLuma(nrgba.R, nrgba.G, nrgba.B)
		}
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) uint8 {
			return palette[pix[(y-minY)*stride+x-minX]]
		}
	case *image.Uniform:
		c := color.NRGBAModel.Convert(img.C).(color.NRGBA)
		y := rgbToLuma(c.R, c.G, c.B)
		return func(int, int) uint8 {
			return y
		}
	case image.RGBA64Image:
		return func(x int, y int) uint8 {
			c := nrgbaFromRGBA64(img.RGBA64At(x, y))
			return rgbToLuma(c.R, c.G, c.B)
		}
	default:
		readPixel := pixelReaderFor(m)
		return func(x int, y int) uint8 {
			c := readPixel(x, y)
			return rgbToLuma(c.R, c.G, c.B)
		}
	}
}

func chromaReaderFor(m image.Image) chromaReader {
	switch img := m.(type) {
	case *image.NRGBA:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) (uint8, uint8) {
			i := (y-minY)*stride + (x-minX)*4
			return rgbToChroma(pix[i+0], pix[i+1], pix[i+2])
		}
	case *image.RGBA:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) (uint8, uint8) {
			i := (y-minY)*stride + (x-minX)*4
			if pix[i+3] == 255 {
				return rgbToChroma(pix[i+0], pix[i+1], pix[i+2])
			}
			c := nrgbaFromRGBA(pix[i+0], pix[i+1], pix[i+2], pix[i+3])
			return rgbToChroma(c.R, c.G, c.B)
		}
	case *image.Gray:
		return func(int, int) (uint8, uint8) {
			return 128, 128
		}
	case *image.YCbCr:
		yPix := img.Y
		cbPix := img.Cb
		crPix := img.Cr
		return func(x int, y int) (uint8, uint8) {
			yy := yPix[img.YOffset(x, y)]
			ci := img.COffset(x, y)
			cb := cbPix[ci]
			cr := crPix[ci]
			r, g, b := color.YCbCrToRGB(yy, cb, cr)
			return rgbToChroma(r, g, b)
		}
	case *image.Paletted:
		if len(img.Palette) == 0 {
			readPixel := pixelReaderFor(m)
			return func(x int, y int) (uint8, uint8) {
				c := readPixel(x, y)
				return rgbToChroma(c.R, c.G, c.B)
			}
		}
		palette := make([][2]uint8, len(img.Palette))
		for i, c := range img.Palette {
			nrgba := color.NRGBAModel.Convert(c).(color.NRGBA)
			palette[i][0], palette[i][1] = rgbToChroma(nrgba.R, nrgba.G, nrgba.B)
		}
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) (uint8, uint8) {
			index := pix[(y-minY)*stride+x-minX]
			pair := palette[index]
			return pair[0], pair[1]
		}
	case *image.Uniform:
		c := color.NRGBAModel.Convert(img.C).(color.NRGBA)
		cb, cr := rgbToChroma(c.R, c.G, c.B)
		return func(int, int) (uint8, uint8) {
			return cb, cr
		}
	case image.RGBA64Image:
		return func(x int, y int) (uint8, uint8) {
			c := nrgbaFromRGBA64(img.RGBA64At(x, y))
			return rgbToChroma(c.R, c.G, c.B)
		}
	default:
		readPixel := pixelReaderFor(m)
		return func(x int, y int) (uint8, uint8) {
			c := readPixel(x, y)
			return rgbToChroma(c.R, c.G, c.B)
		}
	}
}

func nrgbaFromRGBA(r uint8, g uint8, b uint8, a uint8) color.NRGBA {
	if a == 0xff {
		return color.NRGBA{R: r, G: g, B: b, A: 0xff}
	}
	if a == 0 {
		return color.NRGBA{}
	}
	r16 := uint32(r)
	r16 |= r16 << 8
	g16 := uint32(g)
	g16 |= g16 << 8
	b16 := uint32(b)
	b16 |= b16 << 8
	a16 := uint32(a)
	a16 |= a16 << 8
	r16 = (r16 * 0xffff) / a16
	g16 = (g16 * 0xffff) / a16
	b16 = (b16 * 0xffff) / a16
	return color.NRGBA{R: uint8(r16 >> 8), G: uint8(g16 >> 8), B: uint8(b16 >> 8), A: a}
}

func nrgbaFromRGBA64(c color.RGBA64) color.NRGBA {
	if c.A == 0xffff {
		return color.NRGBA{R: uint8(c.R >> 8), G: uint8(c.G >> 8), B: uint8(c.B >> 8), A: 0xff}
	}
	if c.A == 0 {
		return color.NRGBA{}
	}
	a := uint32(c.A)
	return color.NRGBA{
		R: uint8((uint32(c.R) * 0xffff / a) >> 8),
		G: uint8((uint32(c.G) * 0xffff / a) >> 8),
		B: uint8((uint32(c.B) * 0xffff / a) >> 8),
		A: uint8(c.A >> 8),
	}
}

func nrgbaFromNRGBA64(c color.NRGBA64) color.NRGBA {
	if c.A == 0xffff {
		return color.NRGBA{R: uint8(c.R >> 8), G: uint8(c.G >> 8), B: uint8(c.B >> 8), A: 0xff}
	}
	if c.A == 0 {
		return color.NRGBA{}
	}
	a := uint32(c.A)
	r := uint32(c.R) * a / 0xffff
	g := uint32(c.G) * a / 0xffff
	b := uint32(c.B) * a / 0xffff
	return color.NRGBA{
		R: uint8((r * 0xffff / a) >> 8),
		G: uint8((g * 0xffff / a) >> 8),
		B: uint8((b * 0xffff / a) >> 8),
		A: uint8(c.A >> 8),
	}
}
