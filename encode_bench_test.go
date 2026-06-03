package webp

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func BenchmarkEncodeLossyGradient128(b *testing.B) {
	img := newBenchmarkImage(128, 128, false)
	opts := &Options{Compression: CompressionLossy, Quality: 75}
	b.SetBytes(int64(img.Bounds().Dx() * img.Bounds().Dy() * 4))
	b.ReportAllocs()

	var buf bytes.Buffer
	for b.Loop() {
		buf.Reset()
		if err := Encode(&buf, img, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeLossyAlpha128(b *testing.B) {
	img := newBenchmarkImage(128, 128, true)
	opts := &Options{Compression: CompressionLossy, Quality: 75}
	b.SetBytes(int64(img.Bounds().Dx() * img.Bounds().Dy() * 4))
	b.ReportAllocs()

	var buf bytes.Buffer
	for b.Loop() {
		buf.Reset()
		if err := Encode(&buf, img, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func newBenchmarkImage(width int, height int, alpha bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			a := uint8(255)
			if alpha {
				a = uint8(96 + (x*5+y*7)%160)
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*3 + y),
				G: uint8(y*5 + x/2),
				B: uint8((x+y)*2 + x*y/17),
				A: a,
			})
		}
	}
	return img
}
