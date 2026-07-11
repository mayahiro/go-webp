package benchmarkbitstream_test

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	webp "github.com/mayahiro/go-webp"
	"github.com/mayahiro/go-webp/internal/benchmarkbitstream"
)

func TestParseLosslessClassifiesEncoderOutput(t *testing.T) {
	cases := []struct {
		name string
		img  image.Image
	}{
		{name: "flat", img: losslessFlatFixture(16, 16)},
		{name: "palette", img: losslessPaletteFixture(64, 64)},
		{name: "repeating", img: losslessRepeatingFixture(512, 8)},
	}
	modes := []struct {
		name string
		mode webp.Mode
	}{
		{name: "default", mode: webp.ModeDefault},
		{name: "best", mode: webp.ModeBestCompression},
	}
	for _, tc := range cases {
		for _, mode := range modes {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				var encoded bytes.Buffer
				if err := webp.Encode(&encoded, tc.img, &webp.Options{Compression: webp.CompressionLossless, Mode: mode.mode}); err != nil {
					t.Fatal(err)
				}
				layout, err := benchmarkbitstream.ParseLossless(encoded.Bytes())
				if err != nil {
					t.Fatal(err)
				}
				bounds := tc.img.Bounds()
				if layout.Width != bounds.Dx() || layout.Height != bounds.Dy() {
					t.Fatalf("dimensions = %dx%d, want %dx%d", layout.Width, layout.Height, bounds.Dx(), bounds.Dy())
				}
				if got, want := layout.ClassifiedBits(), uint64(layout.VP8LPayloadBytes)*8; got != want {
					t.Fatalf("classified bits = %d, want %d", got, want)
				}
				if got, want := layout.CodedPixels, layout.CodedWidth*layout.Height; got != want {
					t.Fatalf("coded pixels = %d, want %d", got, want)
				}
				if got := layout.LiteralTokens + layout.ColorCacheTokens + layout.CopyPixels; got != layout.CodedPixels {
					t.Fatalf("produced pixels = %d, want %d", got, layout.CodedPixels)
				}
				if layout.EntropyGroups < 1 || layout.ImageHeaderBits != 40 {
					t.Fatalf("layout metadata = %#v", layout)
				}
			})
		}
	}
}

func TestParseLosslessRejectsInvalidData(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		[]byte("not a WebP file"),
		[]byte("RIFF\x04\x00\x00\x00WEBP"),
	} {
		if _, err := benchmarkbitstream.ParseLossless(data); err == nil {
			t.Fatalf("ParseLossless(%q) succeeded", data)
		}
	}
}

func TestLosslessLayoutAdd(t *testing.T) {
	left := benchmarkbitstream.LosslessLayout{FileBytes: 10, LiteralBits: 20, LiteralTokens: 3, CodedPixels: 3}
	right := benchmarkbitstream.LosslessLayout{FileBytes: 15, LiteralBits: 30, CopyBits: 4, CopyTokens: 1, CopyPixels: 7, CodedPixels: 7}
	left.Add(right)
	if left.FileBytes != 25 || left.LiteralBits != 50 || left.CopyBits != 4 {
		t.Fatalf("layout bits = %#v", left)
	}
	if left.LiteralTokens != 3 || left.CopyTokens != 1 || left.CopyPixels != 7 || left.CodedPixels != 10 {
		t.Fatalf("layout tokens = %#v", left)
	}
}

func losslessFlatFixture(width int, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, color.NRGBA{R: 31, G: 63, B: 95, A: 255})
		}
	}
	return img
}

func losslessPaletteFixture(width int, height int) *image.Paletted {
	palette := color.Palette{
		color.NRGBA{R: 12, G: 34, B: 56, A: 255},
		color.NRGBA{R: 220, G: 180, B: 40, A: 255},
		color.NRGBA{R: 50, G: 170, B: 210, A: 255},
		color.NRGBA{R: 190, G: 40, B: 120, A: 128},
	}
	img := image.NewPaletted(image.Rect(0, 0, width, height), palette)
	for y := range height {
		for x := range width {
			img.SetColorIndex(x, y, uint8((x/4+y/4)%len(palette)))
		}
	}
	return img
}

func losslessRepeatingFixture(width int, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			value := x % 512
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(value),
				G: uint8(value >> 8),
				B: uint8(value*73 + value>>2),
				A: 255,
			})
		}
	}
	return img
}
