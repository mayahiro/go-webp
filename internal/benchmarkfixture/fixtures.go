// Package benchmarkfixture provides deterministic generated images for encoder comparisons
package benchmarkfixture

import (
	"image"
	"image/color"
)

// Fixture is a named deterministic comparison image
type Fixture struct {
	Name  string
	Image image.Image
}

// Standard returns the shared lossless and lossy comparison corpus
func Standard() []Fixture {
	return []Fixture{
		{Name: "gradient128", Image: gradient(128, 128)},
		{Name: "ui256", Image: ui(256, 256)},
		{Name: "flat128", Image: flat(128, 128)},
		{Name: "palette256", Image: palette(256, 256)},
		{Name: "alpha128", Image: alpha(128, 128)},
		{Name: "photo512", Image: photoLike(512, 512)},
	}
}

func gradient(width int, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	fill(img, func(x int, y int) color.NRGBA {
		return color.NRGBA{
			R: uint8((x*3 + y) & 0xff),
			G: uint8((x + y*5) & 0xff),
			B: uint8((x*7 + y*11) & 0xff),
			A: 255,
		}
	})
	return img
}

func ui(width int, height int) *image.NRGBA {
	colors := []color.NRGBA{
		{R: 22, G: 27, B: 34, A: 255},
		{R: 36, G: 41, B: 47, A: 255},
		{R: 87, G: 166, B: 74, A: 255},
		{R: 210, G: 214, B: 220, A: 255},
		{R: 246, G: 248, B: 250, A: 255},
		{R: 48, G: 116, B: 190, A: 255},
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	fill(img, func(x int, y int) color.NRGBA {
		if x < width/8 || y < height/10 {
			return colors[0]
		}
		if (x/24+y/18)%7 == 0 {
			return colors[2]
		}
		if (x/48+y/32)%5 == 0 {
			return colors[5]
		}
		if (x+y)%19 == 0 {
			return colors[3]
		}
		return colors[(x/64+y/40)%2+3]
	})
	return img
}

func flat(width int, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	fill(img, func(int, int) color.NRGBA {
		return color.NRGBA{R: 16, G: 32, B: 48, A: 255}
	})
	return img
}

func palette(width int, height int) *image.Paletted {
	colors := make(color.Palette, 16)
	for i := range colors {
		colors[i] = color.NRGBA{
			R: uint8(i * 17),
			G: uint8((i*37 + 19) & 0xff),
			B: uint8((i*53 + 7) & 0xff),
			A: 255,
		}
	}
	img := image.NewPaletted(image.Rect(0, 0, width, height), colors)
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetColorIndex(x, y, uint8((x/4+y/4+x*y)%len(colors)))
		}
	}
	return img
}

func alpha(width int, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	fill(img, func(x int, y int) color.NRGBA {
		return color.NRGBA{
			R: uint8((x*5 + y*2) & 0xff),
			G: uint8((x*3 + y*7 + 11) & 0xff),
			B: uint8((x*13 + y*17 + 29) & 0xff),
			A: uint8(64 + (x*3+y*5)%192),
		}
	})
	return img
}

func photoLike(width int, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	fill(img, func(x int, y int) color.NRGBA {
		base := (x*37 + y*53 + x*y*3) & 0xff
		return color.NRGBA{
			R: uint8((base + x/3 + y/5) & 0xff),
			G: uint8((base + x/7 + y/2 + 17) & 0xff),
			B: uint8((base + x/5 + y/11 + 41) & 0xff),
			A: 255,
		}
	})
	return img
}

func fill(img *image.NRGBA, fn func(x int, y int) color.NRGBA) {
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, fn(x-img.Rect.Min.X, y-img.Rect.Min.Y))
		}
	}
}
