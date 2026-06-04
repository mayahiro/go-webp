package webp

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"
)

type benchmarkImageKind uint8

const (
	benchmarkImageGradient benchmarkImageKind = iota
	benchmarkImagePhotoLike
	benchmarkImageChecker
	benchmarkImageLineArt
	benchmarkImageFlat
	benchmarkImageAlpha
	benchmarkImageAlphaBands
	benchmarkImageColorEdge
)

type lossyBenchmarkCase struct {
	name    string
	kind    benchmarkImageKind
	width   int
	height  int
	quality int
}

func BenchmarkEncodeLossyGradient128(b *testing.B) {
	benchmarkEncodeLossyCase(b, lossyBenchmarkCase{
		name:    "Gradient128Q75",
		kind:    benchmarkImageGradient,
		width:   128,
		height:  128,
		quality: 75,
	})
}

func BenchmarkEncodeLossyAlpha128(b *testing.B) {
	benchmarkEncodeLossyCase(b, lossyBenchmarkCase{
		name:    "Alpha128Q75",
		kind:    benchmarkImageAlpha,
		width:   128,
		height:  128,
		quality: 75,
	})
}

func BenchmarkEncodeLossyAlphaBands512(b *testing.B) {
	benchmarkEncodeLossyCase(b, lossyBenchmarkCase{
		name:    "AlphaBands512Q75",
		kind:    benchmarkImageAlphaBands,
		width:   512,
		height:  512,
		quality: 75,
	})
}

func BenchmarkEncodeLossyGradient1024(b *testing.B) {
	benchmarkEncodeLossyCase(b, lossyBenchmarkCase{
		name:    "Gradient1024Q75",
		kind:    benchmarkImageGradient,
		width:   1024,
		height:  1024,
		quality: 75,
	})
}

func BenchmarkEncodeLossyFixtures(b *testing.B) {
	for _, tc := range lossyBenchmarkCases() {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkEncodeLossyCase(b, tc)
		})
	}
}

func TestEncodeLossyBenchmarkFixtures(t *testing.T) {
	for _, tc := range lossyBenchmarkCases() {
		t.Run(tc.name, func(t *testing.T) {
			img := newBenchmarkFixtureImage(tc)
			data := encodeBenchmarkWebP(t, img, &Options{
				Compression: CompressionLossy,
				Quality:     tc.quality,
			})
			assertLossyBenchmarkWebP(t, data, tc)
		})
	}
}

func lossyBenchmarkCases() []lossyBenchmarkCase {
	return []lossyBenchmarkCase{
		{name: "Gradient128Q1", kind: benchmarkImageGradient, width: 128, height: 128, quality: 1},
		{name: "Gradient128Q50", kind: benchmarkImageGradient, width: 128, height: 128, quality: 50},
		{name: "Gradient128Q75", kind: benchmarkImageGradient, width: 128, height: 128, quality: 75},
		{name: "Gradient128Q90", kind: benchmarkImageGradient, width: 128, height: 128, quality: 90},
		{name: "Gradient128Q100", kind: benchmarkImageGradient, width: 128, height: 128, quality: 100},
		{name: "Gradient512Q75", kind: benchmarkImageGradient, width: 512, height: 512, quality: 75},
		{name: "PhotoLike256Q75", kind: benchmarkImagePhotoLike, width: 256, height: 256, quality: 75},
		{name: "Checker128Q75", kind: benchmarkImageChecker, width: 128, height: 128, quality: 75},
		{name: "LineArt256Q75", kind: benchmarkImageLineArt, width: 256, height: 256, quality: 75},
		{name: "Flat128Q75", kind: benchmarkImageFlat, width: 128, height: 128, quality: 75},
		{name: "Alpha128Q75", kind: benchmarkImageAlpha, width: 128, height: 128, quality: 75},
		{name: "AlphaBands512Q75", kind: benchmarkImageAlphaBands, width: 512, height: 512, quality: 75},
		{name: "ColorEdge128Q75", kind: benchmarkImageColorEdge, width: 128, height: 128, quality: 75},
	}
}

