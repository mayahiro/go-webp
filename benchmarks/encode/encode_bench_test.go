package encode_test

import (
	"bytes"
	"image"
	"strconv"
	"testing"

	webp "github.com/mayahiro/go-webp"
	"github.com/mayahiro/go-webp/benchmarks/internal/benchmarkfixture"
)

func TestFixturesEncodeThroughPublicAPI(t *testing.T) {
	for _, fixture := range benchmarkfixture.Standard() {
		t.Run(fixture.Name, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				options *webp.Options
			}{
				{name: "lossless", options: &webp.Options{Compression: webp.CompressionLossless}},
				{name: "lossy", options: &webp.Options{Compression: webp.CompressionLossy, Quality: 75}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var output bytes.Buffer
					if err := webp.Encode(&output, fixture.Image, tc.options); err != nil {
						t.Fatal(err)
					}
					data := output.Bytes()
					if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
						t.Fatal("Encode returned an invalid WebP container")
					}
				})
			}
		})
	}
}

func BenchmarkEncodeLossyFixtures(b *testing.B) {
	for _, fixture := range benchmarkfixture.Standard() {
		qualities := []int{75}
		if fixture.Name == "gradient128" {
			qualities = []int{1, 50, 75, 90, 100}
		}
		for _, quality := range qualities {
			b.Run(fixture.Name+"/Q"+strconv.Itoa(quality), func(b *testing.B) {
				benchmarkEncode(b, fixture.Image, &webp.Options{
					Compression: webp.CompressionLossy,
					Quality:     quality,
				})
			})
		}
	}
}

func BenchmarkEncodeLosslessFixtures(b *testing.B) {
	for _, fixture := range benchmarkfixture.Standard() {
		b.Run(fixture.Name, func(b *testing.B) {
			benchmarkEncode(b, fixture.Image, &webp.Options{Compression: webp.CompressionLossless})
		})
	}
}

func benchmarkEncode(b *testing.B, img image.Image, options *webp.Options) {
	var validation bytes.Buffer
	if err := webp.Encode(&validation, img, options); err != nil {
		b.Fatal(err)
	}
	encodedBytes := validation.Len()
	inputBytes := benchmarkInputBytes(img)

	var output bytes.Buffer
	output.Grow(encodedBytes)
	b.SetBytes(int64(inputBytes))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		output.Reset()
		if err := webp.Encode(&output, img, options); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(encodedBytes), "encoded_B")
	b.ReportMetric(float64(encodedBytes)/float64(inputBytes), "encoded_per_input")
}

func benchmarkInputBytes(img image.Image) int {
	switch img := img.(type) {
	case *image.NRGBA:
		return len(img.Pix)
	case *image.RGBA:
		return len(img.Pix)
	case *image.Gray:
		return len(img.Pix)
	case *image.YCbCr:
		return len(img.Y) + len(img.Cb) + len(img.Cr)
	case *image.Paletted:
		return len(img.Pix) + len(img.Palette)*4
	default:
		bounds := img.Bounds()
		return bounds.Dx() * bounds.Dy() * 4
	}
}
