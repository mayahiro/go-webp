package webp

import (
	"fmt"
	"image"
	"image/color"
	"testing"
)

func TestVP8SharpChromaReadsEachPixelOnce(t *testing.T) {
	for _, size := range []image.Point{{X: 1, Y: 1}, {X: 1, Y: 3}, {X: 3, Y: 1}, {X: 3, Y: 5}, {X: 16, Y: 16}} {
		t.Run(fmt.Sprintf("%dx%d", size.X, size.Y), func(t *testing.T) {
			bounds := image.Rect(-3, 5, size.X-3, size.Y+5)
			img := image.NewNRGBA(bounds.Inset(-1)).SubImage(bounds).(*image.NRGBA)
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 37), G: uint8(y * 53), B: uint8(x*y + 71), A: 255})
				}
			}
			source := newEncoderSource(img)
			prepared := newVP8Source(source, true)
			reads := make([]int, size.X*size.Y)
			reader := source.pixels()
			prepared.applySharpChroma(func(x, y int) color.NRGBA {
				if !image.Pt(x, y).In(bounds) {
					t.Fatalf("read outside the source bounds at (%d,%d)", x, y)
				}
				reads[(y-bounds.Min.Y)*size.X+x-bounds.Min.X]++
				return reader(x, y)
			})
			for i, count := range reads {
				if count != 1 {
					t.Fatalf("pixel %d read %d times, want once", i, count)
				}
			}
		})
	}
}

func BenchmarkVP8SharpChroma(b *testing.B) {
	for _, fixture := range []struct {
		name string
		img  image.Image
	}{
		{name: "NRGBA", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImagePhotoLike, width: 512, height: 512})},
		{name: "YCbCr", img: newBenchmarkYCbCrFixtureImage(512, 512)},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			source := newEncoderSource(fixture.img)
			prepared := newVP8Source(source, true)
			original := append([]uint8(nil), prepared.plane.data...)
			reader := source.pixels()
			b.ReportAllocs()
			for b.Loop() {
				copy(prepared.plane.data, original)
				prepared.applySharpChroma(reader)
			}
		})
	}
}