func benchmarkEncodeLossyCase(b *testing.B, tc lossyBenchmarkCase) {
	img := newBenchmarkFixtureImage(tc)
	opts := &Options{Compression: CompressionLossy, Quality: tc.quality}
	inputBytes := img.Bounds().Dx() * img.Bounds().Dy() * 4
	encoded := encodeBenchmarkWebP(b, img, opts)
	yPSNR, uvPSNR := lossyYUVPSNRProxy(img, tc.quality)

	b.SetBytes(int64(inputBytes))
	b.ReportAllocs()

	var buf bytes.Buffer
	b.ResetTimer()
	for b.Loop() {
		buf.Reset()
		if err := Encode(&buf, img, opts); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(encoded)), "encoded_B")
	b.ReportMetric(float64(len(encoded))/float64(inputBytes), "encoded_per_input")
	b.ReportMetric(yPSNR, "y_psnr_proxy")
	b.ReportMetric(uvPSNR, "uv_psnr_proxy")
}

func encodeBenchmarkWebP(tb testing.TB, img image.Image, opts *Options) []byte {
	tb.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, img, opts); err != nil {
		tb.Fatalf("Encode failed: %v", err)
	}
	return buf.Bytes()
}

func assertLossyBenchmarkWebP(t *testing.T, data []byte, tc lossyBenchmarkCase) {
	t.Helper()
	chunks := readWebPChunks(t, data)
	if tc.hasAlpha() {
		if len(chunks) != 3 {
			t.Fatalf("chunk count = %d, want 3", len(chunks))
		}
		if chunks[0].name != "VP8X" {
			t.Fatalf("first chunk = %q, want VP8X", chunks[0].name)
		}
		if chunks[1].name != "ALPH" {
			t.Fatalf("second chunk = %q, want ALPH", chunks[1].name)
		}
		if chunks[2].name != "VP8 " {
			t.Fatalf("third chunk = %q, want VP8 ", chunks[2].name)
		}
		assertLossyVP8Frame(t, chunks[2].payload, tc.width, tc.height)
		return
	}

	if len(chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1", len(chunks))
	}
	if chunks[0].name != "VP8 " {
		t.Fatalf("chunk name = %q, want VP8 ", chunks[0].name)
	}
	assertLossyVP8Frame(t, chunks[0].payload, tc.width, tc.height)
}

func (tc lossyBenchmarkCase) hasAlpha() bool {
	return tc.kind == benchmarkImageAlpha || tc.kind == benchmarkImageAlphaBands
}

func newBenchmarkImage(width int, height int, alpha bool) *image.NRGBA {
	kind := benchmarkImageGradient
	if alpha {
		kind = benchmarkImageAlpha
	}
	return newBenchmarkFixtureImage(lossyBenchmarkCase{
		kind:    kind,
		width:   width,
		height:  height,
		quality: 75,
	})
}

func newBenchmarkFixtureImage(tc lossyBenchmarkCase) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, tc.width, tc.height))
	for y := 0; y < tc.height; y++ {
		for x := 0; x < tc.width; x++ {
			img.SetNRGBA(x, y, benchmarkPixel(tc.kind, x, y))
		}
	}
	return img
}

