package webp

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func FuzzEncodeNearLossless(f *testing.F) {
	// Exercise both sides of the preprocessing threshold, including thin images.
	dimensions := [...]int{1, 2, 3, 63, 64, 65}
	for i, quality := range []uint8{1, 20, 40, 60, 80, 100} {
		f.Add(uint8(i%5), uint8(i), uint8(5), quality-1, int8(-2), int8(3), uint8(7), false, []byte{7, 113, 251, 31, 67})
		f.Add(uint8(i%5), uint8(5), uint8(i), quality-1, int8(3), int8(-2), uint8(5), true, []byte{})
	}
	f.Add(uint8(0), uint8(3), uint8(3), uint8(0), int8(0), int8(0), uint8(0), false, []byte{})
	f.Add(uint8(0), uint8(4), uint8(4), uint8(0), int8(-1), int8(1), uint8(9), false, []byte{})
	f.Fuzz(func(t *testing.T, kind uint8, rawWidth uint8, rawHeight uint8, rawQuality uint8, originX int8, originY int8, padding uint8, opaque bool, data []byte) {
		width := dimensions[int(rawWidth)%len(dimensions)]
		height := dimensions[int(rawHeight)%len(dimensions)]
		x, y := int(originX%4), int(originY%4)
		img := fuzzImage(kind%5, image.Rect(x, y, x+width, y+height), int(padding%12), opaque, data)
		bounds := img.Bounds()
		opts := &Options{Mode: ModeNearLossless, Quality: int(rawQuality%100) + 1}
		encoded := encodeFuzzImage(t, img, opts)
		if !bytes.Equal(encoded, encodeFuzzImage(t, img, opts)) {
			t.Fatal("non-deterministic near-lossless output")
		}
		validateFuzzWebP(t, encoded, width, height)
		pixels, decodedWidth, decodedHeight, _, err := decodeEncoderOutput(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if decodedWidth != width || decodedHeight != height || len(pixels) != width*height {
			t.Fatalf("decoded dimensions = %dx%d with %d pixels, want %dx%d", decodedWidth, decodedHeight, len(pixels), width, height)
		}
		// Bound cumulative rounding across all quantization passes, independently
		// of the encoder's preprocessing implementation.
		maxDelta := (1 << (5 - opts.Quality/20)) - 1
		if opts.Quality == 100 || width < 64 && height < 64 || height < 3 {
			maxDelta = 0
		}
		for py := range height {
			for px := range width {
				want := color.NRGBAModel.Convert(img.At(bounds.Min.X+px, bounds.Min.Y+py)).(color.NRGBA)
				got := pixels[py*width+px]
				limit := maxDelta
				if px == 0 || py == 0 || px == width-1 || py == height-1 {
					limit = 0
				}
				if got.A != want.A || absForFuzz(int(got.R)-int(want.R)) > limit ||
					absForFuzz(int(got.G)-int(want.G)) > limit || absForFuzz(int(got.B)-int(want.B)) > limit {
					t.Fatalf("pixel (%d,%d) = %v, source %v, RGB error limit %d with exact alpha", px, py, got, want, limit)
				}
			}
		}
	})
}

func FuzzEncodeLossyMacroblocks(f *testing.F) {
	dimensions := [...]int{15, 16, 17, 31, 32, 33}
	modes := [...]Mode{ModeDefault, ModeFast, ModeBalanced, ModeBestCompression, ModeLowMemory, ModeLossyQuality, ModeAuto}
	for i := range modes {
		for _, quality := range []uint8{1, 75, 100} {
			f.Add(uint8(i%5), uint8(i), uint8(i%len(dimensions)), uint8((i+2)%len(dimensions)), quality-1, int8(-3), int8(2), uint8(7), i%2 == 0, []byte{17, 251, 71, 137, 3})
		}
	}
	f.Fuzz(func(t *testing.T, kind uint8, rawMode uint8, rawWidth uint8, rawHeight uint8, rawQuality uint8, originX int8, originY int8, padding uint8, opaque bool, data []byte) {
		width := dimensions[int(rawWidth)%len(dimensions)]
		height := dimensions[int(rawHeight)%len(dimensions)]
		x, y := int(originX%4), int(originY%4)
		img := fuzzImage(kind%5, image.Rect(x, y, x+width, y+height), int(padding%12), opaque, data)
		opts := &Options{Compression: CompressionLossy, Mode: modes[int(rawMode)%len(modes)], Quality: int(rawQuality%100) + 1}
		encoded := encodeFuzzImage(t, img, opts)
		if !bytes.Equal(encoded, encodeFuzzImage(t, img, opts)) {
			t.Fatal("non-deterministic lossy output across macroblock boundaries")
		}
		validateFuzzWebP(t, encoded, width, height)
	})
}