func benchmarkPixel(kind benchmarkImageKind, x int, y int) color.NRGBA {
	switch kind {
	case benchmarkImagePhotoLike:
		base := (x*3 + y*5 + x*y/29) & 0xff
		return color.NRGBA{
			R: uint8(base + (x*y)%23),
			G: uint8(base/2 + y*2 + (x^y)&31),
			B: uint8(base/3 + x*2 + (x*y)%19),
			A: 255,
		}
	case benchmarkImageChecker:
		if (x/8+y/8)%2 == 0 {
			return color.NRGBA{R: 235, G: 238, B: 242, A: 255}
		}
		return color.NRGBA{R: 28, G: 32, B: 36, A: 255}
	case benchmarkImageLineArt:
		if x%31 == 0 || y%29 == 0 || (x+y)%47 == 0 {
			return color.NRGBA{R: 18, G: 24, B: 32, A: 255}
		}
		return color.NRGBA{R: 244, G: 246, B: 248, A: 255}
	case benchmarkImageFlat:
		return color.NRGBA{R: 96, G: 128, B: 160, A: 255}
	case benchmarkImageAlpha:
		return color.NRGBA{
			R: uint8(x*3 + y),
			G: uint8(y*5 + x/2),
			B: uint8((x+y)*2 + x*y/17),
			A: uint8(96 + (x*5+y*7)%160),
		}
	case benchmarkImageAlphaBands:
		alpha := uint8(48)
		if (x/64+y/16)%2 == 1 {
			alpha = 224
		}
		return color.NRGBA{
			R: uint8(x*3 + y),
			G: uint8(y*5 + x/2),
			B: uint8((x+y)*2 + x*y/17),
			A: alpha,
		}
	case benchmarkImageColorEdge:
		if (x/16+y/16)%2 == 0 {
			return color.NRGBA{R: 220, G: 36, B: 28, A: 255}
		}
		return color.NRGBA{R: 32, G: 56, B: 224, A: 255}
	default:
		return color.NRGBA{
			R: uint8(x*3 + y),
			G: uint8(y*5 + x/2),
			B: uint8((x+y)*2 + x*y/17),
			A: 255,
		}
	}
}

func lossyYUVPSNRProxy(m image.Image, quality int) (float64, float64) {
	bounds := m.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	mbw := (width + 15) >> 4
	mbh := (height + 15) >> 4
	readPixel := pixelReaderFor(m)
	quant := vp8QuantForIndex(qualityToVP8QIndex(quality))
	work := newVP8EncodeBuffers(mbw, mbh)
	modes := analyzeVP8Modes(readPixel, bounds, mbw, mbh, quant, work)
	work.clear()

	yStride := mbw * 16
	cStride := mbw * 8
	for mby := 0; mby < mbh; mby++ {
		for mbx := 0; mbx < mbw; mbx++ {
			mode := modes[mby*mbw+mbx]
			reconstructVP8LumaMB(readPixel, bounds, mbx, mby, work.recY, yStride, quant, mode)
			reconstructVP8ChromaMB(readPixel, bounds, mbx, mby, work.recCb, work.recCr, cStride, quant, mode)
		}
	}

	var yErr float64
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c := samplePixel(readPixel, bounds, x, y)
			got := work.recY[y*yStride+x]
			yErr += squareFloat(float64(int(rgbToLuma(c.R, c.G, c.B)) - int(got)))
		}
	}

	chromaWidth := (width + 1) >> 1
	chromaHeight := (height + 1) >> 1
	var uvErr float64
	for y := 0; y < chromaHeight; y++ {
		for x := 0; x < chromaWidth; x++ {
			wantCb := chromaSample(readPixel, bounds, x*2, y*2, true)
			wantCr := chromaSample(readPixel, bounds, x*2, y*2, false)
			gotCb := work.recCb[y*cStride+x]
			gotCr := work.recCr[y*cStride+x]
			uvErr += squareFloat(float64(int(wantCb) - int(gotCb)))
			uvErr += squareFloat(float64(int(wantCr) - int(gotCr)))
		}
	}

	yMSE := yErr / float64(width*height)
	uvMSE := uvErr / float64(chromaWidth*chromaHeight*2)
	return psnr(yMSE), psnr(uvMSE)
}

func psnr(mse float64) float64 {
	if mse == 0 {
		return 99.99
	}
	return 10 * math.Log10(255*255/mse)
}

func squareFloat(v float64) float64 {
	return v * v
}
