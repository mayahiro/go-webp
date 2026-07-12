package webp

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"os/exec"
	"slices"
	"testing"
)

func TestVP8BoolEncoderEqualProbMatchesWriteBit(t *testing.T) {
	bits := []bool{
		false, true, true, false, true, false, false, true,
		true, true, false, false, false, true, false, true,
		false, false, true, true, true, false, true, false,
	}
	generic := newVP8BoolEncoder()
	equal := newVP8BoolEncoder()
	for i, bit := range bits {
		generic.writeBit(128, bit)
		equal.writeBitEqualProb(bit)
		if generic.range_ != equal.range_ || generic.bottom != equal.bottom || generic.bitCount != equal.bitCount || !bytes.Equal(generic.out, equal.out) {
			t.Fatalf("state after bit %d differs: generic={range:%d bottom:%d bitCount:%d out:%v} equal={range:%d bottom:%d bitCount:%d out:%v}", i, generic.range_, generic.bottom, generic.bitCount, generic.out, equal.range_, equal.bottom, equal.bitCount, equal.out)
		}
	}
	if got, want := equal.bytes(), generic.bytes(); !bytes.Equal(got, want) {
		t.Fatalf("bytes = %v, want %v", got, want)
	}
}

func TestEncodeRoundTripNRGBA(t *testing.T) {
	img := image.NewNRGBA(image.Rect(10, 20, 12, 22))
	want := []color.NRGBA{
		{R: 1, G: 2, B: 3, A: 4},
		{R: 5, G: 6, B: 7, A: 8},
		{R: 9, G: 10, B: 11, A: 12},
		{R: 13, G: 14, B: 15, A: 16},
	}
	i := 0
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, want[i])
			i++
		}
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, nil); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != 2 || height != 2 {
		t.Fatalf("dimensions = %dx%d, want 2x2", width, height)
	}
	if !alpha {
		t.Fatal("alpha hint = false, want true")
	}
	if len(got) != len(want) {
		t.Fatalf("decoded pixel count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pixel %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestEncodeModeDefaultMatchesNilOptions(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 17),
				G: uint8(y * 19),
				B: uint8((x + y) * 11),
				A: 255,
			})
		}
	}

	var nilOptions bytes.Buffer
	if err := Encode(&nilOptions, img, nil); err != nil {
		t.Fatalf("Encode nil options failed: %v", err)
	}
	var modeDefault bytes.Buffer
	if err := Encode(&modeDefault, img, &Options{Mode: ModeDefault}); err != nil {
		t.Fatalf("Encode ModeDefault failed: %v", err)
	}
	if !bytes.Equal(modeDefault.Bytes(), nilOptions.Bytes()) {
		t.Fatal("ModeDefault output differed from nil options")
	}
}

func TestEncodeModeLossyQualityOverridesCompression(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 13),
				G: uint8(y * 17),
				B: uint8((x + y) * 7),
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	err := Encode(&buf, img, &Options{
		Compression: CompressionLossless,
		Quality:     75,
		Mode:        ModeLossyQuality,
	})
	if err != nil {
		t.Fatalf("Encode ModeLossyQuality failed: %v", err)
	}
	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 1 || chunks[0].name != "VP8 " {
		t.Fatalf("chunks = %#v, want a single VP8 chunk", chunks)
	}
}

func TestEncodeModeNearLosslessKeepsSmallImage(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	src := []color.NRGBA{
		{R: 7, G: 25, B: 51, A: 0},
		{R: 63, G: 127, B: 191, A: 128},
		{R: 250, G: 240, B: 230, A: 255},
	}
	for x, c := range src {
		img.SetNRGBA(x, 0, c)
	}

	var buf bytes.Buffer
	const quality = 50
	if err := Encode(&buf, img, &Options{Mode: ModeNearLossless, Quality: quality}); err != nil {
		t.Fatalf("Encode ModeNearLossless failed: %v", err)
	}
	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != 3 || height != 1 {
		t.Fatalf("dimensions = %dx%d, want 3x1", width, height)
	}
	if !alpha {
		t.Fatal("alpha hint = false, want true")
	}
	for i, c := range src {
		if got[i] != c {
			t.Fatalf("pixel %d = %#v, want %#v", i, got[i], c)
		}
	}
}

func TestNearLosslessReaderUsesLocalEdgeQuantization(t *testing.T) {
	img := image.NewNRGBA(image.Rect(5, 7, 69, 71))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			value := uint8(103)
			if x-img.Rect.Min.X >= 32 {
				value = 203
			}
			img.SetNRGBA(x, y, color.NRGBA{R: value, G: value, B: value, A: uint8(x + y)})
		}
	}

	readPixel := newNearLosslessReader(newEncoderSource(img), 50)
	if got := readPixel(10, 20); got.R != 103 {
		t.Fatalf("smooth pixel R = %d, want 103", got.R)
	}
	if got := readPixel(36, 20); got.R != 104 {
		t.Fatalf("left edge pixel R = %d, want 104", got.R)
	}
	if got := readPixel(37, 20); got.R != 200 {
		t.Fatalf("right edge pixel R = %d, want 200", got.R)
	}
	if got := readPixel(36, img.Rect.Min.Y); got.R != 103 {
		t.Fatalf("border pixel R = %d, want 103", got.R)
	}
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			if got, want := readPixel(x, y).A, img.NRGBAAt(x, y).A; got != want {
				t.Fatalf("alpha at (%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}

func TestNearLosslessQualityBands(t *testing.T) {
	cases := []struct {
		quality int
		bits    int
	}{
		{quality: 100, bits: 0},
		{quality: 99, bits: 1},
		{quality: 80, bits: 1},
		{quality: 79, bits: 2},
		{quality: 60, bits: 2},
		{quality: 59, bits: 3},
		{quality: 40, bits: 3},
		{quality: 39, bits: 4},
		{quality: 20, bits: 4},
		{quality: 19, bits: 5},
		{quality: 1, bits: 5},
	}
	for _, tc := range cases {
		if got := nearLosslessQuantizationBits(tc.quality); got != tc.bits {
			t.Errorf("quality %d bits = %d, want %d", tc.quality, got, tc.bits)
		}
	}
}

func TestEncodeNearLosslessQuality100MatchesDefaultLossless(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageGradient, width: 64, height: 64})
	var lossless bytes.Buffer
	if err := Encode(&lossless, img, nil); err != nil {
		t.Fatalf("default lossless Encode failed: %v", err)
	}
	var nearLossless bytes.Buffer
	if err := Encode(&nearLossless, img, &Options{Mode: ModeNearLossless, Quality: 100}); err != nil {
		t.Fatalf("near-lossless quality 100 Encode failed: %v", err)
	}
	if !bytes.Equal(nearLossless.Bytes(), lossless.Bytes()) {
		t.Fatalf("near-lossless quality 100 = %d bytes, want default lossless %d bytes", nearLossless.Len(), lossless.Len())
	}
}

func TestEncodeRejectsUnsupportedMode(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	var buf bytes.Buffer
	err := Encode(&buf, img, &Options{Mode: Mode(99)})
	if err == nil {
		t.Fatal("Encode succeeded with unsupported mode")
	}
}

func TestEncodeLosslessModesRoundTrip(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{
		kind:   benchmarkImageUI,
		width:  64,
		height: 64,
	})
	for _, mode := range []Mode{
		ModeFast,
		ModeBalanced,
		ModeBestCompression,
		ModeLowMemory,
		ModeAuto,
	} {
		t.Run(fmt.Sprintf("mode-%d", mode), func(t *testing.T) {
			data := encodeBenchmarkWebP(t, img, &Options{Mode: mode})
			assertLosslessBenchmarkWebP(t, data, img)
		})
	}
}

func TestLosslessStandardAndWrappedImageOutputsMatch(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImagePhotoLike, width: 64, height: 64})
	wrapped := benchmarkImageWrapper{Image: img}
	var parallel bytes.Buffer
	if err := Encode(&parallel, img, &Options{Mode: ModeDefault}); err != nil {
		t.Fatalf("standard image Encode failed: %v", err)
	}
	var sequential bytes.Buffer
	if err := Encode(&sequential, wrapped, &Options{Mode: ModeDefault}); err != nil {
		t.Fatalf("wrapped image Encode failed: %v", err)
	}
	if !bytes.Equal(parallel.Bytes(), sequential.Bytes()) {
		t.Fatalf("standard image output = %d bytes, want wrapped image output %d bytes", parallel.Len(), sequential.Len())
	}
}

func TestParallelModeOutputsAreDeterministic(t *testing.T) {
	cases := []struct {
		name string
		img  image.Image
		opts *Options
	}{
		{
			name: "lossless-best",
			img:  newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageUI, width: 64, height: 64}),
			opts: &Options{Mode: ModeBestCompression},
		},
		{
			name: "lossy-alpha-best",
			img:  newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageAlpha, width: 64, height: 64}),
			opts: &Options{Compression: CompressionLossy, Mode: ModeBestCompression, Quality: 75},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var first bytes.Buffer
			if err := Encode(&first, tc.img, tc.opts); err != nil {
				t.Fatalf("first Encode failed: %v", err)
			}
			for run := 0; run < 3; run++ {
				var got bytes.Buffer
				if err := Encode(&got, tc.img, tc.opts); err != nil {
					t.Fatalf("Encode run %d failed: %v", run, err)
				}
				if !bytes.Equal(got.Bytes(), first.Bytes()) {
					t.Fatalf("run %d output differs: got %d bytes, want %d bytes", run, got.Len(), first.Len())
				}
			}
		})
	}
}

func TestVP8ReconstructionRowRingMatchesFullFrameBuffers(t *testing.T) {
	const width, height = 80, 64
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImagePhotoLike, width: width, height: height})
	bounds := img.Bounds()
	readLuma := lumaReaderFor(img)
	readChroma := chromaReaderFor(img)
	mbw := (width + 15) >> 4
	mbh := (height + 15) >> 4
	cfg := vp8LossyConfigForModeQuality(ModeDefault, 75)

	ring := newVP8EncodeBuffers(mbw, mbh)
	if got, want := len(ring.recY), mbw*16*32; got != want {
		t.Fatalf("luma ring bytes = %d, want %d", got, want)
	}
	if got, want := len(ring.recCb)+len(ring.recCr), 2*mbw*8*16; got != want {
		t.Fatalf("chroma ring bytes = %d, want %d", got, want)
	}

	full := newVP8EncodeBuffers(mbw, mbh)
	full.recY = make([]uint8, mbw*16*mbh*16)
	full.recCb = make([]uint8, mbw*8*mbh*8)
	full.recCr = make([]uint8, mbw*8*mbh*8)
	ringModes := analyzeVP8ModesConfig(readLuma, readChroma, bounds, mbw, mbh, cfg, ring)
	fullModes := analyzeVP8ModesConfig(readLuma, readChroma, bounds, mbw, mbh, cfg, full)
	if !slices.Equal(ringModes, fullModes) {
		t.Fatal("row-ring mode selection differs from full-frame reconstruction")
	}

	ringStats := collectVP8TokenStats(readLuma, readChroma, bounds, mbw, mbh, cfg.quant, ringModes, ring)
	fullStats := collectVP8TokenStats(readLuma, readChroma, bounds, mbw, mbh, cfg.quant, fullModes, full)
	if ringStats != fullStats {
		t.Fatal("row-ring token statistics differ from full-frame reconstruction")
	}
	tokenProbs := chooseVP8TokenProbs(&ringStats)
	ringResiduals := encodeVP8Residuals(readLuma, readChroma, bounds, width, height, mbw, mbh, cfg.quant, ringModes, ring, &tokenProbs)
	fullResiduals := encodeVP8Residuals(readLuma, readChroma, bounds, width, height, mbw, mbh, cfg.quant, fullModes, full, &tokenProbs)
	if !bytes.Equal(ringResiduals, fullResiduals) {
		t.Fatal("row-ring residual stream differs from full-frame reconstruction")
	}
}

func TestLossyParallelAlphaOnlyUsesStandardImages(t *testing.T) {
	standard := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	if !lossySourceSupportsParallelRead(standard) {
		t.Fatal("standard NRGBA image disabled parallel alpha analysis")
	}
	if lossySourceSupportsParallelRead(benchmarkImageWrapper{Image: standard}) {
		t.Fatal("custom image wrapper enabled parallel alpha analysis")
	}
}

func TestVP8LAutoLosslessModeClassifiesLargeLowColorImagesAsFast(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{
		kind:   benchmarkImageChecker,
		width:  512,
		height: 512,
	})
	readPixel := pixelReaderFor(img)
	bounds := img.Bounds()
	source := newVP8LSource(newEncoderSource(img), readPixel)
	alpha, table, ok := vp8lStreamingSourceInfo(source)
	if !ok {
		t.Fatal("vp8lStreamingSourceInfo returned false")
	}
	colorIndexBits := vp8lAutoPalettePlanBits(source, alpha, table)
	gotMode, gotReason := vp8lAutoLosslessProfile(img, readPixel, bounds, bounds.Dx(), bounds.Dy())
	if gotMode != ModeFast {
		t.Fatalf("vp8lAutoLosslessProfile mode = %d, want ModeFast; colorIndexBits=%d pixels=%d", gotMode, colorIndexBits, bounds.Dx()*bounds.Dy())
	}
	if gotReason != vp8lAutoLosslessReasonLargeLowColor {
		t.Fatalf("vp8lAutoLosslessProfile reason = %d, want large low-color", gotReason)
	}
}

func TestVP8LAutoLosslessModeKeepsSmallLowColorImagesBalanced(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{
		kind:   benchmarkImageChecker,
		width:  128,
		height: 128,
	})
	readPixel := pixelReaderFor(img)
	bounds := img.Bounds()

	if got := vp8lAutoLosslessMode(img, readPixel, bounds, bounds.Dx(), bounds.Dy()); got != ModeBalanced {
		t.Fatalf("vp8lAutoLosslessMode = %d, want ModeBalanced", got)
	}
}

func TestVP8LAutoLosslessModeAvoidsFastWhenIndexedPayloadIsLarge(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{
		kind:   benchmarkImageUI,
		width:  1024,
		height: 1024,
	})
	readPixel := pixelReaderFor(img)
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	source := newVP8LSource(newEncoderSource(img), readPixel)
	alpha, table, ok := vp8lStreamingSourceInfo(source)
	if !ok {
		t.Fatal("vp8lStreamingSourceInfo returned false")
	}
	colorIndexBits := vp8lAutoPalettePlanBits(source, alpha, table)
	if vp8lAutoFastColorIndexPayloadIsSmall(colorIndexBits, width*height) {
		t.Fatalf("color-index payload bits = %d, want too large for Auto Fast", colorIndexBits)
	}

	gotMode, gotReason := vp8lAutoLosslessProfile(img, readPixel, bounds, width, height)
	if gotMode != ModeBalanced {
		t.Fatalf("vp8lAutoLosslessProfile mode = %d, want ModeBalanced", gotMode)
	}
	if gotReason != vp8lAutoLosslessReasonUILike {
		t.Fatalf("vp8lAutoLosslessProfile reason = %d, want UI-like", gotReason)
	}
}

func TestVP8LAutoLosslessModeRejectsSampledLowColorFalsePositive(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x),
				G: uint8(y),
				B: uint8(x*31 + y*17),
				A: 255,
			})
		}
	}
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x += 128 {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x / 128),
				A: 255,
			})
		}
	}

	readPixel := pixelReaderFor(img)
	bounds := img.Bounds()
	if got := vp8lSampleUniqueColors(readPixel, bounds, bounds.Dx()); got > 16 {
		t.Fatalf("vp8lSampleUniqueColors = %d, want <= 16", got)
	}
	gotMode, gotReason := vp8lAutoLosslessProfile(img, readPixel, bounds, bounds.Dx(), bounds.Dy())
	if gotMode != ModeBalanced {
		t.Fatalf("vp8lAutoLosslessProfile mode = %d, want ModeBalanced", gotMode)
	}
	if gotReason != vp8lAutoLosslessReasonBalanced {
		t.Fatalf("vp8lAutoLosslessProfile reason = %d, want balanced", gotReason)
	}
}

func TestVP8LAutoLosslessModeClassifiesBalancedImageTypes(t *testing.T) {
	cases := []struct {
		name       string
		img        image.Image
		wantReason vp8lAutoLosslessReason
	}{
		{
			name:       "palette",
			img:        newBenchmarkLimitedPalettedFixtureImage(1024, 1024),
			wantReason: vp8lAutoLosslessReasonPaletteLike,
		},
		{
			name: "flat",
			img: newBenchmarkFixtureImage(lossyBenchmarkCase{
				kind:   benchmarkImageFlat,
				width:  512,
				height: 512,
			}),
			wantReason: vp8lAutoLosslessReasonFlat,
		},
		{
			name: "ui",
			img: newBenchmarkFixtureImage(lossyBenchmarkCase{
				kind:   benchmarkImageUI,
				width:  1024,
				height: 1024,
			}),
			wantReason: vp8lAutoLosslessReasonUILike,
		},
		{
			name: "gradient",
			img: newBenchmarkFixtureImage(lossyBenchmarkCase{
				kind:   benchmarkImageGradient,
				width:  128,
				height: 128,
			}),
			wantReason: vp8lAutoLosslessReasonGradientLike,
		},
		{
			name: "photo-like",
			img: newBenchmarkFixtureImage(lossyBenchmarkCase{
				kind:   benchmarkImagePhotoLike,
				width:  256,
				height: 256,
			}),
			wantReason: vp8lAutoLosslessReasonPhotoLike,
		},
		{
			name: "alpha-heavy",
			img: newBenchmarkFixtureImage(lossyBenchmarkCase{
				kind:   benchmarkImageAlpha,
				width:  128,
				height: 128,
			}),
			wantReason: vp8lAutoLosslessReasonAlphaHeavy,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bounds := tc.img.Bounds()
			gotMode, gotReason := vp8lAutoLosslessProfile(tc.img, pixelReaderFor(tc.img), bounds, bounds.Dx(), bounds.Dy())
			if gotMode != ModeBalanced {
				t.Fatalf("mode = %d, want ModeBalanced", gotMode)
			}
			if gotReason != tc.wantReason {
				t.Fatalf("reason = %d, want %d", gotReason, tc.wantReason)
			}
		})
	}
}

func TestNearLosslessErrorMetricsPreserveAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			value := uint8(103)
			if x >= 32 {
				value = 203
			}
			img.SetNRGBA(x, y, color.NRGBA{R: value, G: value, B: value, A: uint8(x + y)})
		}
	}

	metrics := estimateNearLosslessError(img, 50)
	if metrics.alphaExact != 1 {
		t.Fatal("near-lossless alphaExact = 0, want 1")
	}
	if metrics.rgbMaxAbs <= 0 {
		t.Fatalf("near-lossless rgbMaxAbs = %d, want positive", metrics.rgbMaxAbs)
	}
	if metrics.rgbMAE <= 0 {
		t.Fatalf("near-lossless rgbMAE = %f, want positive", metrics.rgbMAE)
	}
	if metrics.rgbMaxAbs > 4 {
		t.Fatalf("near-lossless rgbMaxAbs = %d, want <= 4", metrics.rgbMaxAbs)
	}
}

func TestPixelReaderForFastPaths(t *testing.T) {
	nrgba := image.NewNRGBA(image.Rect(3, 5, 5, 6))
	nrgba.SetNRGBA(3, 5, color.NRGBA{R: 1, G: 2, B: 3, A: 4})
	nrgba.SetNRGBA(4, 5, color.NRGBA{R: 250, G: 251, B: 252, A: 253})
	readNRGBA := pixelReaderFor(nrgba)
	readNRGBALuma := lumaReaderFor(nrgba)
	readNRGBAChroma := chromaReaderFor(nrgba)
	if got, want := readNRGBA(3, 5), (color.NRGBA{R: 1, G: 2, B: 3, A: 4}); got != want {
		t.Fatalf("NRGBA pixel = %#v, want %#v", got, want)
	}
	if got, want := readNRGBA(4, 5), (color.NRGBA{R: 250, G: 251, B: 252, A: 253}); got != want {
		t.Fatalf("NRGBA pixel = %#v, want %#v", got, want)
	}
	if got, want := readNRGBALuma(4, 5), rgbToLuma(250, 251, 252); got != want {
		t.Fatalf("NRGBA luma = %d, want %d", got, want)
	}
	gotCb, gotCr := readNRGBAChroma(4, 5)
	wantCb, wantCr := rgbToChroma(250, 251, 252)
	if gotCb != wantCb || gotCr != wantCr {
		t.Fatalf("NRGBA chroma = (%d,%d), want (%d,%d)", gotCb, gotCr, wantCb, wantCr)
	}

	rgba := image.NewRGBA(image.Rect(7, 11, 10, 12))
	values := []color.RGBA{
		{R: 0, G: 0, B: 0, A: 0},
		{R: 32, G: 64, B: 96, A: 128},
		{R: 9, G: 10, B: 11, A: 255},
	}
	for i, c := range values {
		rgba.SetRGBA(rgba.Rect.Min.X+i, rgba.Rect.Min.Y, c)
	}
	readRGBA := pixelReaderFor(rgba)
	readRGBALuma := lumaReaderFor(rgba)
	readRGBAChroma := chromaReaderFor(rgba)
	for i := range values {
		x := rgba.Rect.Min.X + i
		want := color.NRGBAModel.Convert(rgba.RGBAAt(x, rgba.Rect.Min.Y)).(color.NRGBA)
		if got := readRGBA(x, rgba.Rect.Min.Y); got != want {
			t.Fatalf("RGBA pixel %d = %#v, want %#v", i, got, want)
		}
		wantLuma := rgbToLuma(want.R, want.G, want.B)
		if got := readRGBALuma(x, rgba.Rect.Min.Y); got != wantLuma {
			t.Fatalf("RGBA luma %d = %d, want %d", i, got, wantLuma)
		}
		gotCb, gotCr := readRGBAChroma(x, rgba.Rect.Min.Y)
		wantCb, wantCr := rgbToChroma(want.R, want.G, want.B)
		if gotCb != wantCb || gotCr != wantCr {
			t.Fatalf("RGBA chroma %d = (%d,%d), want (%d,%d)", i, gotCb, gotCr, wantCb, wantCr)
		}
	}

	gray := image.NewGray(image.Rect(11, 13, 15, 15))
	for y := gray.Rect.Min.Y; y < gray.Rect.Max.Y; y++ {
		for x := gray.Rect.Min.X; x < gray.Rect.Max.X; x++ {
			gray.SetGray(x, y, color.Gray{Y: uint8(17 + x*3 + y*5)})
		}
	}
	readGray := pixelReaderFor(gray)
	readGrayLuma := lumaReaderFor(gray)
	readGrayChroma := chromaReaderFor(gray)
	for _, p := range []image.Point{
		{X: 11, Y: 13},
		{X: 14, Y: 13},
		{X: 12, Y: 14},
	} {
		wantY := gray.GrayAt(p.X, p.Y).Y
		want := color.NRGBA{R: wantY, G: wantY, B: wantY, A: 255}
		if got := readGray(p.X, p.Y); got != want {
			t.Fatalf("Gray pixel at %v = %#v, want %#v", p, got, want)
		}
		wantLuma := rgbToLuma(wantY, wantY, wantY)
		if got := readGrayLuma(p.X, p.Y); got != wantLuma {
			t.Fatalf("Gray luma at %v = %d, want %d", p, got, wantLuma)
		}
		gotCb, gotCr := readGrayChroma(p.X, p.Y)
		if gotCb != 128 || gotCr != 128 {
			t.Fatalf("Gray chroma at %v = (%d,%d), want (128,128)", p, gotCb, gotCr)
		}
	}

	ycbcr := image.NewYCbCr(image.Rect(5, 7, 9, 11), image.YCbCrSubsampleRatio420)
	for y := ycbcr.Rect.Min.Y; y < ycbcr.Rect.Max.Y; y++ {
		for x := ycbcr.Rect.Min.X; x < ycbcr.Rect.Max.X; x++ {
			ycbcr.Y[ycbcr.YOffset(x, y)] = uint8(32 + x*11 + y*7)
			ci := ycbcr.COffset(x, y)
			ycbcr.Cb[ci] = uint8(96 + ci*13)
			ycbcr.Cr[ci] = uint8(160 + ci*17)
		}
	}
	readYCbCr := pixelReaderFor(ycbcr)
	readYCbCrLuma := lumaReaderFor(ycbcr)
	readYCbCrChroma := chromaReaderFor(ycbcr)
	for _, p := range []image.Point{
		{X: 5, Y: 7},
		{X: 6, Y: 7},
		{X: 8, Y: 10},
	} {
		want := color.NRGBAModel.Convert(ycbcr.YCbCrAt(p.X, p.Y)).(color.NRGBA)
		if got := readYCbCr(p.X, p.Y); got != want {
			t.Fatalf("YCbCr pixel at %v = %#v, want %#v", p, got, want)
		}
		yy := ycbcr.Y[ycbcr.YOffset(p.X, p.Y)]
		ci := ycbcr.COffset(p.X, p.Y)
		wantLuma := ycbcrToVP8LumaTable[yy]
		if got := readYCbCrLuma(p.X, p.Y); got != wantLuma {
			t.Fatalf("YCbCr luma at %v = %d, want %d", p, got, wantLuma)
		}
		gotCb, gotCr := readYCbCrChroma(p.X, p.Y)
		wantCb := ycbcrToVP8ChromaTable[ycbcr.Cb[ci]]
		wantCr := ycbcrToVP8ChromaTable[ycbcr.Cr[ci]]
		if gotCb != wantCb || gotCr != wantCr {
			t.Fatalf("YCbCr chroma at %v = (%d,%d), want (%d,%d)", p, gotCb, gotCr, wantCb, wantCr)
		}
	}

	paletted := image.NewPaletted(image.Rect(2, 4, 6, 6), color.Palette{
		color.NRGBA{R: 3, G: 5, B: 7, A: 255},
		color.RGBA{R: 64, G: 96, B: 128, A: 160},
		color.Gray{Y: 210},
		color.NRGBA{R: 250, G: 16, B: 48, A: 80},
	})
	for y := paletted.Rect.Min.Y; y < paletted.Rect.Max.Y; y++ {
		for x := paletted.Rect.Min.X; x < paletted.Rect.Max.X; x++ {
			paletted.SetColorIndex(x, y, uint8((x+y)%len(paletted.Palette)))
		}
	}
	readPaletted := pixelReaderFor(paletted)
	readPalettedLuma := lumaReaderFor(paletted)
	readPalettedChroma := chromaReaderFor(paletted)
	for _, p := range []image.Point{
		{X: 2, Y: 4},
		{X: 5, Y: 4},
		{X: 4, Y: 5},
	} {
		want := color.NRGBAModel.Convert(paletted.At(p.X, p.Y)).(color.NRGBA)
		if got := readPaletted(p.X, p.Y); got != want {
			t.Fatalf("Paletted pixel at %v = %#v, want %#v", p, got, want)
		}
		wantLuma := rgbToLuma(want.R, want.G, want.B)
		if got := readPalettedLuma(p.X, p.Y); got != wantLuma {
			t.Fatalf("Paletted luma at %v = %d, want %d", p, got, wantLuma)
		}
		gotCb, gotCr := readPalettedChroma(p.X, p.Y)
		wantCb, wantCr := rgbToChroma(want.R, want.G, want.B)
		if gotCb != wantCb || gotCr != wantCr {
			t.Fatalf("Paletted chroma at %v = (%d,%d), want (%d,%d)", p, gotCb, gotCr, wantCb, wantCr)
		}
	}
}

func TestPixelReaderFor64BitFastPaths(t *testing.T) {
	bounds := image.Rect(3, 5, 6, 6)
	nrgba64 := image.NewNRGBA64(bounds)
	nrgba64.SetNRGBA64(3, 5, color.NRGBA64{R: 0xffff, G: 0x8000, B: 0x4000, A: 0})
	nrgba64.SetNRGBA64(4, 5, color.NRGBA64{R: 0x7000, G: 0x5000, B: 0x3000, A: 0x8080})
	nrgba64.SetNRGBA64(5, 5, color.NRGBA64{R: 0x1234, G: 0xabcd, B: 0x789a, A: 0xffff})

	rgba64 := image.NewRGBA64(bounds)
	rgba64.SetRGBA64(3, 5, color.RGBA64{R: 0, G: 0, B: 0, A: 0})
	rgba64.SetRGBA64(4, 5, color.RGBA64{R: 0x4000, G: 0x3000, B: 0x2000, A: 0x8080})
	rgba64.SetRGBA64(5, 5, color.RGBA64{R: 0x1234, G: 0xabcd, B: 0x789a, A: 0xffff})

	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{name: "NRGBA64", img: nrgba64},
		{name: "RGBA64", img: rgba64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readPixel := pixelReaderFor(tc.img)
			readGeneric := pixelReaderFor(benchmarkImageWrapper{Image: tc.img})
			readLuma := lumaReaderFor(tc.img)
			readChroma := chromaReaderFor(tc.img)
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				want := color.NRGBAModel.Convert(tc.img.At(x, bounds.Min.Y)).(color.NRGBA)
				if got := readPixel(x, bounds.Min.Y); got != want {
					t.Fatalf("pixel at x=%d = %#v, want %#v", x, got, want)
				}
				if got := readPixel(x, bounds.Min.Y); got != readGeneric(x, bounds.Min.Y) {
					t.Fatalf("fast pixel at x=%d = %#v, generic %#v", x, got, readGeneric(x, bounds.Min.Y))
				}
				if got, want := readLuma(x, bounds.Min.Y), rgbToLuma(want.R, want.G, want.B); got != want {
					t.Fatalf("luma at x=%d = %d, want %d", x, got, want)
				}
				gotCb, gotCr := readChroma(x, bounds.Min.Y)
				wantCb, wantCr := rgbToChroma(want.R, want.G, want.B)
				if gotCb != wantCb || gotCr != wantCr {
					t.Fatalf("chroma at x=%d = (%d,%d), want (%d,%d)", x, gotCb, gotCr, wantCb, wantCr)
				}
			}

			var sink color.NRGBA
			if allocs := testing.AllocsPerRun(100, func() {
				sink = readPixel(bounds.Min.X+1, bounds.Min.Y)
			}); allocs != 0 {
				t.Fatalf("pixel read allocations = %f, want 0", allocs)
			}
			if sink.A == 0xff && sink.R == 0xff && sink.G == 0xff && sink.B == 0xff {
				t.Fatal("unexpected allocation test sink")
			}
		})
	}
}

func TestVP8StudioRangeColorConversion(t *testing.T) {
	y := rgbToLuma(16, 32, 48)
	cb, cr := rgbToChroma(16, 32, 48)
	if y != 41 || cb != 137 || cr != 120 {
		t.Fatalf("RGB to VP8 YUV = (%d,%d,%d), want (41,137,120)", y, cb, cr)
	}
	r, g, b := vp8YUVToRGB(y, cb, cr)
	if absInt(int(r)-16) > 1 || absInt(int(g)-32) > 1 || absInt(int(b)-48) > 1 {
		t.Fatalf("VP8 YUV round trip = (%d,%d,%d), want approximately (16,32,48)", r, g, b)
	}
	if got := rgbToLuma(0, 0, 0); got != 16 {
		t.Fatalf("black luma = %d, want 16", got)
	}
	if got := rgbToLuma(255, 255, 255); got != 235 {
		t.Fatalf("white luma = %d, want 235", got)
	}
}

func TestVP8MaterializedSourceMatchesSpecializedReaders(t *testing.T) {
	images := []image.Image{
		newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageGradient, width: 17, height: 19}),
		newBenchmarkYCbCrFixtureImage(17, 19),
		newBenchmarkPalettedFixtureImage(17, 19),
	}
	for _, img := range images {
		t.Run(fmt.Sprintf("%T", img), func(t *testing.T) {
			source := newEncoderSource(img)
			direct := newVP8Source(source, false)
			materialized := newVP8Source(source, true)
			if direct.materialized() {
				t.Fatal("direct source unexpectedly materialized")
			}
			if !materialized.materialized() {
				t.Fatal("materialized source kept specialized readers")
			}
			if got, want := len(materialized.plane.data), 3*source.width*source.height; got != want {
				t.Fatalf("plane bytes = %d, want %d", got, want)
			}
			for y := source.bounds.Min.Y; y < source.bounds.Max.Y; y++ {
				for x := source.bounds.Min.X; x < source.bounds.Max.X; x++ {
					if got, want := materialized.readLuma(x, y), direct.readLuma(x, y); got != want {
						t.Fatalf("luma (%d,%d) = %d, want %d", x, y, got, want)
					}
					gotCb, gotCr := materialized.readChroma(x, y)
					wantCb, wantCr := direct.readChroma(x, y)
					if gotCb != wantCb || gotCr != wantCr {
						t.Fatalf("chroma (%d,%d) = (%d,%d), want (%d,%d)", x, y, gotCb, gotCr, wantCb, wantCr)
					}
				}
			}
			cfg := vp8LossyConfigForModeQuality(ModeBestCompression, 75)
			directFrame, err := encodeVP8KeyFrameSource(direct, cfg)
			if err != nil {
				t.Fatalf("direct encode failed: %v", err)
			}
			materializedFrame, err := encodeVP8KeyFrameSource(materialized, cfg)
			if err != nil {
				t.Fatalf("materialized encode failed: %v", err)
			}
			if !bytes.Equal(materializedFrame, directFrame) {
				t.Fatalf("materialized frame differs from direct frame: got %d bytes, want %d bytes", len(materializedFrame), len(directFrame))
			}
		})
	}
}

func TestEncoderRoundTripGray(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 3, 1))
	img.SetGray(0, 0, color.Gray{Y: 7})
	img.SetGray(1, 0, color.Gray{Y: 7})
	img.SetGray(2, 0, color.Gray{Y: 9})

	var buf bytes.Buffer
	enc := Encoder{}
	if err := enc.Encode(&buf, img); err != nil {
		t.Fatalf("Encoder.Encode failed: %v", err)
	}

	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != 3 || height != 1 {
		t.Fatalf("dimensions = %dx%d, want 3x1", width, height)
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	want := []color.NRGBA{
		{R: 7, G: 7, B: 7, A: 255},
		{R: 7, G: 7, B: 7, A: 255},
		{R: 9, G: 9, B: 9, A: 255},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pixel %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestVP8LDistanceCodeForPositionDistance(t *testing.T) {
	tests := []struct {
		name     string
		distance int
		width    int
		wantCode int
	}{
		{name: "previous pixel", distance: 1, width: 16, wantCode: 2},
		{name: "previous row", distance: 16, width: 16, wantCode: 1},
		{name: "generic distance", distance: 1000, width: 16, wantCode: 1120},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCode, ok := vp8lDistanceCodeForPositionDistance(tt.distance, tt.width)
			if !ok {
				t.Fatal("vp8lDistanceCodeForPositionDistance returned false")
			}
			if gotCode != tt.wantCode {
				t.Fatalf("distance code = %d, want %d", gotCode, tt.wantCode)
			}
			gotDistance, err := testVP8LDistanceCodeToDistance(gotCode, tt.width)
			if err != nil {
				t.Fatalf("testVP8LDistanceCodeToDistance failed: %v", err)
			}
			if gotDistance != tt.distance {
				t.Fatalf("round-trip distance = %d, want %d", gotDistance, tt.distance)
			}
		})
	}
}

func TestVP8LSpecialDistanceCodeFastMatchesDistanceMap(t *testing.T) {
	for _, width := range []int{16, 17, 64, 4096} {
		maxDistance := 7*width + 8
		for distance := 1; distance <= maxDistance; distance++ {
			wantCode, wantOK := slowVP8LSpecialDistanceCode(distance, width)
			gotCode, gotOK := vp8lSpecialDistanceCode(distance, width)
			if gotOK != wantOK || gotCode != wantCode {
				t.Fatalf("width %d distance %d: code = %d ok = %t, want code %d ok %t", width, distance, gotCode, gotOK, wantCode, wantOK)
			}
		}
	}
}

func slowVP8LSpecialDistanceCode(distance int, width int) (int, bool) {
	for i, offset := range vp8lDistanceMap {
		mapped := offset.x + offset.y*width
		if mapped == distance && mapped >= 1 {
			return i + 1, true
		}
	}
	return 0, false
}

func TestVP8LLengthPrefixCodeBoundariesRoundTrip(t *testing.T) {
	assertVP8LPrefixCodeBoundariesRoundTrip(t, "length", nLengthCodes, vp8lPrefixCode)
}

func TestVP8LDistancePrefixCodeBoundariesRoundTrip(t *testing.T) {
	assertVP8LPrefixCodeBoundariesRoundTrip(t, "distance", nDistanceCodes, vp8lDistancePrefixCode)
}

func assertVP8LPrefixCodeBoundariesRoundTrip(t *testing.T, name string, codeCount int, encode func(int) vp8lPrefix) {
	t.Helper()
	for code := 0; code < codeCount; code++ {
		for _, value := range vp8lPrefixBoundaryValues(code) {
			t.Run(fmt.Sprintf("%s/code%d/value%d", name, code, value), func(t *testing.T) {
				prefix := encode(value)
				if prefix.code != code {
					t.Fatalf("prefix code = %d, want %d", prefix.code, code)
				}
				if prefix.extraBits != vp8lPrefixExtraBits(code) {
					t.Fatalf("extra bits = %d, want %d", prefix.extraBits, vp8lPrefixExtraBits(code))
				}

				var buf bytes.Buffer
				bw := bufio.NewWriter(&buf)
				bits := newBitWriter(bw)
				bits.writeBits(prefix.extra, prefix.extraBits)
				if err := bits.flush(); err != nil {
					t.Fatalf("bit flush failed: %v", err)
				}
				if err := bw.Flush(); err != nil {
					t.Fatalf("buffer flush failed: %v", err)
				}
				r := testBitReader{data: buf.Bytes()}
				got, err := decodeVP8LPrefixValue(&r, code)
				if err != nil {
					t.Fatalf("decodeVP8LPrefixValue failed: %v", err)
				}
				if got != value {
					t.Fatalf("decoded value = %d, want %d", got, value)
				}
			})
		}
	}
}

func vp8lPrefixBoundaryValues(code int) []int {
	if code < 4 {
		return []int{code + 1}
	}
	extraBits := vp8lPrefixExtraBits(code)
	offset := (2 + code&1) << extraBits
	minValue := offset + 1
	maxValue := offset + 1<<extraBits
	return []int{minValue, maxValue}
}

func TestEncodeLossyWritesVP8Chunk(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 17, 19))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 11),
				G: uint8(y * 9),
				B: uint8((x + y) * 5),
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{Compression: CompressionLossy}); err != nil {
		t.Fatalf("Encode lossy failed: %v", err)
	}

	data := buf.Bytes()
	if len(data) < 30 {
		t.Fatalf("lossy WebP length = %d, want at least 30", len(data))
	}
	chunks := readWebPChunks(t, data)
	if len(chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1", len(chunks))
	}
	if chunks[0].name != "VP8 " {
		t.Fatalf("chunk name = %q, want VP8 ", chunks[0].name)
	}
	assertLossyVP8Frame(t, chunks[0].payload, 17, 19)
}

func TestLossyStandardImageOpaque(t *testing.T) {
	nrgba := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	nrgba.SetNRGBA(0, 0, color.NRGBA{R: 1, A: 255})
	nrgba.SetNRGBA(1, 0, color.NRGBA{G: 2, A: 255})
	nrgbaAlpha := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	nrgbaAlpha.SetNRGBA(0, 0, color.NRGBA{A: 254})
	rgba := image.NewRGBA(image.Rect(0, 0, 1, 1))
	rgba.SetRGBA(0, 0, color.RGBA{R: 1, A: 255})
	paletted := image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.NRGBA{A: 255}})

	for _, tc := range []struct {
		name string
		img  image.Image
		want bool
	}{
		{name: "NRGBA", img: nrgba, want: true},
		{name: "NRGBAAlpha", img: nrgbaAlpha, want: false},
		{name: "RGBA", img: rgba, want: true},
		{name: "Gray", img: image.NewGray(image.Rect(0, 0, 1, 1)), want: true},
		{name: "YCbCr", img: image.NewYCbCr(image.Rect(0, 0, 1, 1), image.YCbCrSubsampleRatio420), want: true},
		{name: "Paletted", img: paletted, want: true},
		{name: "Uniform", img: image.NewUniform(color.NRGBA{A: 255}), want: true},
		{name: "Wrapped", img: benchmarkImageWrapper{Image: nrgba}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := lossyStandardImageOpaque(tc.img); got != tc.want {
				t.Fatalf("lossyStandardImageOpaque = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEncodeLossyQualityOption(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 17, 19))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*11 + y*3),
				G: uint8(y*9 + x*5),
				B: uint8((x + y) * 7),
				A: 255,
			})
		}
	}

	encode := func(quality int) []byte {
		t.Helper()
		var buf bytes.Buffer
		if err := Encode(&buf, img, &Options{
			Compression: CompressionLossy,
			Quality:     quality,
		}); err != nil {
			t.Fatalf("Encode lossy quality %d failed: %v", quality, err)
		}
		return buf.Bytes()
	}

	defaultQuality := encode(0)
	quality100 := encode(100)
	quality1 := encode(1)
	qualityOverMax := encode(200)
	qualityNegative := encode(-1)

	if !bytes.Equal(defaultQuality, quality100) {
		t.Fatal("default lossy quality differs from Quality 100")
	}
	if !bytes.Equal(qualityOverMax, quality100) {
		t.Fatal("Quality greater than 100 was not clamped to Quality 100")
	}
	if !bytes.Equal(qualityNegative, quality100) {
		t.Fatal("Quality less than or equal to zero did not use the default")
	}
	if bytes.Equal(quality1, quality100) {
		t.Fatal("Quality 1 output equals Quality 100 output")
	}

	chunks := readWebPChunks(t, quality1)
	if len(chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1", len(chunks))
	}
	if chunks[0].name != "VP8 " {
		t.Fatalf("chunk name = %q, want VP8 ", chunks[0].name)
	}
	assertLossyVP8Frame(t, chunks[0].payload, 17, 19)
}

func TestVP8QualityToQIndexMapping(t *testing.T) {
	cases := []struct {
		quality int
		want    int
	}{
		{quality: 100, want: 0},
		{quality: 90, want: 9},
		{quality: 75, want: 26},
		{quality: 50, want: 38},
		{quality: 25, want: 57},
		{quality: 1, want: 103},
	}
	for _, tc := range cases {
		if got := qualityToVP8QIndex(tc.quality); got != tc.want {
			t.Fatalf("qualityToVP8QIndex(%d) = %d, want %d", tc.quality, got, tc.want)
		}
	}
	if qualityToVP8QIndex(90) >= qualityToVP8QIndex(75) {
		t.Fatal("higher quality did not produce a lower qIndex")
	}
	if qualityToVP8QIndex(75) >= qualityToVP8QIndex(50) {
		t.Fatal("quality 75 did not produce a lower qIndex than quality 50")
	}
	previous := qualityToVP8QIndex(1)
	for quality := 2; quality <= 100; quality++ {
		current := qualityToVP8QIndex(quality)
		if current > previous {
			t.Fatalf("quality %d qIndex = %d, previous quality qIndex = %d", quality, current, previous)
		}
		previous = current
	}
}

func TestVP8QuantUsesQualityDependentDeltas(t *testing.T) {
	high := vp8QuantForIndex(qualityToVP8QIndex(90))
	medium := vp8QuantForIndex(qualityToVP8QIndex(75))
	low := vp8QuantForIndex(qualityToVP8QIndex(10))
	if high.uvAC > high.y1AC {
		t.Fatalf("high quality uvAC = %d, want <= y1AC %d", high.uvAC, high.y1AC)
	}
	if medium.y2AC <= medium.y1AC {
		t.Fatalf("medium quality y2AC = %d, want > y1AC %d", medium.y2AC, medium.y1AC)
	}
	if low.uvAC > low.y1AC {
		t.Fatalf("low quality uvAC = %d, want <= y1AC %d", low.uvAC, low.y1AC)
	}
}

func TestVP8LoopFilterTracksQualityMapping(t *testing.T) {
	high := vp8LoopFilterForIndex(qualityToVP8QIndex(90))
	medium := vp8LoopFilterForIndex(qualityToVP8QIndex(75))
	low := vp8LoopFilterForIndex(qualityToVP8QIndex(10))
	if high.level >= medium.level {
		t.Fatalf("high quality loop filter level = %d, want less than medium %d", high.level, medium.level)
	}
	if medium.level >= low.level {
		t.Fatalf("medium quality loop filter level = %d, want less than low %d", medium.level, low.level)
	}
	if high.sharpness > low.sharpness {
		t.Fatalf("high quality sharpness = %d, want <= low quality sharpness %d", high.sharpness, low.sharpness)
	}
}

func TestVP8LossyConfigForModeQuality(t *testing.T) {
	fast := vp8LossyConfigForModeQuality(ModeFast, 75)
	if fast.qIndex != qualityToVP8QIndex(75) {
		t.Fatalf("ModeFast qIndex = %d, want quality mapping", fast.qIndex)
	}
	if fast.filter != vp8LoopFilterForIndex(fast.qIndex) {
		t.Fatalf("ModeFast loop filter = %#v, want quality-derived filter %#v", fast.filter, vp8LoopFilterForIndex(fast.qIndex))
	}
	wantDeltas := vp8QuantDeltas{uvDC: -2}
	if fast.quantDeltas != wantDeltas {
		t.Fatalf("ModeFast quantizer deltas = %+v, want %+v", fast.quantDeltas, wantDeltas)
	}
	if want := vp8QuantForIndexDeltasBias(fast.qIndex, fast.quantDeltas, fast.quantBias); fast.quant != want {
		t.Fatalf("ModeFast quantizer = %+v, want header-derived %+v", fast.quant, want)
	}
	if fast.tryY4 {
		t.Fatal("ModeFast enabled Y4 mode search")
	}
	if fast.updateTokenProb {
		t.Fatal("ModeFast enabled token probability update search")
	}
	if fast.bufferResiduals {
		t.Fatal("ModeFast enabled residual buffering without a reusable analysis pass")
	}
	if fast.materializeSource {
		t.Fatal("ModeFast enabled the YUV source plane")
	}
	if fast.parallelAlpha {
		t.Fatal("ModeFast enabled parallel alpha analysis")
	}
	if fast.maxSegments != 1 {
		t.Fatalf("ModeFast max segments = %d, want 1", fast.maxSegments)
	}
	if fast.quantBias != vp8ConservativeQuantBias() || fast.rdYLambdaScale != 256 || fast.rdUVLambdaScale != 256 {
		t.Fatalf("ModeFast quant/RD profile = %#v/%d/%d", fast.quantBias, fast.rdYLambdaScale, fast.rdUVLambdaScale)
	}

	lossyQuality := vp8LossyConfigForModeQuality(ModeLossyQuality, 75)
	if !lossyQuality.tryY4 {
		t.Fatal("ModeLossyQuality disabled Y4 mode search")
	}
	if !lossyQuality.updateTokenProb {
		t.Fatal("ModeLossyQuality disabled token probability updates")
	}
	if !lossyQuality.bufferResiduals {
		t.Fatal("ModeLossyQuality disabled residual buffering")
	}
	if lossyQuality.maxSegments != 4 || lossyQuality.segmentStrength != 0 {
		t.Fatalf("ModeLossyQuality segmentation = %d/%d, want 4/adaptive", lossyQuality.maxSegments, lossyQuality.segmentStrength)
	}
	if lossyQuality.rdPasses != 1 {
		t.Fatalf("ModeLossyQuality RD passes = %d, want 1", lossyQuality.rdPasses)
	}
	if !lossyQuality.parallelAlpha {
		t.Fatal("ModeLossyQuality disabled parallel alpha analysis")
	}
	if lossyQuality.trellis {
		t.Fatal("ModeLossyQuality enabled trellis quantization")
	}
	best := vp8LossyConfigForModeQuality(ModeBestCompression, 75)
	if !best.tryY4 {
		t.Fatal("ModeBestCompression disabled Y4 mode search")
	}
	if !best.materializeSource {
		t.Fatal("ModeBestCompression disabled the reusable YUV source plane")
	}
	if best.maxSegments != 4 || best.segmentStrength != 0 {
		t.Fatalf("ModeBestCompression segmentation = %d/%d, want 4/adaptive", best.maxSegments, best.segmentStrength)
	}
	if best.rdPasses != 2 {
		t.Fatalf("ModeBestCompression RD passes = %d, want 2", best.rdPasses)
	}
	if best.trellis {
		t.Fatal("ModeBestCompression enabled trellis quantization after Y4 beam superseded it")
	}
	if best.y4RefinementBeamWidth != 2 {
		t.Fatalf("ModeBestCompression Y4 refinement beam width = %d, want 2", best.y4RefinementBeamWidth)
	}
	if best.dcDiffusion {
		t.Fatal("ModeBestCompression enabled chroma DC error diffusion without an RD benefit check")
	}
	if !best.sharpYUV {
		t.Fatal("ModeBestCompression disabled sharp YUV search")
	}
	if !best.parallelAlpha {
		t.Fatal("ModeBestCompression disabled parallel alpha analysis")
	}
	if best.quantBias != lossyQuality.quantBias || best.rdYLambdaScale != lossyQuality.rdYLambdaScale || best.rdUVLambdaScale != lossyQuality.rdUVLambdaScale || best.textureStrength != lossyQuality.textureStrength {
		t.Fatalf("ModeBestCompression quality profile = %#v/%d/%d/%d, want Default profile %#v/%d/%d/%d", best.quantBias, best.rdYLambdaScale, best.rdUVLambdaScale, best.textureStrength, lossyQuality.quantBias, lossyQuality.rdYLambdaScale, lossyQuality.rdUVLambdaScale, lossyQuality.textureStrength)
	}
	if lowMemory := vp8LossyConfigForModeQuality(ModeLowMemory, 75); lowMemory.tryY4 || lowMemory.bufferResiduals || lowMemory.materializeSource || lowMemory.maxSegments != 1 {
		t.Fatal("ModeLowMemory enabled buffered source or residual state")
	}
	if low := vp8LossyConfigForModeQuality(ModeLossyQuality, 10); low.rd.yLambda <= lossyQuality.rd.yLambda {
		t.Fatalf("low quality luma lambda = %d, want greater than q75 lambda %d", low.rd.yLambda, lossyQuality.rd.yLambda)
	}
}

func TestLossyAlphaConfigForMode(t *testing.T) {
	balanced := lossyAlphaConfigForMode(ModeBalanced)
	if balanced.filters != [4]bool{true, true, true, true} || !balanced.tryRLE || !balanced.trySpatialRef {
		t.Fatalf("ModeBalanced alpha config = %#v", balanced)
	}
	if balanced.optimalPasses != 0 || balanced.optimalFilters != 0 {
		t.Fatal("ModeBalanced enabled optimal alpha parsing")
	}

	fast := lossyAlphaConfigForMode(ModeFast)
	if fast.filters != [4]bool{true, false, false, false} || !fast.tryRLE || fast.trySpatialRef || fast.optimalPasses != 0 {
		t.Fatalf("ModeFast alpha config = %#v", fast)
	}

	best := lossyAlphaConfigForMode(ModeBestCompression)
	if !best.trySpatialRef || best.optimalPasses != 1 || best.optimalFilters != 1 || best.optimalPixels != 4<<20 {
		t.Fatalf("ModeBestCompression alpha config = %#v", best)
	}

	lowMemory := lossyAlphaConfigForMode(ModeLowMemory)
	if !lowMemory.tryRLE || lowMemory.trySpatialRef || lowMemory.optimalPasses != 0 {
		t.Fatalf("ModeLowMemory alpha config = %#v", lowMemory)
	}
}

func TestVP8SegmentationClassifiesOneToFourActivityGroups(t *testing.T) {
	activities := make([]uint32, 0, 192)
	for _, activity := range []uint32{0, 100, 1000, 10000} {
		for range 48 {
			activities = append(activities, activity)
		}
	}
	cfg := vp8LossyConfigForModeQuality(ModeBestCompression, 75)
	cfg.maxSegments = 4
	segmentation := makeVP8SegmentationForActivities(activities, cfg)
	if !segmentation.enabled() || segmentation.count != 4 {
		t.Fatalf("segmentation enabled=%t count=%d, want enabled with 4 segments", segmentation.enabled(), segmentation.count)
	}
	if !segmentation.useDCDiffusion() {
		t.Fatal("mixed flat and active segments did not enable DC diffusion")
	}
	for group := 0; group < 4; group++ {
		for i := group * 48; i < (group+1)*48; i++ {
			if got := segmentation.mapIDs[i]; got != uint8(group) {
				t.Fatalf("segment map[%d] = %d, want %d", i, got, group)
			}
		}
	}
	for i := 1; i < segmentation.count; i++ {
		if segmentation.segments[i-1].quant.qIndex >= segmentation.segments[i].quant.qIndex {
			t.Fatalf("segment quantizers are not increasing: %d then %d", segmentation.segments[i-1].quant.qIndex, segmentation.segments[i].quant.qIndex)
		}
	}

	cfg.maxSegments = 2
	segmentation = makeVP8SegmentationForActivities(activities, cfg)
	if !segmentation.enabled() || segmentation.count != 2 {
		t.Fatalf("two-segment profile enabled=%t count=%d, want enabled with 2 segments", segmentation.enabled(), segmentation.count)
	}
	cfg.maxSegments = 1
	if got := makeVP8SegmentationForActivities(activities, cfg); got.enabled() {
		t.Fatal("one-segment profile enabled segmentation")
	}
	for i := range activities {
		activities[i] += 100
	}
	cfg.maxSegments = 4
	if got := makeVP8SegmentationForActivities(activities, cfg); !got.enabled() || got.useDCDiffusion() {
		t.Fatalf("non-flat activity distribution enabled=%t diffusion=%t, want enabled without diffusion", got.enabled(), got.useDCDiffusion())
	}
}

func TestVP8SegmentationDisablesNarrowActivityDistribution(t *testing.T) {
	activities := make([]uint32, 256)
	for i := range activities {
		activities[i] = 15000 + uint32(i%2000)
	}
	cfg := vp8LossyConfigForModeQuality(ModeDefault, 75)
	if got := makeVP8SegmentationForActivities(activities, cfg); got.enabled() {
		t.Fatalf("narrow activity distribution enabled %d segments", got.count)
	}
}

func TestVP8SegmentMapProbabilities(t *testing.T) {
	got := vp8SegmentMapProbabilities([]uint8{0, 0, 1, 1, 2, 3})
	want := [3]uint8{170, 128, 128}
	if got != want {
		t.Fatalf("segment map probabilities = %v, want %v", got, want)
	}
	if got := vp8SegmentMapProbabilities([]uint8{0, 0, 0}); got != [3]uint8{255, 255, 255} {
		t.Fatalf("single-segment map probabilities = %v, want [255 255 255]", got)
	}
	if got := vp8SegmentMapProbabilities([]uint8{2, 2, 3, 3}); got[0] != 0 {
		t.Fatalf("all-right root probability = %d, want 0", got[0])
	}
}

func TestVP8FastLossyConfigUsesY16OnlyModes(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{
		kind:   benchmarkImageGradient,
		width:  32,
		height: 32,
	})
	bounds := img.Bounds()
	mbw := (bounds.Dx() + 15) >> 4
	mbh := (bounds.Dy() + 15) >> 4
	cfg := vp8LossyConfigForModeQuality(ModeFast, 75)
	modes := analyzeVP8ModesConfig(lumaReaderFor(img), chromaReaderFor(img), bounds, mbw, mbh, cfg, newVP8EncodeBuffers(mbw, mbh))
	for i, mode := range modes {
		if !mode.useY16 {
			t.Fatalf("mode %d used Y4 search in ModeFast", i)
		}
	}
}

func TestVP8ResidualPartitionCapacityTracksQualityAndBounds(t *testing.T) {
	if got := vp8ResidualPartitionCapacity(8, 8, qualityToVP8QIndex(75)); got != 1024 {
		t.Fatalf("small image capacity = %d, want 1024", got)
	}
	if got := vp8ResidualPartitionCapacity(1024, 1024, qualityToVP8QIndex(100)); got != 1<<20 {
		t.Fatalf("high quality capacity = %d, want %d", got, 1<<20)
	}
	medium := vp8ResidualPartitionCapacity(1024, 1024, qualityToVP8QIndex(75))
	low := vp8ResidualPartitionCapacity(1024, 1024, qualityToVP8QIndex(25))
	if medium != (1024*1024)/2 {
		t.Fatalf("medium quality capacity = %d, want %d", medium, (1024*1024)/2)
	}
	if low >= medium {
		t.Fatalf("low quality capacity = %d, want less than medium quality %d", low, medium)
	}
	if got := vp8ResidualPartitionCapacity(maxVP8Dimension, maxVP8Dimension, qualityToVP8QIndex(100)); got != 1<<20 {
		t.Fatalf("large image capacity = %d, want capped at %d", got, 1<<20)
	}
}

func TestChromaSampleFilteredUsesNeighboringPixels(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	red := color.NRGBA{R: 255, G: 0, B: 0, A: 255}
	blue := color.NRGBA{R: 0, G: 0, B: 255, A: 255}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, blue)
		}
	}
	img.SetNRGBA(1, 1, red)
	img.SetNRGBA(2, 1, red)

	readChroma := chromaReaderFor(img)
	redCb, redCr := rgbToChroma(red.R, red.G, red.B)
	blueCb, blueCr := rgbToChroma(blue.R, blue.G, blue.B)
	simpleCb := uint8((int(redCb)*2 + int(blueCb)*2 + 2) / 4)
	simpleCr := uint8((int(redCr)*2 + int(blueCr)*2 + 2) / 4)
	gotCb := chromaSample(readChroma, img.Bounds(), 1, 1, true)
	gotCr := chromaSample(readChroma, img.Bounds(), 1, 1, false)
	if gotCb <= simpleCb || gotCb >= blueCb {
		t.Fatalf("filtered Cb = %d, want between simple %d and blue %d", gotCb, simpleCb, blueCb)
	}
	if gotCr >= simpleCr || gotCr <= blueCr {
		t.Fatalf("filtered Cr = %d, want between blue %d and simple %d", gotCr, blueCr, simpleCr)
	}
}

func TestChromaSampleFilteredClampsImageEdges(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 20, G: 180, B: 80, A: 255})
	readChroma := chromaReaderFor(img)
	wantCb, wantCr := rgbToChroma(20, 180, 80)
	if got := chromaSample(readChroma, img.Bounds(), 0, 0, true); got != wantCb {
		t.Fatalf("edge Cb = %d, want %d", got, wantCb)
	}
	if got := chromaSample(readChroma, img.Bounds(), 0, 0, false); got != wantCr {
		t.Fatalf("edge Cr = %d, want %d", got, wantCr)
	}
}

func TestChromaSampleFilteredInBoundsMatchesClampedPath(t *testing.T) {
	img := image.NewNRGBA(image.Rect(3, 5, 19, 23))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*17 + y*3),
				G: uint8(y*19 + x*5),
				B: uint8((x-y)*11 + x*y),
				A: 255,
			})
		}
	}
	readChroma := chromaReaderFor(img)
	bounds := img.Bounds()
	for _, tc := range []struct {
		x  int
		y  int
		cb bool
	}{
		{x: 1, y: 1, cb: true},
		{x: 1, y: 1, cb: false},
		{x: 6, y: 7, cb: true},
		{x: 6, y: 7, cb: false},
		{x: bounds.Dx() - 3, y: bounds.Dy() - 3, cb: true},
		{x: bounds.Dx() - 3, y: bounds.Dy() - 3, cb: false},
	} {
		got := chromaSampleFiltered(readChroma, bounds, tc.x, tc.y, tc.cb)
		want := chromaSampleFilteredClamped(readChroma, bounds, tc.x, tc.y, tc.cb)
		if got != want {
			t.Fatalf("sample at (%d,%d) cb=%v = %d, want %d", tc.x, tc.y, tc.cb, got, want)
		}
	}
}

func TestChromaTargetMBMatchesChromaSamples(t *testing.T) {
	img := image.NewNRGBA(image.Rect(3, 5, 70, 74))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*13 + y*7),
				G: uint8(y*17 + x*3),
				B: uint8((x+y)*11 + x*y),
				A: 255,
			})
		}
	}

	readChroma := chromaReaderFor(img)
	bounds := img.Bounds()
	for _, tc := range []struct {
		mbx int
		mby int
	}{
		{mbx: 0, mby: 0},
		{mbx: 1, mby: 1},
		{mbx: 3, mby: 3},
	} {
		target := makeChromaTargetMB(readChroma, bounds, tc.mbx, tc.mby)
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				sampleX := tc.mbx*16 + x*2
				sampleY := tc.mby*16 + y*2
				i := y*8 + x
				if got, want := target.cb[i], chromaSample(readChroma, bounds, sampleX, sampleY, true); got != want {
					t.Fatalf("target Cb mb=(%d,%d) xy=(%d,%d) = %d, want %d", tc.mbx, tc.mby, x, y, got, want)
				}
				if got, want := target.cr[i], chromaSample(readChroma, bounds, sampleX, sampleY, false); got != want {
					t.Fatalf("target Cr mb=(%d,%d) xy=(%d,%d) = %d, want %d", tc.mbx, tc.mby, x, y, got, want)
				}
			}
		}
	}
}

func TestChromaPairCacheMatchesInBoundsSampler(t *testing.T) {
	img := image.NewNRGBA(image.Rect(3, 5, 70, 74))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*29 + y*7),
				G: uint8(y*31 + x*5),
				B: uint8((x-y)*13 + x*y),
				A: 255,
			})
		}
	}

	readChroma := chromaReaderFor(img)
	bounds := img.Bounds()
	baseX, baseY := 16, 16
	cache := makeChromaPairCacheMB(readChroma, bounds.Min.X+baseX, bounds.Min.Y+baseY)
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			sampleX := x * 2
			sampleY := y * 2
			gotCb, gotCr := chromaSamplePairFromCache(&cache, sampleX, sampleY)
			wantCb, wantCr := chromaSamplePairInBounds(readChroma, bounds.Min.X+baseX+sampleX, bounds.Min.Y+baseY+sampleY)
			if gotCb != wantCb || gotCr != wantCr {
				t.Fatalf("cached chroma xy=(%d,%d) = (%d,%d), want (%d,%d)", x, y, gotCb, gotCr, wantCb, wantCr)
			}
		}
	}
}

func TestChromaTargetReuseMatchesFreshTarget(t *testing.T) {
	img := image.NewNRGBA(image.Rect(3, 5, 70, 74))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*11 + y*7),
				G: uint8(y*13 + x*5),
				B: uint8((x-y)*19 + x*y),
				A: 255,
			})
		}
	}

	readChroma := chromaReaderFor(img)
	bounds := img.Bounds()
	mbw := (bounds.Dx() + 15) >> 4
	mbh := (bounds.Dy() + 15) >> 4
	stride := mbw * 8
	mbx, mby := 1, 1
	quant := vp8QuantForIndex(qualityToVP8QIndex(75))
	rd := newVP8RDConfig(quant)
	recCb := make([]uint8, stride*mbh*8)
	recCr := make([]uint8, stride*mbh*8)
	for i := range recCb {
		recCb[i] = uint8(i*17 + i/5)
		recCr[i] = uint8(i*23 + i/7)
	}

	left := [4]uint8{1, 0, 1, 0}
	up := [4]uint8{0, 1, 0, 1}
	freshLeft, freshUp := left, up
	reuseLeft, reuseUp := left, up
	target := makeChromaTargetMB(readChroma, bounds, mbx, mby)
	freshMode := chooseVP8ChromaMode(readChroma, bounds, mbx, mby, recCb, recCr, stride, quant, rd, &freshLeft, &freshUp)
	reuseMode := chooseVP8ChromaModeFromTarget(&target, mbx, mby, recCb, recCr, stride, quant, rd, &reuseLeft, &reuseUp)
	if reuseMode != freshMode {
		t.Fatalf("reused chroma mode = %d, want %d", reuseMode, freshMode)
	}
	if reuseLeft != freshLeft || reuseUp != freshUp {
		t.Fatal("chroma mode selection mutated reuse context differently")
	}

	mode := vp8MBMode{cMode: freshMode}
	freshCb := append([]uint8(nil), recCb...)
	freshCr := append([]uint8(nil), recCr...)
	reuseCb := append([]uint8(nil), recCb...)
	reuseCr := append([]uint8(nil), recCr...)
	freshLeft, freshUp = left, up
	reuseLeft, reuseUp = left, up
	processVP8ChromaMB(readChroma, bounds, mbx, mby, freshCb, freshCr, stride, quant, mode, &freshLeft, &freshUp, nil)
	processVP8ChromaTargetMB(&target, mbx, mby, reuseCb, reuseCr, stride, quant, mode, &reuseLeft, &reuseUp, nil)
	if !bytes.Equal(reuseCb, freshCb) {
		t.Fatal("reused chroma target produced different Cb reconstruction")
	}
	if !bytes.Equal(reuseCr, freshCr) {
		t.Fatal("reused chroma target produced different Cr reconstruction")
	}
	if reuseLeft != freshLeft || reuseUp != freshUp {
		t.Fatal("reused chroma target produced different context state")
	}
}

func TestLumaTargetMBMatchesSampledLuma(t *testing.T) {
	img := image.NewNRGBA(image.Rect(3, 5, 70, 74))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*5 + y*19),
				G: uint8(y*7 + x*11),
				B: uint8((x-y)*13 + x*y),
				A: 255,
			})
		}
	}

	readPixel := pixelReaderFor(img)
	readLuma := lumaReaderFor(img)
	bounds := img.Bounds()
	for _, tc := range []struct {
		mbx int
		mby int
	}{
		{mbx: 0, mby: 0},
		{mbx: 1, mby: 1},
		{mbx: 3, mby: 3},
	} {
		target := makeLumaTargetMB(readLuma, bounds, tc.mbx, tc.mby)
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				sampleX := tc.mbx*16 + x
				sampleY := tc.mby*16 + y
				c := samplePixel(readPixel, bounds, sampleX, sampleY)
				want := rgbToLuma(c.R, c.G, c.B)
				got := target.blocks[(y/4)*4+x/4][(y%4)*4+x%4]
				if got != want {
					t.Fatalf("target Y mb=(%d,%d) xy=(%d,%d) = %d, want %d", tc.mbx, tc.mby, x, y, got, want)
				}
			}
		}
	}
}

func TestLumaResidualBlockMatchesSampledLuma(t *testing.T) {
	img := image.NewNRGBA(image.Rect(3, 5, 70, 74))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*9 + y*5),
				G: uint8(y*13 + x*3),
				B: uint8((x-y)*17 + x*y),
				A: 255,
			})
		}
	}

	var pred [16]uint8
	for i := range pred {
		pred[i] = uint8(20 + i*7)
	}

	readPixel := pixelReaderFor(img)
	readLuma := lumaReaderFor(img)
	bounds := img.Bounds()
	for _, pos := range []struct {
		x int
		y int
	}{
		{x: 4, y: 8},
		{x: 64, y: 66},
	} {
		got := lumaResidualBlock(readLuma, bounds, pos.x, pos.y, pred)
		var want [16]int
		for yy := 0; yy < 4; yy++ {
			for xx := 0; xx < 4; xx++ {
				c := samplePixel(readPixel, bounds, pos.x+xx, pos.y+yy)
				luma := rgbToLuma(c.R, c.G, c.B)
				want[yy*4+xx] = int(luma) - int(pred[yy*4+xx])
			}
		}
		if got != want {
			t.Fatalf("residual at (%d,%d) = %v, want %v", pos.x, pos.y, got, want)
		}
	}
}

func TestEncodeLossyEnablesNormalLoopFilterWithDelta(t *testing.T) {
	const quality = 25

	img := image.NewNRGBA(image.Rect(0, 0, 17, 19))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*13 + y*3),
				G: uint8(y*11 + x*5),
				B: uint8((x + y) * 7),
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{
		Compression: CompressionLossy,
		Quality:     quality,
	}); err != nil {
		t.Fatalf("Encode lossy failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1", len(chunks))
	}
	if chunks[0].name != "VP8 " {
		t.Fatalf("chunk name = %q, want VP8 ", chunks[0].name)
	}

	got := readVP8LoopFilterHeader(t, chunks[0].payload)
	want := vp8LoopFilterForIndex(qualityToVP8QIndex(quality))
	if got != want {
		t.Fatalf("loop filter = %#v, want %#v", got, want)
	}
	if got.level == 0 {
		t.Fatal("loop filter level = 0, want enabled")
	}
	if got.simple {
		t.Fatal("loop filter is simple, want normal")
	}
	if !got.deltaEnabled {
		t.Fatal("loop filter delta is disabled")
	}
	if got.modeDeltas[0] <= 0 {
		t.Fatalf("B_PRED mode delta = %d, want positive", got.modeDeltas[0])
	}
}

func TestVP8BlockQuantizationKeepsAC(t *testing.T) {
	residual := [16]int{
		64, -64, 64, -64,
		-64, 64, -64, 64,
		64, -64, 64, -64,
		-64, 64, -64, 64,
	}
	quant := vp8QuantForIndex(qualityToVP8QIndex(100))
	coeff := quantizeVP8Block(residual, quant.y1DC, quant.y1AC)

	hasAC := false
	for _, c := range coeff[1:] {
		if c != 0 {
			hasAC = true
			break
		}
	}
	if !hasAC {
		t.Fatal("quantized checkerboard residual has no AC coefficients")
	}

	recon := reconstructVP8Block(filledBlock4(128), coeff, quant.y1DC, quant.y1AC)
	minV, maxV := recon[0], recon[0]
	for _, v := range recon[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if minV == maxV {
		t.Fatal("AC reconstruction produced a constant block")
	}
}

func TestVP8BlockQuantizationACOnlyMatchesZeroDC(t *testing.T) {
	transformed := [16]int{
		1200, -96, 80, -64,
		48, -32, 16, -8,
		7, -6, 5, -4,
		3, -2, 1, -1,
	}
	quant := vp8QuantForIndex(qualityToVP8QIndex(75))
	got := quantizeTransformedVP8BlockACOnly(transformed, quant.y1AC)
	want := quantizeTransformedVP8Block(transformed, 0, quant.y1AC)
	if got != want {
		t.Fatalf("AC-only quantized coeff = %#v, want %#v", got, want)
	}
	if got[0] != 0 {
		t.Fatalf("AC-only DC coeff = %d, want 0", got[0])
	}
}

func TestVP8TrellisQuantizationReducesRDCost(t *testing.T) {
	const (
		dcQ    = 24
		acQ    = 24
		lambda = int64(1 << 16)
	)
	transformed := [16]int{13, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	scalar := quantizeTransformedVP8Block(transformed, dcQ, acQ)
	if scalar[0] == 0 {
		t.Fatal("scalar quantization did not produce the expected non-zero coefficient")
	}
	probs := vp8DefaultTokenProbs
	probs[vp8PlaneY1SansY2][0][0][0] = 255
	trellis := quantizeTransformedVP8BlockRD(transformed, dcQ, acQ, vp8PlaneY1SansY2, 0, 0, lambda, &probs)
	if trellis[0] != 0 {
		t.Fatalf("trellis coefficient = %d, want zero", trellis[0])
	}
	gotScore := vp8TrellisBlockScore(transformed, trellis, dcQ, acQ, vp8PlaneY1SansY2, 0, 0, lambda, &probs)
	wantMax := vp8TrellisBlockScore(transformed, scalar, dcQ, acQ, vp8PlaneY1SansY2, 0, 0, lambda, &probs)
	if gotScore >= wantMax {
		t.Fatalf("trellis score = %d, want less than scalar score %d", gotScore, wantMax)
	}
	if got := quantizeTransformedVP8BlockRD(transformed, dcQ, acQ, vp8PlaneY1SansY2, 0, 0, lambda, nil); got != scalar {
		t.Fatalf("disabled trellis = %v, want scalar %v", got, scalar)
	}
}

func TestVP8ChromaDCDiffusionRoutesQuantizationError(t *testing.T) {
	diffusion := newVP8DCDiffusion(2)
	first := diffusion.beginMacroblock(0, true)
	wantCorrected := [4]int{13, 7, 7, 18}
	for block := range 4 {
		if got := first.correct(block, 13, 24); got != wantCorrected[block] {
			t.Fatalf("corrected DC block %d = %d, want %d", block, got, wantCorrected[block])
		}
	}
	first.finish()
	if got := diffusion.left[0]; got != [2]int8{3, -3} {
		t.Fatalf("Cb left errors = %v, want [3 -3]", got)
	}
	if got := diffusion.top[0][0]; got != [2]int8{3, 0} {
		t.Fatalf("Cb top errors = %v, want [3 0]", got)
	}
	second := diffusion.beginMacroblock(1, true)
	if got := second.correct(0, 13, 24); got != 16 {
		t.Fatalf("next macroblock corrected DC = %d, want 16", got)
	}
	if got := diffusion.left[1]; got != [2]int8{} {
		t.Fatalf("Cr errors changed while diffusing Cb: %v", got)
	}
	newRow := diffusion.beginMacroblock(0, true)
	if newRow.left != [2]int8{} {
		t.Fatalf("new row left errors = %v, want zero", newRow.left)
	}
}

func TestVP8SharpChromaDoesNotIncreaseLocalRGBError(t *testing.T) {
	img := image.NewNRGBA(image.Rect(3, 5, 8, 10))
	colors := [...]color.NRGBA{
		{R: 250, G: 20, B: 20, A: 255},
		{R: 20, G: 30, B: 250, A: 255},
		{R: 20, G: 240, B: 40, A: 255},
		{R: 240, G: 220, B: 20, A: 255},
	}
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, colors[(x+y*3)&3])
		}
	}
	source := newEncoderSource(img)
	vp8Source := newVP8Source(source, true)
	readPixel := source.pixels()
	halfWidth := (source.width + 1) >> 1
	halfHeight := (source.height + 1) >> 1
	baselineScores := make([]uint64, halfWidth*halfHeight)
	for by := 0; by < halfHeight; by++ {
		for bx := 0; bx < halfWidth; bx++ {
			x, y := bx*2, by*2
			cb, cr := chromaSamplePair(vp8Source.readChroma, source.bounds, x, y)
			baselineScores[by*halfWidth+bx] = vp8Source.chromaRGBScore2x2(readPixel, x, y, cb, cr)
		}
	}
	vp8Source.applySharpChroma(readPixel)
	for by := 0; by < halfHeight; by++ {
		for bx := 0; bx < halfWidth; bx++ {
			x, y := bx*2, by*2
			cb, cr := chromaSamplePair(vp8Source.readChroma, source.bounds, x, y)
			gotScore := vp8Source.chromaRGBScore2x2(readPixel, x, y, cb, cr)
			wantMax := baselineScores[by*halfWidth+bx]
			if gotScore > wantMax {
				t.Fatalf("sharp chroma block (%d,%d) RGB score = %d, want <= %d", bx, by, gotScore, wantMax)
			}
			for yy := 0; yy < 2 && y+yy < source.height; yy++ {
				for xx := 0; xx < 2 && x+xx < source.width; xx++ {
					gotCb, gotCr := vp8Source.readChroma(source.bounds.Min.X+x+xx, source.bounds.Min.Y+y+yy)
					if gotCb != cb || gotCr != cr {
						t.Fatalf("sharp chroma block (%d,%d) is not constant", bx, by)
					}
				}
			}
		}
	}
}

func TestVP8Y4MacroblockPreservesY2NonZeroContext(t *testing.T) {
	bounds := image.Rect(0, 0, 16, 16)
	readLuma := func(x int, y int) uint8 {
		return uint8(x*11 + y*7)
	}
	mode := vp8MBMode{y4Modes: [16]uint8{}}
	leftY16 := uint8(1)
	upY16 := uint8(1)
	processVP8LumaMB(readLuma, bounds, 0, 0, make([]uint8, 16*16), 16, vp8QuantForIndex(48), mode, &[4]uint8{}, &[4]uint8{}, &leftY16, &upY16, nil)
	if leftY16 != 1 || upY16 != 1 {
		t.Fatalf("Y2 contexts after Y4 macroblock = (%d, %d), want (1, 1)", leftY16, upY16)
	}
}

func TestEncodeLossyBestCompressionWithDWebP(t *testing.T) {
	if _, err := exec.LookPath("dwebp"); err != nil {
		t.Skip("dwebp is not available")
	}
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{
		kind:   benchmarkImageUI,
		width:  256,
		height: 256,
	})
	cfg := vp8LossyConfigForModeQuality(ModeBestCompression, 75)
	source := newVP8Source(newEncoderSource(img), cfg.materializeSource)
	mbw := (source.width + 15) >> 4
	mbh := (source.height + 15) >> 4
	plan := makeVP8FramePlan(source, cfg, newVP8EncodeBuffers(mbw, mbh))
	if !plan.segmentation.enabled() {
		t.Fatal("segmentation is disabled for the regression fixture")
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{Compression: CompressionLossy, Mode: ModeBestCompression, Quality: 75}); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	dir := t.TempDir()
	webpPath := dir + "/best-segments.webp"
	pngPath := dir + "/best-segments.png"
	if err := os.WriteFile(webpPath, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write WebP: %v", err)
	}
	cmd := exec.Command("dwebp", "-quiet", webpPath, "-o", pngPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dwebp failed: %v: %s", err, output)
	}
}

func TestVP8Y4InternalReconstructionMatchesDWebP(t *testing.T) {
	if _, err := exec.LookPath("dwebp"); err != nil {
		t.Skip("dwebp is not available")
	}
	const width = 32
	const height = 32
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{
		kind:   benchmarkImageGradient,
		width:  width,
		height: height,
	})
	source := newVP8Source(newEncoderSource(img), false)
	cfg := vp8LossyConfigForModeQuality(ModeDefault, 75)
	const mbw = width / 16
	const mbh = height / 16
	modes := make([]vp8MBMode, mbw*mbh)
	for macroblock := range modes {
		modes[macroblock].cMode = vp8PredDC
		for block := range modes[macroblock].y4Modes {
			modes[macroblock].y4Modes[block] = uint8((macroblock*16 + block) % int(vp8NumPredModes))
		}
	}
	tokenProbs := vp8DefaultTokenProbs
	work := newVP8EncodeBuffers(mbw, mbh)
	firstPart, err := vp8FirstPartition(mbw, mbh, cfg.qIndex, cfg.quantDeltas, vp8LoopFilter{}, nil, modes, tokenProbs, nil, 0)
	if err != nil {
		t.Fatalf("vp8FirstPartition failed: %v", err)
	}
	residualPart := encodeVP8ResidualsConfig(source.readLuma, source.readChroma, source.bounds, width, height, mbw, mbh, cfg.quant, nil, modes, work, &tokenProbs, nil)
	frame := assembleVP8KeyFrame(width, height, firstPart, residualPart)
	var encoded bytes.Buffer
	if err := writeLossySimple(&encoded, frame); err != nil {
		t.Fatalf("writeLossySimple failed: %v", err)
	}

	dir := t.TempDir()
	webpPath := dir + "/y4.webp"
	yuvPath := dir + "/y4.yuv"
	if err := os.WriteFile(webpPath, encoded.Bytes(), 0o600); err != nil {
		t.Fatalf("write WebP: %v", err)
	}
	cmd := exec.Command("dwebp", "-quiet", "-nofilter", "-yuv", webpPath, "-o", yuvPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dwebp failed: %v: %s", err, output)
	}
	decoded, err := os.ReadFile(yuvPath)
	if err != nil {
		t.Fatalf("read decoded YUV: %v", err)
	}
	if len(decoded) < width*height {
		t.Fatalf("decoded YUV length = %d, want at least %d", len(decoded), width*height)
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if got, want := decoded[y*width+x], work.recY[y*width+x]; got != want {
				macroblock := (y/16)*mbw + x/16
				block := ((y&15)/4)*4 + (x&15)/4
				t.Fatalf("decoded luma (%d,%d) = %d, internal %d, macroblock %d block %d mode %d", x, y, got, want, macroblock, block, modes[macroblock].y4Modes[block])
			}
		}
	}
}

func TestVP8BlockQuantizationClampsToInt16Range(t *testing.T) {
	transformed := [16]int{1 << 30, -(1 << 30)}
	got := quantizeTransformedVP8Block(transformed, 1, 1)
	if got[0] != 2047 {
		t.Fatalf("positive coefficient = %d, want 2047", got[0])
	}
	if got[1] != -2047 {
		t.Fatalf("negative coefficient = %d, want -2047", got[1])
	}
}

func TestVP8MacroblockPredictionsUseBlockMajorLayout(t *testing.T) {
	const stride = 40
	rec := make([]uint8, stride*40)
	for y := 0; y < 40; y++ {
		for x := 0; x < stride; x++ {
			rec[y*stride+x] = uint8(x*13 + y*7)
		}
	}
	modes := []struct {
		name string
		mode uint8
	}{
		{name: "dc", mode: vp8PredDC},
		{name: "vertical", mode: vp8PredVE},
		{name: "horizontal", mode: vp8PredHE},
		{name: "true-motion", mode: vp8PredTM},
	}
	for _, tc := range modes {
		t.Run("luma-"+tc.name, func(t *testing.T) {
			const mbx = 1
			const mby = 1
			const x0 = mbx * 16
			const y0 = mby * 16
			pred := predictLuma16(rec, stride, mbx, mby, tc.mode)
			for y := 0; y < 16; y++ {
				for x := 0; x < 16; x++ {
					want := dcPred16(rec, stride, mbx, mby)
					switch tc.mode {
					case vp8PredVE:
						want = rec[(y0-1)*stride+x0+x]
					case vp8PredHE:
						want = rec[(y0+y)*stride+x0-1]
					case vp8PredTM:
						want = clipUint8(int(rec[(y0+y)*stride+x0-1]) + int(rec[(y0-1)*stride+x0+x]) - int(rec[(y0-1)*stride+x0-1]))
					}
					got := pred[(y/4)*4+x/4][(y%4)*4+x%4]
					if got != want {
						t.Fatalf("prediction at (%d, %d) = %d, want %d", x, y, got, want)
					}
				}
			}
		})

		t.Run("chroma-"+tc.name, func(t *testing.T) {
			const mbx = 1
			const mby = 1
			const x0 = mbx * 8
			const y0 = mby * 8
			pred := predictChroma8(rec, stride, mbx, mby, tc.mode)
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					want := dcPred8(rec, stride, mbx, mby)
					switch tc.mode {
					case vp8PredVE:
						want = rec[(y0-1)*stride+x0+x]
					case vp8PredHE:
						want = rec[(y0+y)*stride+x0-1]
					case vp8PredTM:
						want = clipUint8(int(rec[(y0+y)*stride+x0-1]) + int(rec[(y0-1)*stride+x0+x]) - int(rec[(y0-1)*stride+x0-1]))
					}
					got := pred[(y/4)*2+x/4][(y%4)*4+x%4]
					if got != want {
						t.Fatalf("prediction at (%d, %d) = %d, want %d", x, y, got, want)
					}
				}
			}
		})
	}
}

func TestVP8Y16ModeSelectionChoosesVertical(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 32))
	recY := make([]uint8, 16*32)
	for x := 0; x < 16; x++ {
		sourceV := uint8(32 + x*8)
		recY[15*16+x] = rgbToLuma(sourceV, sourceV, sourceV)
		for y := 16; y < 32; y++ {
			img.SetNRGBA(x, y, color.NRGBA{R: sourceV, G: sourceV, B: sourceV, A: 255})
		}
	}

	quant := vp8QuantForIndex(qualityToVP8QIndex(75))
	rd := newVP8RDConfig(quant)
	var left, up [4]uint8
	var leftY16, upY16 uint8
	target := makeLumaTargetMB(lumaReaderFor(img), img.Bounds(), 0, 1)
	blocks := makeLumaTargetBlocks(&target)
	mode, score := chooseVP8Y16Mode(&blocks, 0, 1, recY, 16, quant, rd, &left, &up, &leftY16, &upY16)
	if mode != vp8PredVE {
		t.Fatalf("Y16 mode = %d, want vertical", mode)
	}
	var zero vp8QuantizedBlock
	wantBits := vp8BitCost(145, true) + vp8Y16ModeCost(vp8PredVE) + vp8BlockBitCost(vp8PlaneY2, 0, zero)
	for i := 0; i < 16; i++ {
		wantBits += vp8BlockBitCostFrom(vp8PlaneY1WithY2, 0, zero, 1)
	}
	if want := rd.lumaScore(0, wantBits); score != want {
		t.Fatalf("Y16 vertical score = %d, want %d", score, want)
	}
}

func TestVP8Y4ModeSelectionChoosesVertical(t *testing.T) {
	const stride = 16
	const x = 4
	const y = 4

	recY := make([]uint8, stride*16)
	top := []uint8{0, 0, 0, 255, 255, 255}
	for i, v := range top {
		recY[(y-1)*stride+x-1+i] = v
	}
	for yy := 0; yy < 4; yy++ {
		recY[(y+yy)*stride+x-1] = 220
	}
	pred := predictLuma4(recY, stride, x, y, vp8PredVE)

	quant := vp8QuantForIndex(qualityToVP8QIndex(1))
	rd := newVP8RDConfig(quant)
	target := pred
	mode, score, nz, _ := chooseVP8Y4Mode(&target, x, y, recY, stride, quant, rd, vp8PredVE, vp8PredVE, 0)
	if mode != vp8PredVE {
		t.Fatalf("Y4 mode = %d, want vertical", mode)
	}
	if nz != 0 {
		t.Fatalf("Y4 vertical nz = %d, want 0", nz)
	}
	var zero vp8QuantizedBlock
	wantBits := vp8Y4ModeCost(vp8PredVE, vp8PredVE, vp8PredVE) + vp8BlockBitCost(vp8PlaneY1SansY2, 0, zero)
	if want := rd.lumaScore(0, wantBits); score != want {
		t.Fatalf("Y4 vertical score = %d, want %d", score, want)
	}
}

func TestVP8Y4ModeCostTableMatchesProbabilityTree(t *testing.T) {
	for topPred := uint8(0); topPred < vp8NumPredModes; topPred++ {
		for leftPred := uint8(0); leftPred < vp8NumPredModes; leftPred++ {
			prob := vp8PredProb[topPred][leftPred]
			for mode := uint8(0); mode < vp8NumPredModes; mode++ {
				got := vp8Y4ModeCost(topPred, leftPred, mode)
				want := vp8Y4ModeCostFromProb(prob, mode)
				if got != want {
					t.Fatalf("cost top=%d left=%d mode=%d = %d, want %d", topPred, leftPred, mode, got, want)
				}
			}
		}
	}
}

func TestVP8MacroblockModeCostTablesMatchBranchCosts(t *testing.T) {
	for mode := uint8(0); mode < vp8NumPredModes; mode++ {
		if got, want := vp8Y16ModeCost(mode), vp8Y16ModeCostFromMode(mode); got != want {
			t.Fatalf("Y16 cost mode=%d = %d, want %d", mode, got, want)
		}
		if got, want := vp8ChromaModeCost(mode), vp8ChromaModeCostFromMode(mode); got != want {
			t.Fatalf("chroma cost mode=%d = %d, want %d", mode, got, want)
		}
	}
}

func TestPredictLuma4WithNeighborsMatchesDirectPrediction(t *testing.T) {
	const stride = 16
	recY := make([]uint8, stride*16)
	for i := range recY {
		recY[i] = uint8(i*37 + i/3)
	}
	for _, pos := range []struct {
		x int
		y int
	}{
		{x: 0, y: 0},
		{x: 4, y: 4},
		{x: 12, y: 8},
	} {
		neighbors := makeLuma4Neighbors(recY, stride, pos.x, pos.y)
		for mode := uint8(0); mode < vp8NumPredModes; mode++ {
			want := predictLuma4(recY, stride, pos.x, pos.y, mode)
			got := predictLuma4WithNeighbors(&neighbors, mode)
			if got != want {
				t.Fatalf("prediction at (%d,%d) mode %d = %v, want %v", pos.x, pos.y, mode, got, want)
			}
		}
	}
}

func TestLuma4TopRightReplicatesAtUnavailableMacroblockEdge(t *testing.T) {
	const stride = 32
	recY := make([]uint8, stride*24)
	for x := 12; x < 20; x++ {
		recY[3*stride+x] = uint8(10 * (x - 11))
		recY[15*stride+x] = uint8(100 + 10*(x-11))
	}

	insideRow := makeLuma4Neighbors(recY, stride, 12, 4)
	for i := 4; i < 8; i++ {
		if got, want := insideRow.top[i], 0x7f; got != want {
			t.Fatalf("top-row macroblock top-right %d = %d, want border sample %d", i, got, want)
		}
	}
	macroblockTop := makeLuma4Neighbors(recY, stride, 12, 16)
	for i := 4; i < 8; i++ {
		if got, want := macroblockTop.top[i], int(recY[15*stride+12+i]); got != want {
			t.Fatalf("macroblock top-row top-right %d = %d, want available sample %d", i, got, want)
		}
	}
	insideLaterRow := makeLuma4Neighbors(recY, stride, 12, 20)
	for i := 4; i < 8; i++ {
		if got, want := insideLaterRow.top[i], int(recY[15*stride+12+i]); got != want {
			t.Fatalf("later macroblock-row top-right %d = %d, want cached sample %d", i, got, want)
		}
	}
}

func TestVP8FirstPartitionWritesSelectedY4Modes(t *testing.T) {
	want := [16]uint8{
		vp8PredDC, vp8PredTM, vp8PredVE, vp8PredHE,
		vp8PredRD, vp8PredVR, vp8PredLD, vp8PredVL,
		vp8PredHD, vp8PredHU, vp8PredDC, vp8PredTM,
		vp8PredVE, vp8PredHE, vp8PredRD, vp8PredVR,
	}
	firstPart, err := vp8FirstPartition(1, 1, qualityToVP8QIndex(75), vp8QuantDeltas{}, vp8LoopFilterForIndex(qualityToVP8QIndex(75)), nil, []vp8MBMode{{
		y4Modes: want,
		cMode:   vp8PredDC,
	}}, vp8DefaultTokenProbs, nil, 0)
	if err != nil {
		t.Fatalf("vp8FirstPartition failed: %v", err)
	}

	got := readVP8FirstPartitionY4Modes(t, firstPart)
	if got != want {
		t.Fatalf("Y4 modes = %v, want %v", got, want)
	}
}

func TestVP8FirstPartitionWritesSegmentation(t *testing.T) {
	segmentation := vp8Segmentation{
		count:    2,
		mapIDs:   []uint8{1},
		mapProbs: [3]uint8{137, 149, 255},
	}
	segmentation.segments[0] = vp8SegmentConfig{
		quant:       vp8QuantForIndex(12),
		filterLevel: 3,
	}
	segmentation.segments[1] = vp8SegmentConfig{
		quant:       vp8QuantForIndex(45),
		filterLevel: 7,
	}
	firstPart, err := vp8FirstPartition(1, 1, 30, vp8QuantDeltas{}, vp8LoopFilterForIndex(30), &segmentation, []vp8MBMode{{
		useY16: true,
		yMode:  vp8PredDC,
		cMode:  vp8PredDC,
	}}, vp8DefaultTokenProbs, nil, 0)
	if err != nil {
		t.Fatalf("vp8FirstPartition failed: %v", err)
	}

	var r testVP8PartitionReader
	r.init(firstPart)
	r.readUint(128, 1) // color space
	r.readUint(128, 1) // pixel clamp
	header := readVP8SegmentationHeader(t, &r)
	if !header.enabled || !header.updateMap || !header.updateData || !header.absolute {
		t.Fatalf("segmentation header = %+v, want enabled absolute updates", header)
	}
	if header.quantizers != [4]int{12, 45, 0, 0} {
		t.Fatalf("segment quantizers = %v, want [12 45 0 0]", header.quantizers)
	}
	if header.filterLevels != [4]int{3, 7, 0, 0} {
		t.Fatalf("segment filter levels = %v, want [3 7 0 0]", header.filterLevels)
	}
	if header.mapProbs != segmentation.mapProbs {
		t.Fatalf("segment map probabilities = %v, want %v", header.mapProbs, segmentation.mapProbs)
	}

	r.readBit(128)     // loop filter type
	r.readUint(128, 6) // loop filter level
	r.readUint(128, 3) // sharpness
	readVP8LoopFilterDeltas(t, &r)
	r.readUint(128, 2) // token partitions
	r.readUint(128, 7) // base quantizer
	readVP8QuantDeltas(&r)
	r.readBit(128) // refresh last frame buffer
	readVP8FirstPartitionTokenProbs(t, &r)
	if r.readBit(128) {
		t.Fatal("macroblock skip probability is enabled, want disabled")
	}
	if got := readVP8SegmentID(&r, header.mapProbs); got != 1 {
		t.Fatalf("macroblock segment ID = %d, want 1", got)
	}
	if r.unexpectedEOF {
		t.Fatal("unexpected end while reading segmented first partition")
	}
}

func TestVP8FirstPartitionWritesQuantizerDeltas(t *testing.T) {
	want := vp8QuantDeltas{
		y1DC: -3,
		y2DC: 4,
		y2AC: -5,
		uvDC: -2,
		uvAC: 6,
	}
	firstPart, err := vp8FirstPartition(1, 1, 30, want, vp8LoopFilterForIndex(30), nil, []vp8MBMode{{
		useY16: true,
		yMode:  vp8PredDC,
		cMode:  vp8PredDC,
	}}, vp8DefaultTokenProbs, nil, 0)
	if err != nil {
		t.Fatalf("vp8FirstPartition failed: %v", err)
	}

	var r testVP8PartitionReader
	r.init(firstPart)
	r.readUint(128, 1) // color space
	r.readUint(128, 1) // pixel clamp
	readVP8SegmentationHeader(t, &r)
	r.readBit(128)     // loop filter type
	r.readUint(128, 6) // loop filter level
	r.readUint(128, 3) // sharpness
	readVP8LoopFilterDeltas(t, &r)
	r.readUint(128, 2) // token partitions
	r.readUint(128, 7) // base quantizer
	if got := readVP8QuantDeltas(&r); got != want {
		t.Fatalf("quantizer deltas = %+v, want %+v", got, want)
	}
	if r.unexpectedEOF {
		t.Fatal("unexpected end while reading quantizer deltas")
	}
}

func TestVP8BlockBitCostAccountsForNonZeroCoefficients(t *testing.T) {
	var zero vp8QuantizedBlock
	var dc vp8QuantizedBlock
	dc[0] = 1
	zeroCost := vp8BlockBitCost(vp8PlaneY1SansY2, 0, zero)
	if got := vp8BlockBitCost(vp8PlaneY1SansY2, 0, dc); got <= zeroCost {
		t.Fatalf("non-zero DC bit cost = %d, want greater than zero block cost %d", got, zeroCost)
	}

	var ac vp8QuantizedBlock
	ac[1] = 1
	zeroSkipCost := vp8BlockBitCostFrom(vp8PlaneY1WithY2, 0, zero, 1)
	if got := vp8BlockBitCostFrom(vp8PlaneY1WithY2, 0, ac, 1); got <= zeroSkipCost {
		t.Fatalf("non-zero AC bit cost = %d, want greater than zero skip-first cost %d", got, zeroSkipCost)
	}
}

func TestVP8BlockBitCostDefaultMatchesExplicitDefaultProbs(t *testing.T) {
	coeff := vp8QuantizedBlock{
		0:  2,
		3:  -1,
		9:  5,
		14: -3,
		15: 1024,
	}
	probs := vp8DefaultTokenProbs
	for _, tc := range []struct {
		plane   int
		context uint8
		start   int
	}{
		{plane: vp8PlaneY1SansY2, context: 0, start: 0},
		{plane: vp8PlaneY1WithY2, context: 1, start: 1},
		{plane: vp8PlaneY2, context: 2, start: 0},
		{plane: vp8PlaneUV, context: 3, start: 0},
	} {
		got := vp8BlockBitCostFrom(tc.plane, tc.context, coeff, tc.start)
		want := vp8BlockBitCostFromWithProbs(&probs, tc.plane, tc.context, coeff, tc.start)
		if got != want {
			t.Fatalf("default cost plane=%d context=%d start=%d = %d, want %d", tc.plane, tc.context, tc.start, got, want)
		}
		gotWithNZ, gotNZ := vp8BlockBitCostFromAndNonZero(tc.plane, tc.context, coeff, tc.start)
		if gotWithNZ != got {
			t.Fatalf("default cost with nz plane=%d context=%d start=%d = %d, want %d", tc.plane, tc.context, tc.start, gotWithNZ, got)
		}
		ptrCost, ptrNZ := vp8BlockBitCostFromAndNonZeroPtr(tc.plane, tc.context, &coeff, tc.start)
		if ptrCost != gotWithNZ || ptrNZ != gotNZ {
			t.Fatalf("pointer default cost plane=%d context=%d start=%d = (%d,%v), want (%d,%v)", tc.plane, tc.context, tc.start, ptrCost, ptrNZ, gotWithNZ, gotNZ)
		}
		wantNZ := vp8HasNonZeroCoeff(coeff, tc.start)
		if gotNZ != wantNZ {
			t.Fatalf("default nz plane=%d context=%d start=%d = %v, want %v", tc.plane, tc.context, tc.start, gotNZ, wantNZ)
		}
	}

	var zero vp8QuantizedBlock
	zeroStartCost := vp8BlockBitCost(vp8PlaneY1SansY2, 2, zero)
	zeroStartCostWithNZ, zeroStartNZ := vp8BlockBitCostAndNonZero(vp8PlaneY1SansY2, 2, zero)
	if zeroStartCostWithNZ != zeroStartCost {
		t.Fatalf("zero start cost with nz = %d, want %d", zeroStartCostWithNZ, zeroStartCost)
	}
	if zeroStartNZ {
		t.Fatal("zero block from start reported non-zero coefficients")
	}

	zeroCost := vp8BlockBitCostFrom(vp8PlaneY1WithY2, 2, zero, 1)
	zeroCostWithNZ, zeroNZ := vp8BlockBitCostFromAndNonZero(vp8PlaneY1WithY2, 2, zero, 1)
	if zeroCostWithNZ != zeroCost {
		t.Fatalf("zero cost with nz = %d, want %d", zeroCostWithNZ, zeroCost)
	}
	if zeroNZ {
		t.Fatal("zero block reported non-zero coefficients")
	}
}

func TestEncodeVP8ZeroBlockWritesOnlyEOB(t *testing.T) {
	var zero vp8QuantizedBlock
	customProbs := vp8DefaultTokenProbs
	customProbs[vp8PlaneY1WithY2][1][2][0] = 17
	for _, tc := range []struct {
		name     string
		plane    int
		context  uint8
		start    int
		probs    *vp8TokenProbs
		wantProb uint8
		wantCtx  uint8
		wantBand int
	}{
		{
			name:     "start",
			plane:    vp8PlaneY1SansY2,
			context:  0,
			start:    0,
			wantProb: vp8DefaultTokenProbs[vp8PlaneY1SansY2][0][0][0],
			wantCtx:  0,
			wantBand: 0,
		},
		{
			name:     "skip-first",
			plane:    vp8PlaneY1WithY2,
			context:  1,
			start:    1,
			wantProb: vp8DefaultTokenProbs[vp8PlaneY1WithY2][1][1][0],
			wantCtx:  1,
			wantBand: 1,
		},
		{
			name:     "clamped-context-and-custom-probs",
			plane:    vp8PlaneY1WithY2,
			context:  7,
			start:    1,
			probs:    &customProbs,
			wantProb: customProbs[vp8PlaneY1WithY2][1][2][0],
			wantCtx:  2,
			wantBand: 1,
		},
	} {
		gotEnc := newVP8BoolEncoder()
		gotNZ := encodeVP8BlockFromWithProbs(gotEnc, tc.probs, tc.plane, tc.context, zero, tc.start)
		if gotNZ != 0 {
			t.Fatalf("%s non-zero flag = %d, want 0", tc.name, gotNZ)
		}

		wantEnc := newVP8BoolEncoder()
		wantEnc.writeBit(tc.wantProb, false)
		if got, want := gotEnc.bytes(), wantEnc.bytes(); !bytes.Equal(got, want) {
			t.Fatalf("%s bytes = %v, want %v", tc.name, got, want)
		}
		if got := vp8TokenProbFrom(tc.probs, tc.plane, tc.wantBand, tc.wantCtx)[0]; got != tc.wantProb {
			t.Fatalf("%s token prob = %d, want %d", tc.name, got, tc.wantProb)
		}
	}
}

func TestVP8RecordZeroBlockTokensOnlyRecordsEOB(t *testing.T) {
	var zero vp8QuantizedBlock
	for _, tc := range []struct {
		name    string
		plane   int
		context uint8
		start   int
	}{
		{name: "start", plane: vp8PlaneY1SansY2, context: 0, start: 0},
		{name: "skip-first", plane: vp8PlaneY1WithY2, context: 1, start: 1},
		{name: "clamped-context", plane: vp8PlaneUV, context: 9, start: 3},
	} {
		var got vp8TokenStats
		gotNZ := vp8RecordBlockTokensFrom(&got, tc.plane, tc.context, zero, tc.start)
		if gotNZ != 0 {
			t.Fatalf("%s non-zero flag = %d, want 0", tc.name, gotNZ)
		}

		wantContext := tc.context
		if wantContext > 2 {
			wantContext = 2
		}
		var want vp8TokenStats
		want.record(tc.plane, int(vp8Bands[tc.start]), wantContext, 0, false)
		if got != want {
			t.Fatalf("%s stats = %#v, want %#v", tc.name, got, want)
		}
	}
}

func TestVP8BlockFromIgnoresCoefficientsBeforeStart(t *testing.T) {
	var zero vp8QuantizedBlock
	coeff := zero
	coeff[0] = 7

	const (
		plane   = vp8PlaneY1WithY2
		context = uint8(2)
		start   = 1
	)
	if got, want := vp8BlockBitCostFrom(plane, context, coeff, start), vp8BlockBitCostFrom(plane, context, zero, start); got != want {
		t.Fatalf("cost = %d, want %d", got, want)
	}

	gotEnc := newVP8BoolEncoder()
	gotNZ := encodeVP8BlockFrom(gotEnc, plane, context, coeff, start)
	wantEnc := newVP8BoolEncoder()
	wantNZ := encodeVP8BlockFrom(wantEnc, plane, context, zero, start)
	if gotNZ != wantNZ {
		t.Fatalf("non-zero flag = %d, want %d", gotNZ, wantNZ)
	}
	if got, want := gotEnc.bytes(), wantEnc.bytes(); !bytes.Equal(got, want) {
		t.Fatalf("bytes = %v, want %v", got, want)
	}

	var gotStats vp8TokenStats
	gotStatsNZ := vp8RecordBlockTokensFrom(&gotStats, plane, context, coeff, start)
	var wantStats vp8TokenStats
	wantStatsNZ := vp8RecordBlockTokensFrom(&wantStats, plane, context, zero, start)
	if gotStatsNZ != wantStatsNZ {
		t.Fatalf("stats non-zero flag = %d, want %d", gotStatsNZ, wantStatsNZ)
	}
	if gotStats != wantStats {
		t.Fatalf("stats = %#v, want %#v", gotStats, wantStats)
	}
}

func TestVP8LastNonZeroCoeffUsesZigzagOrder(t *testing.T) {
	var coeff vp8QuantizedBlock
	if got := vp8LastNonZeroCoeff(coeff, 0); got != -1 {
		t.Fatalf("zero block last non-zero = %d, want -1", got)
	}

	coeff[vp8Zigzag[5]] = -1
	coeff[vp8Zigzag[12]] = 2
	if got := vp8LastNonZeroCoeff(coeff, 0); got != 12 {
		t.Fatalf("last non-zero = %d, want 12", got)
	}
	if got := vp8LastNonZeroCoeff(coeff, 6); got != 12 {
		t.Fatalf("last non-zero from 6 = %d, want 12", got)
	}
	if got := vp8LastNonZeroCoeff(coeff, 13); got != -1 {
		t.Fatalf("last non-zero from 13 = %d, want -1", got)
	}
}

func TestVP8PassesIgnoreInitialReconstructionBuffer(t *testing.T) {
	img := image.NewNRGBA(image.Rect(3, 5, 42, 38))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*9 + y*5),
				G: uint8(y*11 + x*7),
				B: uint8((x-y)*13 + x*y),
				A: 255,
			})
		}
	}

	readLuma := lumaReaderFor(img)
	readChroma := chromaReaderFor(img)
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	mbw := (width + 15) >> 4
	mbh := (height + 15) >> 4
	quant := vp8QuantForIndex(qualityToVP8QIndex(75))

	cleanWork := newVP8EncodeBuffers(mbw, mbh)
	cleanModes := analyzeVP8Modes(readLuma, readChroma, bounds, mbw, mbh, quant, cleanWork)
	dirtyWork := newVP8EncodeBuffers(mbw, mbh)
	fillVP8EncodeBuffers(dirtyWork, 0xa5)
	clear(dirtyWork.recY)
	dirtyModes := analyzeVP8Modes(readLuma, readChroma, bounds, mbw, mbh, quant, dirtyWork)
	if len(dirtyModes) != len(cleanModes) {
		t.Fatalf("dirty mode count = %d, want %d", len(dirtyModes), len(cleanModes))
	}
	for i := range cleanModes {
		if dirtyModes[i] != cleanModes[i] {
			t.Fatalf("mode[%d] with dirty work = %#v, want %#v", i, dirtyModes[i], cleanModes[i])
		}
	}

	cleanStatsWork := newVP8EncodeBuffers(mbw, mbh)
	cleanStats := collectVP8TokenStats(readLuma, readChroma, bounds, mbw, mbh, quant, cleanModes, cleanStatsWork)
	dirtyStatsWork := newVP8EncodeBuffers(mbw, mbh)
	fillVP8EncodeBuffers(dirtyStatsWork, 0x5a)
	clear(dirtyStatsWork.recY)
	dirtyStats := collectVP8TokenStats(readLuma, readChroma, bounds, mbw, mbh, quant, cleanModes, dirtyStatsWork)
	if dirtyStats != cleanStats {
		t.Fatal("token stats depend on the initial reconstruction buffer")
	}

	tokenProbs := chooseVP8TokenProbs(&cleanStats)
	cleanResidualWork := newVP8EncodeBuffers(mbw, mbh)
	cleanResidual := encodeVP8Residuals(readLuma, readChroma, bounds, width, height, mbw, mbh, quant, cleanModes, cleanResidualWork, &tokenProbs)
	dirtyResidualWork := newVP8EncodeBuffers(mbw, mbh)
	fillVP8EncodeBuffers(dirtyResidualWork, 0x3c)
	clear(dirtyResidualWork.recY)
	dirtyResidual := encodeVP8Residuals(readLuma, readChroma, bounds, width, height, mbw, mbh, quant, cleanModes, dirtyResidualWork, &tokenProbs)
	if !bytes.Equal(dirtyResidual, cleanResidual) {
		t.Fatal("residual stream depends on the initial reconstruction buffer")
	}
}

func TestVP8ResidualBufferMatchesLegacyPipeline(t *testing.T) {
	patterned := image.NewNRGBA(image.Rect(3, 5, 42, 38))
	for y := patterned.Rect.Min.Y; y < patterned.Rect.Max.Y; y++ {
		for x := patterned.Rect.Min.X; x < patterned.Rect.Max.X; x++ {
			patterned.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*9 + y*5),
				G: uint8(y*11 + x*7),
				B: uint8((x-y)*13 + x*y),
				A: 255,
			})
		}
	}
	solid := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := solid.Rect.Min.Y; y < solid.Rect.Max.Y; y++ {
		for x := solid.Rect.Min.X; x < solid.Rect.Max.X; x++ {
			solid.SetNRGBA(x, y, color.NRGBA{R: 80, G: 120, B: 160, A: 255})
		}
	}

	for _, tc := range []struct {
		name    string
		img     image.Image
		mode    Mode
		quality int
	}{
		{name: "default", img: patterned, mode: ModeDefault, quality: 75},
		{name: "best-y4", img: patterned, mode: ModeBestCompression, quality: 75},
		{name: "macroblock-skip", img: solid, mode: ModeDefault, quality: 75},
		{name: "high-quality", img: patterned, mode: ModeDefault, quality: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bounds := tc.img.Bounds()
			cfg := vp8LossyConfigForModeQuality(tc.mode, tc.quality)
			cfg.trellis = false
			buffered, err := encodeVP8KeyFrameConfig(lumaReaderFor(tc.img), chromaReaderFor(tc.img), bounds, bounds.Dx(), bounds.Dy(), cfg)
			if err != nil {
				t.Fatalf("buffered encode failed: %v", err)
			}

			cfg.bufferResiduals = false
			legacy, err := encodeVP8KeyFrameConfig(lumaReaderFor(tc.img), chromaReaderFor(tc.img), bounds, bounds.Dx(), bounds.Dy(), cfg)
			if err != nil {
				t.Fatalf("legacy encode failed: %v", err)
			}
			if !bytes.Equal(buffered, legacy) {
				t.Fatalf("buffered frame differs from legacy frame: got %d bytes, want %d bytes", len(buffered), len(legacy))
			}
		})
	}
}

func TestVP8FramePlanMatchesDirectEncoding(t *testing.T) {
	img := image.NewNRGBA(image.Rect(3, 5, 42, 38))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*9 + y*5),
				G: uint8(y*11 + x*7),
				B: uint8((x-y)*13 + x*y),
				A: 255,
			})
		}
	}
	readLuma := lumaReaderFor(img)
	readChroma := chromaReaderFor(img)
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	mbw := (width + 15) >> 4
	mbh := (height + 15) >> 4
	for _, mode := range []Mode{ModeDefault, ModeLowMemory} {
		t.Run(fmt.Sprintf("mode-%d", mode), func(t *testing.T) {
			cfg := vp8LossyConfigForModeQuality(mode, 75)
			work := newVP8EncodeBuffers(mbw, mbh)
			source := vp8Source{bounds: bounds, width: width, height: height, readLuma: readLuma, readChroma: readChroma}
			plan := makeVP8FramePlan(source, cfg, work)
			if plan.mbw != mbw || plan.mbh != mbh || len(plan.modes) != mbw*mbh {
				t.Fatalf("plan dimensions = %dx%d modes=%d, want %dx%d modes=%d", plan.mbw, plan.mbh, len(plan.modes), mbw, mbh, mbw*mbh)
			}
			firstPart, residualPart, err := encodeVP8FramePartitions(source, cfg, work, plan)
			if err != nil {
				t.Fatalf("encodeVP8FramePartitions failed: %v", err)
			}
			got := assembleVP8KeyFrame(width, height, firstPart, residualPart)
			want, err := encodeVP8KeyFrameConfig(readLuma, readChroma, bounds, width, height, cfg)
			if err != nil {
				t.Fatalf("encodeVP8KeyFrameConfig failed: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("planned frame differs from direct frame: got %d bytes, want %d bytes", len(got), len(want))
			}
		})
	}
}

func TestVP8ResidualBufferFitsMemoryBudget(t *testing.T) {
	if !vp8ResidualBufferFits(64, 64) {
		t.Fatal("1024x1024 macroblock grid did not fit the residual buffer budget")
	}
	if vp8ResidualBufferFits(1024, 1024) {
		t.Fatal("maximum VP8 macroblock grid unexpectedly fit the residual buffer budget")
	}
	if vp8ResidualBufferFits(0, 1) || vp8ResidualBufferFits(1, 0) {
		t.Fatal("empty macroblock grid fit the residual buffer budget")
	}
}

func TestVP8ResidualBufferChoosesSkipFromTokenCost(t *testing.T) {
	withZeroTokens := newVP8ResidualBuffer(8)
	for range 8 {
		for range vp8ResidualBlocksPerMacroblock {
			withZeroTokens.appendBlock(vp8PlaneY1SansY2, 0, vp8QuantizedBlock{}, 0)
		}
		withZeroTokens.finishMacroblock(false)
	}
	skipMap := withZeroTokens.candidateSkipMap(true)
	_, selectedSkipMap := withZeroTokens.chooseEntropyPlan(true, skipMap)
	if selectedSkipMap == nil {
		t.Fatal("zero residual token savings did not pay for the skip syntax")
	}

	withoutTokens := newVP8ResidualBuffer(8)
	for range 8 {
		withoutTokens.finishMacroblock(false)
	}
	skipMap = withoutTokens.candidateSkipMap(true)
	_, selectedSkipMap = withoutTokens.chooseEntropyPlan(true, skipMap)
	if selectedSkipMap != nil {
		t.Fatal("skip syntax was selected without residual token savings")
	}
	if got := withoutTokens.candidateSkipMap(false); got != nil {
		t.Fatal("disabled skip analysis returned a map")
	}
}

func TestVP8ResidualBufferChoosesJointSkipAndProbabilityPlan(t *testing.T) {
	buffer := newVP8ResidualBuffer(8)
	for macroblock := 0; macroblock < 8; macroblock++ {
		for block := 0; block < vp8ResidualBlocksPerMacroblock; block++ {
			coeff := vp8QuantizedBlock{}
			if macroblock >= 4 {
				coeff[0] = int16(1 + block%2)
			}
			buffer.appendBlock(vp8PlaneY1SansY2, 0, coeff, 0)
		}
		buffer.finishMacroblock(macroblock >= 4)
	}
	candidate := buffer.candidateSkipMap(true)
	probs, skipMap := buffer.chooseEntropyPlan(true, candidate)
	noSkipStats := buffer.tokenStats(nil)
	noSkipProbs := chooseVP8TokenProbsConfig(&noSkipStats, true)
	noSkipCost := buffer.entropyPlanBitCost(&noSkipProbs, nil)
	selectedCost := buffer.entropyPlanBitCost(&probs, skipMap)
	if selectedCost > noSkipCost {
		t.Fatalf("selected entropy plan cost = %d, want <= no-skip cost %d", selectedCost, noSkipCost)
	}
}

func TestVP8PassesIgnoreWorkspaceTopState(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 257, 17))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*3 + y*17),
				G: uint8(y*19 + x*5),
				B: uint8((x-y)*11 + x*y),
				A: 255,
			})
		}
	}

	readLuma := lumaReaderFor(img)
	readChroma := chromaReaderFor(img)
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	mbw := (width + 15) >> 4
	mbh := (height + 15) >> 4
	quant := vp8QuantForIndex(qualityToVP8QIndex(75))

	cleanWork := newVP8EncodeBuffers(mbw, mbh)
	if cleanWork.top == nil {
		t.Fatal("test image did not allocate top workspace")
	}
	cleanModes := analyzeVP8Modes(readLuma, readChroma, bounds, mbw, mbh, quant, cleanWork)
	dirtyWork := newVP8EncodeBuffers(mbw, mbh)
	fillVP8EncodeBuffers(dirtyWork, 0xd7)
	clear(dirtyWork.recY)
	dirtyModes := analyzeVP8Modes(readLuma, readChroma, bounds, mbw, mbh, quant, dirtyWork)
	if len(dirtyModes) != len(cleanModes) {
		t.Fatalf("dirty mode count = %d, want %d", len(dirtyModes), len(cleanModes))
	}
	for i := range cleanModes {
		if dirtyModes[i] != cleanModes[i] {
			t.Fatalf("mode[%d] with dirty top workspace = %#v, want %#v", i, dirtyModes[i], cleanModes[i])
		}
	}

	cleanStatsWork := newVP8EncodeBuffers(mbw, mbh)
	cleanStats := collectVP8TokenStats(readLuma, readChroma, bounds, mbw, mbh, quant, cleanModes, cleanStatsWork)
	dirtyStatsWork := newVP8EncodeBuffers(mbw, mbh)
	fillVP8EncodeBuffers(dirtyStatsWork, 0x93)
	clear(dirtyStatsWork.recY)
	dirtyStats := collectVP8TokenStats(readLuma, readChroma, bounds, mbw, mbh, quant, cleanModes, dirtyStatsWork)
	if dirtyStats != cleanStats {
		t.Fatal("token stats depend on dirty top workspace")
	}

	tokenProbs := chooseVP8TokenProbs(&cleanStats)
	cleanResidualWork := newVP8EncodeBuffers(mbw, mbh)
	cleanResidual := encodeVP8Residuals(readLuma, readChroma, bounds, width, height, mbw, mbh, quant, cleanModes, cleanResidualWork, &tokenProbs)
	dirtyResidualWork := newVP8EncodeBuffers(mbw, mbh)
	fillVP8EncodeBuffers(dirtyResidualWork, 0x41)
	clear(dirtyResidualWork.recY)
	dirtyResidual := encodeVP8Residuals(readLuma, readChroma, bounds, width, height, mbw, mbh, quant, cleanModes, dirtyResidualWork, &tokenProbs)
	if !bytes.Equal(dirtyResidual, cleanResidual) {
		t.Fatal("residual stream depends on dirty top workspace")
	}
}

func fillVP8EncodeBuffers(work *vp8EncodeBuffers, value uint8) {
	for _, buf := range [][]uint8{work.recY, work.recCb, work.recCr} {
		for i := range buf {
			buf[i] = value
		}
	}
	if work.top == nil {
		return
	}
	for i := range work.top.modes {
		work.top.modes[i] = vp8MBMode{
			useY16: value&1 != 0,
			yMode:  value,
			cMode:  value,
		}
		for j := range work.top.modes[i].y4Modes {
			work.top.modes[i].y4Modes[j] = value
		}
	}
	for _, states := range [][][4]uint8{work.top.upPred, work.top.upY, work.top.upUV} {
		for i := range states {
			states[i] = [4]uint8{value, value, value, value}
		}
	}
	for i := range work.top.upY16 {
		work.top.upY16[i] = value
	}
}

func TestVP8RecordBlockTokensCollectsBranches(t *testing.T) {
	var coeff vp8QuantizedBlock
	coeff[0] = 1
	var stats vp8TokenStats
	if nz := vp8RecordBlockTokens(&stats, vp8PlaneY1SansY2, 0, coeff); nz != 1 {
		t.Fatalf("non-zero flag = %d, want 1", nz)
	}
	if stats[vp8PlaneY1SansY2][0][0][0].one == 0 {
		t.Fatal("EOB branch count was not recorded")
	}
	if stats[vp8PlaneY1SansY2][0][0][1].one == 0 {
		t.Fatal("non-zero coefficient branch count was not recorded")
	}
}

func TestVP8RDLambdaIncreasesWithQuantizer(t *testing.T) {
	highQuality := newVP8RDConfig(vp8QuantForIndex(qualityToVP8QIndex(90)))
	lowQuality := newVP8RDConfig(vp8QuantForIndex(qualityToVP8QIndex(10)))
	if lowQuality.yLambda <= highQuality.yLambda {
		t.Fatalf("low quality luma lambda = %d, want greater than high quality lambda %d", lowQuality.yLambda, highQuality.yLambda)
	}
	if lowQuality.uvLambda <= highQuality.uvLambda {
		t.Fatalf("low quality chroma lambda = %d, want greater than high quality lambda %d", lowQuality.uvLambda, highQuality.uvLambda)
	}
}

func TestVP8TokenProbabilitySelectionKeepsSmallSamples(t *testing.T) {
	var stats vp8TokenStats
	stats[vp8PlaneY1SansY2][1][0][0] = vp8TokenBranchCounts{zero: 1, one: 1}
	probs := chooseVP8TokenProbs(&stats)
	if probs[vp8PlaneY1SansY2][1][0][0] != vp8DefaultTokenProbs[vp8PlaneY1SansY2][1][0][0] {
		t.Fatal("small token sample changed probability")
	}
}

func TestVP8TokenProbabilitySelectionUpdatesWhenWorthwhile(t *testing.T) {
	var stats vp8TokenStats
	current := vp8DefaultTokenProbs[vp8PlaneY1SansY2][1][0][0]
	if current < 128 {
		stats[vp8PlaneY1SansY2][1][0][0] = vp8TokenBranchCounts{zero: 1000, one: 1}
	} else {
		stats[vp8PlaneY1SansY2][1][0][0] = vp8TokenBranchCounts{zero: 1, one: 1000}
	}
	probs := chooseVP8TokenProbs(&stats)
	got := probs[vp8PlaneY1SansY2][1][0][0]
	if got == vp8DefaultTokenProbs[vp8PlaneY1SansY2][1][0][0] {
		t.Fatal("token probability was not updated")
	}
	if got != estimateVP8TokenProb(stats[vp8PlaneY1SansY2][1][0][0]) {
		t.Fatalf("token probability = %d, want estimated probability", got)
	}
}

func TestVP8TokenProbabilitySelectionCanBeDisabled(t *testing.T) {
	var stats vp8TokenStats
	stats[vp8PlaneY1SansY2][1][0][0] = vp8TokenBranchCounts{zero: 1000, one: 1}
	probs := chooseVP8TokenProbsConfig(&stats, false)
	if probs != vp8DefaultTokenProbs {
		t.Fatal("disabled token probability updates changed defaults")
	}
}

func TestVP8FirstPartitionWritesTokenProbUpdate(t *testing.T) {
	probs := vp8DefaultTokenProbs
	probs[vp8PlaneY1SansY2][1][0][0] = 17
	firstPart, err := vp8FirstPartition(1, 1, qualityToVP8QIndex(75), vp8QuantDeltas{}, vp8LoopFilterForIndex(qualityToVP8QIndex(75)), nil, []vp8MBMode{{
		useY16: true,
		yMode:  vp8PredDC,
		cMode:  vp8PredDC,
	}}, probs, nil, 0)
	if err != nil {
		t.Fatalf("vp8FirstPartition failed: %v", err)
	}

	var r testVP8PartitionReader
	r.init(firstPart)
	readVP8FirstPartitionHeaderBeforeTokenProbs(t, &r)
	got := readVP8FirstPartitionTokenProbs(t, &r)
	if got[vp8PlaneY1SansY2][1][0][0] != 17 {
		t.Fatalf("token probability = %d, want 17", got[vp8PlaneY1SansY2][1][0][0])
	}
	if got[vp8PlaneY1SansY2][1][0][1] != vp8DefaultTokenProbs[vp8PlaneY1SansY2][1][0][1] {
		t.Fatal("unchanged token probability did not keep the default value")
	}
}

func TestEncodeLossyWithAlphaWritesExtendedChunks(t *testing.T) {
	img := image.NewNRGBA(image.Rect(4, 5, 7, 7))
	wantAlpha := []byte{255, 128, 0, 64, 200, 255}
	i := 0
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(20 + i*7),
				G: uint8(40 + i*9),
				B: uint8(60 + i*11),
				A: wantAlpha[i],
			})
			i++
		}
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{Compression: CompressionLossy}); err != nil {
		t.Fatalf("Encode lossy with alpha failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3", len(chunks))
	}
	if chunks[0].name != "VP8X" {
		t.Fatalf("first chunk = %q, want VP8X", chunks[0].name)
	}
	if len(chunks[0].payload) != vp8xPayloadSize {
		t.Fatalf("VP8X payload size = %d, want %d", len(chunks[0].payload), vp8xPayloadSize)
	}
	if chunks[0].payload[0] != vp8xAlphaFlag {
		t.Fatalf("VP8X flags = %#02x, want %#02x", chunks[0].payload[0], vp8xAlphaFlag)
	}
	if !bytes.Equal(chunks[0].payload[1:4], []byte{0, 0, 0}) {
		t.Fatalf("VP8X reserved bytes = % x, want 00 00 00", chunks[0].payload[1:4])
	}
	if widthMinusOne := readUint24LE(chunks[0].payload[4:7]); widthMinusOne != 2 {
		t.Fatalf("VP8X width minus one = %d, want 2", widthMinusOne)
	}
	if heightMinusOne := readUint24LE(chunks[0].payload[7:10]); heightMinusOne != 1 {
		t.Fatalf("VP8X height minus one = %d, want 1", heightMinusOne)
	}

	if chunks[1].name != "ALPH" {
		t.Fatalf("second chunk = %q, want ALPH", chunks[1].name)
	}
	if len(chunks[1].payload) != 1+len(wantAlpha) {
		t.Fatalf("ALPH payload size = %d, want %d", len(chunks[1].payload), 1+len(wantAlpha))
	}
	if chunks[1].payload[0] != 0 {
		t.Fatalf("ALPH header = %#02x, want 0", chunks[1].payload[0])
	}
	if !bytes.Equal(chunks[1].payload[1:], wantAlpha) {
		t.Fatalf("ALPH data = %v, want %v", chunks[1].payload[1:], wantAlpha)
	}

	if chunks[2].name != "VP8 " {
		t.Fatalf("third chunk = %q, want VP8 ", chunks[2].name)
	}
	assertLossyVP8Frame(t, chunks[2].payload, 3, 2)
}

func TestEncodeLossyWithFilteredAlphaWritesCompressedALPH(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 12, 1))
	for x := 0; x < img.Rect.Dx(); x++ {
		img.SetNRGBA(x, 0, color.NRGBA{
			R: uint8(10 + x),
			G: uint8(20 + x),
			B: uint8(30 + x),
			A: uint8((x + 1) * 7),
		})
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{Compression: CompressionLossy}); err != nil {
		t.Fatalf("Encode lossy with filtered alpha failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3", len(chunks))
	}
	if chunks[1].name != "ALPH" {
		t.Fatalf("second chunk = %q, want ALPH", chunks[1].name)
	}
	if chunks[1].payload[0]&0x03 != alphCompressionVP8L {
		t.Fatalf("ALPH compression = %d, want %d", chunks[1].payload[0]&0x03, alphCompressionVP8L)
	}
	if chunks[1].payload[0]>>2&0x03 != alphFilterHorizontal {
		t.Fatalf("ALPH filter = %d, want %d", chunks[1].payload[0]>>2&0x03, alphFilterHorizontal)
	}
	if len(chunks[1].payload) >= 1+img.Rect.Dx()*img.Rect.Dy() {
		t.Fatalf("compressed ALPH payload size = %d, want smaller than raw %d", len(chunks[1].payload), 1+img.Rect.Dx()*img.Rect.Dy())
	}
	assertLossyVP8Frame(t, chunks[2].payload, 12, 1)
}

func TestEncodeLossyModeFastNarrowsAlphaSearch(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 12, 1))
	for x := 0; x < img.Rect.Dx(); x++ {
		img.SetNRGBA(x, 0, color.NRGBA{
			R: uint8(10 + x),
			G: uint8(20 + x),
			B: uint8(30 + x),
			A: uint8((x + 1) * 7),
		})
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{Compression: CompressionLossy, Mode: ModeFast}); err != nil {
		t.Fatalf("Encode lossy ModeFast with alpha failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3", len(chunks))
	}
	if chunks[1].name != "ALPH" {
		t.Fatalf("second chunk = %q, want ALPH", chunks[1].name)
	}
	if chunks[1].payload[0]>>2&0x03 != alphFilterNone {
		t.Fatalf("ALPH filter = %d, want none for ModeFast", chunks[1].payload[0]>>2&0x03)
	}
	assertLossyVP8Frame(t, chunks[2].payload, 12, 1)
}

func TestEncodeLossyModeFastUsesDefaultTokenProbabilities(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{
		kind:   benchmarkImageGradient,
		width:  32,
		height: 32,
	})

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{
		Compression: CompressionLossy,
		Mode:        ModeFast,
		Quality:     75,
	}); err != nil {
		t.Fatalf("Encode lossy ModeFast failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1", len(chunks))
	}
	firstPart := readVP8FirstPartition(t, chunks[0].payload)
	var r testVP8PartitionReader
	r.init(firstPart)
	readVP8FirstPartitionHeaderBeforeTokenProbs(t, &r)
	if got := readVP8FirstPartitionTokenProbs(t, &r); got != vp8DefaultTokenProbs {
		t.Fatal("ModeFast wrote token probability updates")
	}
	if r.readBit(128) {
		t.Fatal("ModeFast enabled macroblock skip probability")
	}
}

func TestEncodeLossyUsesMacroblockSkipWhenResidualsAreZero(t *testing.T) {
	bounds := image.Rect(0, 0, 64, 64)
	img := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 80, G: 120, B: 160, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{
		Compression: CompressionLossy,
		Quality:     75,
	}); err != nil {
		t.Fatalf("Encode lossy failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1", len(chunks))
	}
	assertLossyVP8Frame(t, chunks[0].payload, bounds.Dx(), bounds.Dy())
	firstPart := readVP8FirstPartition(t, chunks[0].payload)
	var r testVP8PartitionReader
	r.init(firstPart)
	readVP8FirstPartitionHeaderBeforeTokenProbs(t, &r)
	readVP8FirstPartitionTokenProbs(t, &r)
	if !r.readBit(128) {
		t.Fatal("macroblock skip probability was not enabled")
	}
	prob := r.readUint(128, 8)
	if prob == 0 || prob == 255 {
		t.Fatalf("macroblock skip probability = %d, want interior probability", prob)
	}
}

func TestEncodeLossyWithBinaryAlphaWritesCompressedALPH(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 1))
	for x := 0; x < img.Rect.Dx(); x++ {
		alpha := uint8(0)
		if x%2 == 0 {
			alpha = 255
		}
		img.SetNRGBA(x, 0, color.NRGBA{
			R: uint8(100 + x),
			G: uint8(80 + x),
			B: uint8(60 + x),
			A: alpha,
		})
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{Compression: CompressionLossy}); err != nil {
		t.Fatalf("Encode lossy with binary alpha failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3", len(chunks))
	}
	if chunks[1].name != "ALPH" {
		t.Fatalf("second chunk = %q, want ALPH", chunks[1].name)
	}
	if chunks[1].payload[0]&0x03 != alphCompressionVP8L {
		t.Fatalf("ALPH compression = %d, want %d", chunks[1].payload[0]&0x03, alphCompressionVP8L)
	}
	if chunks[1].payload[0]>>2&0x03 != alphFilterNone {
		t.Fatalf("ALPH filter = %d, want %d", chunks[1].payload[0]>>2&0x03, alphFilterNone)
	}
	if len(chunks[1].payload) >= 1+img.Rect.Dx()*img.Rect.Dy() {
		t.Fatalf("compressed ALPH payload size = %d, want smaller than raw %d", len(chunks[1].payload), 1+img.Rect.Dx()*img.Rect.Dy())
	}
	assertLossyVP8Frame(t, chunks[2].payload, 16, 1)
}

func TestEncodeLossyWithMultiSymbolAlphaWritesCompressedALPH(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1024, 1))
	alphaValues := [...]uint8{0, 100, 200}
	for x := 0; x < img.Rect.Dx(); x++ {
		alpha := alphaValues[x%len(alphaValues)]
		img.SetNRGBA(x, 0, color.NRGBA{
			R: uint8(x),
			G: uint8(x >> 1),
			B: uint8(x >> 2),
			A: alpha,
		})
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{Compression: CompressionLossy}); err != nil {
		t.Fatalf("Encode lossy with multi-symbol alpha failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3", len(chunks))
	}
	if chunks[1].name != "ALPH" {
		t.Fatalf("second chunk = %q, want ALPH", chunks[1].name)
	}
	if chunks[1].payload[0]&0x03 != alphCompressionVP8L {
		t.Fatalf("ALPH compression = %d, want %d", chunks[1].payload[0]&0x03, alphCompressionVP8L)
	}
	if len(chunks[1].payload) >= 1+img.Rect.Dx()*img.Rect.Dy() {
		t.Fatalf("compressed ALPH payload size = %d, want smaller than raw %d", len(chunks[1].payload), 1+img.Rect.Dx()*img.Rect.Dy())
	}
	assertLossyVP8Frame(t, chunks[2].payload, 1024, 1)
}

func TestEncodeLossyWithAlphaRunsUsesBackwardReferences(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4096, 1))
	for x := 0; x < img.Rect.Dx(); x++ {
		alpha := uint8(32)
		if x/512%2 == 1 {
			alpha = 220
		}
		img.SetNRGBA(x, 0, color.NRGBA{
			R: uint8(x),
			G: uint8(x >> 1),
			B: uint8(x >> 2),
			A: alpha,
		})
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{Compression: CompressionLossy}); err != nil {
		t.Fatalf("Encode lossy with alpha runs failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3", len(chunks))
	}
	if chunks[1].name != "ALPH" {
		t.Fatalf("second chunk = %q, want ALPH", chunks[1].name)
	}
	if chunks[1].payload[0]&0x03 != alphCompressionVP8L {
		t.Fatalf("ALPH compression = %d, want %d", chunks[1].payload[0]&0x03, alphCompressionVP8L)
	}
	if len(chunks[1].payload) >= 300 {
		t.Fatalf("compressed ALPH payload size = %d, want less than 300", len(chunks[1].payload))
	}
	assertLossyVP8Frame(t, chunks[2].payload, 4096, 1)
}

func TestLossyAlphaConfigSkipsSpatialCandidates(t *testing.T) {
	img := newAlphaSizeEstimateNeighborhoodImage()
	readPixel := pixelReaderFor(img)
	bounds := img.Bounds()
	analysis := analyzeLossyAlphaConfig(readPixel, bounds, bounds.Dx(), bounds.Dy(), lossyAlphaConfigForMode(ModeLowMemory))
	candidates := appendAlphaPayloadCandidatesConfig(nil, analysis, lossyAlphaConfigForMode(ModeLowMemory))
	for _, candidate := range candidates {
		if candidate.code.rowCopy {
			t.Fatal("ModeLowMemory alpha candidate kept spatial row-copy references")
		}
	}
}

func TestAlphaCodeLengthTokensUseZeroRunCodes(t *testing.T) {
	var lengths [nLiteralCodes + nLengthCodes]uint8
	lengths[0] = 1
	lengths[100] = 2
	lengths[260] = 3

	tokens := alphaCodeLengthTokens(lengths[:])
	if len(tokens) >= 261 {
		t.Fatalf("code length token count = %d, want less than 261", len(tokens))
	}
	foundBigZeroRun := false
	for _, token := range tokens {
		if token.symbol == alphaCodeLengthRepeatZeroBig {
			foundBigZeroRun = true
			break
		}
	}
	if !foundBigZeroRun {
		t.Fatal("missing long zero-run code length token")
	}
	gotTokenBits, gotTokenCount := alphaCodeLengthTokenBits(lengths[:])
	if gotTokenCount != len(tokens) {
		t.Fatalf("code length token count from bit scan = %d, want %d", gotTokenCount, len(tokens))
	}
	var wantTokenBits uint64
	codeLengthCodeLengths, _ := alphaCodeLengthCodeLengthsForUsage(alphaCodeLengthCodeUsageForTokens(tokens))
	for _, token := range tokens {
		wantTokenBits += uint64(codeLengthCodeLengths[token.symbol] + token.extraBits)
	}
	if gotTokenBits != wantTokenBits {
		t.Fatalf("code length token bits = %d, want %d", gotTokenBits, wantTokenBits)
	}

	got := expandAlphaCodeLengthTokensForTest(tokens, 261)
	for i, want := range lengths[:261] {
		if got[i] != want {
			t.Fatalf("expanded code length at %d = %d, want %d", i, got[i], want)
		}
	}
}

func TestAlphaCodeLengthTokensUseRepeatPreviousCode(t *testing.T) {
	var lengths [nLiteralCodes + nLengthCodes]uint8
	for i := 4; i < 12; i++ {
		lengths[i] = 5
	}
	lengths[128] = 3

	tokens := alphaCodeLengthTokens(lengths[:])
	foundRepeat := false
	for _, token := range tokens {
		if token.symbol == alphaCodeLengthRepeatPrevious {
			foundRepeat = true
			if token.extraBits != 2 {
				t.Fatalf("repeat-previous extra bits = %d, want 2", token.extraBits)
			}
		}
	}
	if !foundRepeat {
		t.Fatal("missing repeat-previous code length token")
	}
	if got := alphaCodeLengthCodeCountForTokens(tokens); got < 9 {
		t.Fatalf("code length code count = %d, want at least 9 for repeat-previous symbol", got)
	}

	gotTokenBits, gotTokenCount := alphaCodeLengthTokenBits(lengths[:])
	if gotTokenCount != len(tokens) {
		t.Fatalf("code length token count from bit scan = %d, want %d", gotTokenCount, len(tokens))
	}
	var wantTokenBits uint64
	codeLengthCodeLengths, _ := alphaCodeLengthCodeLengthsForUsage(alphaCodeLengthCodeUsageForTokens(tokens))
	for _, token := range tokens {
		wantTokenBits += uint64(codeLengthCodeLengths[token.symbol] + token.extraBits)
	}
	if gotTokenBits != wantTokenBits {
		t.Fatalf("code length token bits = %d, want %d", gotTokenBits, wantTokenBits)
	}

	got := expandAlphaCodeLengthTokensForTest(tokens, alphaCodeLengthLimit(lengths[:]))
	for i, want := range lengths[:alphaCodeLengthLimit(lengths[:])] {
		if got[i] != want {
			t.Fatalf("expanded code length at %d = %d, want %d", i, got[i], want)
		}
	}

	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	bits := newBitWriter(bw)
	writeAlphaNormalTree(bits, lengths[:])
	if err := bits.flush(); err != nil {
		t.Fatalf("bit flush failed: %v", err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatalf("buffer flush failed: %v", err)
	}

	r := testBitReader{data: buf.Bytes()}
	tree, err := decodeEncoderTree(&r, len(lengths))
	if err != nil {
		t.Fatalf("decodeEncoderTree failed: %v", err)
	}
	for symbol, want := range lengths {
		if tree.lengths[symbol] != want {
			t.Fatalf("length[%d] = %d, want %d", symbol, tree.lengths[symbol], want)
		}
	}
}

func TestAlphaCodeLengthCodeLengthsUseFrequencyCost(t *testing.T) {
	var usage [alphaCodeLengthCodeCount]uint32
	usage[0] = 1000
	usage[1] = 1
	usage[2] = 1
	usage[3] = 1
	usage[4] = 1

	lengths, nCodes := alphaCodeLengthCodeLengthsForUsage(usage)
	if nCodes != alphaCodeLengthCodeCountForUsage(usage) {
		t.Fatalf("code length code count = %d, want %d", nCodes, alphaCodeLengthCodeCountForUsage(usage))
	}
	if lengths[0] >= lengths[1] {
		t.Fatalf("frequent token length = %d, rare token length = %d, want frequent token shorter", lengths[0], lengths[1])
	}
	for symbol, count := range usage {
		if count == 0 {
			if lengths[symbol] != 0 {
				t.Fatalf("unused token length[%d] = %d, want 0", symbol, lengths[symbol])
			}
			continue
		}
		if lengths[symbol] == 0 || lengths[symbol] > alphaCodeLengthCodeMaxLength {
			t.Fatalf("used token length[%d] = %d, want 1..%d", symbol, lengths[symbol], alphaCodeLengthCodeMaxLength)
		}
	}
	if got := alphaCodeLengthCodeKraftSumForTest(lengths); got != alphaCodeLengthCodeKraft {
		t.Fatalf("code length code Kraft sum = %d, want %d", got, alphaCodeLengthCodeKraft)
	}
}

func TestHuffmanCodeLengthsFallBackWhenTreeWouldExceedVP8LLimit(t *testing.T) {
	var counts [nLiteralCodes + nLengthCodes]uint32
	counts[0], counts[1] = 1, 1
	for i := 2; i < 46; i++ {
		counts[i] = counts[i-1] + counts[i-2]
	}

	lengths, ok := huffmanCodeLengths(counts)
	if !ok {
		t.Fatal("huffmanCodeLengths returned false")
	}
	for symbol, length := range lengths {
		if length > 15 {
			t.Fatalf("code length for symbol %d = %d, want at most 15", symbol, length)
		}
	}
	if got := huffmanKraftSumForTest(lengths); got != 1<<15 {
		t.Fatalf("Kraft sum = %d, want %d", got, 1<<15)
	}
	if lengths[45] > lengths[0] {
		t.Fatalf("frequent symbol length = %d, rare symbol length = %d", lengths[45], lengths[0])
	}
}

func TestCanonicalCodesHandleSparseAndMaxLengthSymbols(t *testing.T) {
	const maxColorCacheGreenCodes = nLiteralCodes + nLengthCodes + 1<<vp8lMaxColorCacheBits
	channelLengths := make([]uint8, maxColorCacheGreenCodes)
	channelLengths[0] = 1
	channelLengths[nLiteralCodes+nLengthCodes-1] = 2
	channelLengths[maxColorCacheGreenCodes-1] = 2
	channelCodes := vp8lCanonicalCodes(channelLengths)
	assertCanonicalCodesForTest(t, channelLengths, channelCodes)
	if channelCodes[0] != 0 {
		t.Fatalf("channel code for first symbol = %b, want 0", channelCodes[0])
	}
	if channelCodes[nLiteralCodes+nLengthCodes-1] != 2 {
		t.Fatalf("channel code for sparse length-2 symbol = %b, want 10", channelCodes[nLiteralCodes+nLengthCodes-1])
	}
	if channelCodes[maxColorCacheGreenCodes-1] != 3 {
		t.Fatalf("channel code for high color-cache symbol = %b, want 11", channelCodes[maxColorCacheGreenCodes-1])
	}

	var greenLengths [nLiteralCodes + nLengthCodes]uint8
	greenLengths[0] = 1
	greenLengths[nLiteralCodes-1] = 2
	greenLengths[nLiteralCodes+nLengthCodes-1] = 2
	greenCodes := canonicalCodes(greenLengths)
	assertCanonicalCodesForTest(t, greenLengths[:], greenCodes[:])
	if greenCodes[nLiteralCodes+nLengthCodes-1] != 3 {
		t.Fatalf("green code for max length symbol = %b, want 11", greenCodes[nLiteralCodes+nLengthCodes-1])
	}

	var distanceLengths [nDistanceCodes]uint8
	distanceLengths[0] = 1
	distanceLengths[nDistanceCodes-2] = 2
	distanceLengths[nDistanceCodes-1] = 2
	distanceCodes := canonicalDistanceCodes(distanceLengths)
	assertCanonicalCodesForTest(t, distanceLengths[:], distanceCodes[:])
	if distanceCodes[nDistanceCodes-1] != 3 {
		t.Fatalf("distance code for max symbol = %b, want 11", distanceCodes[nDistanceCodes-1])
	}
}

func TestCanonicalCodesHandleFullLengthLimitTree(t *testing.T) {
	var lengths [nLiteralCodes + nLengthCodes]uint8
	for symbol := 0; symbol < len(lengths); symbol++ {
		lengths[symbol] = 15
	}
	codes := canonicalCodes(lengths)
	assertCanonicalCodesForTest(t, lengths[:], codes[:])
	if codes[0] != 0 {
		t.Fatalf("first canonical code = %b, want 0", codes[0])
	}
	if codes[len(codes)-1] != uint16(len(codes)-1) {
		t.Fatalf("last canonical code = %b, want %b", codes[len(codes)-1], uint16(len(codes)-1))
	}
}

func TestAlphaNormalTreeCodeLengthLimitUsesTokenCount(t *testing.T) {
	var lengths [nLiteralCodes + nLengthCodes]uint8
	lengths[0] = 2
	lengths[3] = 3
	lengths[128] = 3
	lengths[260] = 2
	tokens := alphaCodeLengthTokens(lengths[:])
	if len(tokens) >= alphaCodeLengthLimit(lengths[:]) {
		t.Fatalf("token count = %d, want less than expanded limit %d", len(tokens), alphaCodeLengthLimit(lengths[:]))
	}

	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	bits := newBitWriter(bw)
	writeAlphaNormalTree(bits, lengths[:])
	if err := bits.flush(); err != nil {
		t.Fatalf("bit flush failed: %v", err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatalf("buffer flush failed: %v", err)
	}

	r := testBitReader{data: buf.Bytes()}
	tree, err := decodeEncoderTree(&r, len(lengths))
	if err != nil {
		t.Fatalf("decodeEncoderTree failed: %v", err)
	}
	for symbol, want := range lengths {
		if tree.lengths[symbol] != want {
			t.Fatalf("length[%d] = %d, want %d", symbol, tree.lengths[symbol], want)
		}
	}
}

func TestAlphaNormalTreeTrimsCodeLengthCodeAlphabet(t *testing.T) {
	var lengths [nLiteralCodes + nLengthCodes]uint8
	lengths[0] = 1
	lengths[3] = 2
	lengths[128] = 2

	tokens := alphaCodeLengthTokens(lengths[:])
	nCodes := alphaCodeLengthCodeCountForTokens(tokens)
	if nCodes >= len(normalCodeLengthCodeOrder) {
		t.Fatalf("code length code count = %d, want trimmed below %d", nCodes, len(normalCodeLengthCodeOrder))
	}
	if got := alphaCodeLengthCodeCountForLengths(lengths[:]); got != nCodes {
		t.Fatalf("code length code count from lengths = %d, want %d", got, nCodes)
	}

	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	bits := newBitWriter(bw)
	writeAlphaNormalTree(bits, lengths[:])
	if err := bits.flush(); err != nil {
		t.Fatalf("bit flush failed: %v", err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatalf("buffer flush failed: %v", err)
	}

	header := testBitReader{data: buf.Bytes()}
	useSimple, err := header.read(1)
	if err != nil {
		t.Fatalf("read tree type failed: %v", err)
	}
	if useSimple != 0 {
		t.Fatalf("tree type = %d, want normal", useSimple)
	}
	gotNCodesMinusFour, err := header.read(4)
	if err != nil {
		t.Fatalf("read code length code count failed: %v", err)
	}
	if got := int(gotNCodesMinusFour) + 4; got != nCodes {
		t.Fatalf("encoded code length code count = %d, want %d", got, nCodes)
	}

	r := testBitReader{data: buf.Bytes()}
	tree, err := decodeEncoderTree(&r, len(lengths))
	if err != nil {
		t.Fatalf("decodeEncoderTree failed: %v", err)
	}
	for symbol, want := range lengths {
		if tree.lengths[symbol] != want {
			t.Fatalf("length[%d] = %d, want %d", symbol, tree.lengths[symbol], want)
		}
	}
}

func TestAlphaLZ77PlanUsesPreviousRowDistance(t *testing.T) {
	row := []uint8{4, 9, 16, 25, 36, 49, 64, 81}
	var plan alphaResidualPlan
	plan.observeLZ77Row(row, nil, false)
	plan.observeLZ77Row(row, row, true)
	plan.flushRLE()

	aboveSymbol := vp8lDistancePrefixCode(alphaDistanceAbove).code
	previousSymbol := vp8lDistancePrefixCode(alphaDistancePrevious).code
	if plan.distanceCounts[aboveSymbol] == 0 {
		t.Fatal("missing previous-row distance reference")
	}
	if plan.distanceCounts[previousSymbol] != 0 {
		t.Fatalf("previous-pixel distance references = %d, want 0", plan.distanceCounts[previousSymbol])
	}
	prefix := vp8lPrefixCode(len(row))
	if got := plan.counts[nLiteralCodes+prefix.code]; got == 0 {
		t.Fatalf("missing copy length prefix code %d", prefix.code)
	}
}

func TestAlphaLZ77PlanUsesPreviousRowNeighborhoodDistances(t *testing.T) {
	previous := []uint8{10, 20, 30, 40, 50, 60, 70, 80, 90}

	topLeft := []uint8{99, 10, 20, 30, 40, 50, 60, 70, 80}
	var topLeftPlan alphaResidualPlan
	topLeftPlan.observeLZ77Row(topLeft, previous, true)
	topLeftPlan.flushRLE()
	if topLeftPlan.distanceCounts[vp8lDistancePrefixCode(alphaDistanceTopLeft).code] == 0 {
		t.Fatal("missing top-left distance reference")
	}

	topRight := []uint8{20, 30, 40, 50, 60, 70, 80, 90, 99}
	var topRightPlan alphaResidualPlan
	topRightPlan.observeLZ77Row(topRight, previous, true)
	topRightPlan.flushRLE()
	if topRightPlan.distanceCounts[vp8lDistancePrefixCode(alphaDistanceTopRight).code] == 0 {
		t.Fatal("missing top-right distance reference")
	}
}

func TestAlphaLZ77PlanUsesExpandedPreviousRowDistances(t *testing.T) {
	previous := []uint8{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	current := []uint8{201, 202, 10, 20, 30, 40, 50, 60, 70, 80}
	var plan alphaResidualPlan
	plan.observeLZ77Row(current, previous, true)
	plan.flushRLE()
	distanceCode, ok := vp8lDistanceCodeForPositionDistance(len(previous)+2, len(previous))
	if !ok {
		t.Fatal("expanded previous-row distance code is unavailable")
	}
	symbol := vp8lDistancePrefixCode(distanceCode).code
	if plan.distanceCounts[symbol] == 0 {
		t.Fatalf("missing expanded previous-row distance symbol %d", symbol)
	}
}

func TestAlphaDistanceCodeUsesNormalTreeForNeighborhoodDistances(t *testing.T) {
	var plan alphaResidualPlan
	plan.observeCopy(alphaMinBackwardRefLength, alphaDistanceAbove)
	plan.observeCopy(alphaMinBackwardRefLength, alphaDistancePrevious)
	plan.observeCopy(alphaMinBackwardRefLength, alphaDistanceTopLeft)
	plan.observeCopy(alphaMinBackwardRefLength, alphaDistanceTopRight)
	code, ok := alphaCodeFor(plan)
	if !ok {
		t.Fatal("alphaCodeFor returned false")
	}
	if !code.distanceNormal {
		t.Fatal("distance tree is not normal")
	}
	for _, distanceCode := range []int{
		alphaDistanceAbove,
		alphaDistancePrevious,
		alphaDistanceTopLeft,
		alphaDistanceTopRight,
	} {
		symbol := vp8lDistancePrefixCode(distanceCode).code
		if code.distanceLengths[symbol] == 0 {
			t.Fatalf("distance symbol %d has zero code length", symbol)
		}
	}
}

func TestAlphaOptimalPlansImproveGreedyCandidate(t *testing.T) {
	found := false
	for _, img := range []*image.NRGBA{
		newAlphaSizeEstimateRunsImage(),
		newAlphaSizeEstimateNeighborhoodImage(),
	} {
		bounds := img.Bounds()
		analysis := analyzeLossyAlphaConfig(pixelReaderFor(img), bounds, bounds.Dx(), bounds.Dy(), lossyAlphaConfigForMode(ModeBestCompression))
		for filter, optimal := range analysis.optimalResiduals {
			if len(optimal.tokens) == 0 {
				continue
			}
			greedy := analysis.lz77Residuals[filter]
			greedyCode, ok := alphaCodeFor(greedy)
			if !ok {
				t.Fatalf("filter %d greedy code is unavailable", filter)
			}
			greedyCode.lz77 = true
			greedyCode.rowCopy = true
			optimalCode, ok := alphaCodeFor(optimal)
			if !ok {
				t.Fatalf("filter %d optimal code is unavailable", filter)
			}
			optimalCode.lz77 = true
			if got, wantMax := alphaVP8LStreamSize(optimal, optimalCode), alphaVP8LStreamSize(greedy, greedyCode); got >= wantMax {
				t.Fatalf("filter %d optimal size = %d, want < greedy size %d", filter, got, wantMax)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no alpha fixture produced an improved optimal plan")
	}
}

func TestAlphaPayloadCandidateSizeMatchesEncodedStream(t *testing.T) {
	for _, img := range []*image.NRGBA{
		newAlphaSizeEstimateRunsImage(),
		newAlphaSizeEstimateNeighborhoodImage(),
	} {
		readPixel := pixelReaderFor(img)
		bounds := img.Bounds()
		width, height := bounds.Dx(), bounds.Dy()
		cfg := lossyAlphaConfigForMode(ModeBestCompression)
		analysis := analyzeLossyAlphaConfig(readPixel, bounds, width, height, cfg)
		candidates := appendAlphaPayloadCandidatesConfig(nil, analysis, cfg)
		if len(candidates) == 0 {
			t.Fatal("no alpha payload candidates")
		}
		for _, candidate := range candidates {
			stream, err := encodeAlphaVP8LStream(readPixel, bounds, width, height, candidate.filter, candidate.plan, candidate.code)
			if err != nil {
				t.Fatalf("encodeAlphaVP8LStream failed: %v", err)
			}
			want := uint64(1 + len(stream))
			if got := alphaPayloadCandidateSize(candidate); got != want {
				t.Fatalf("candidate size = %d, want %d", got, want)
			}
		}
	}
}

func newAlphaSizeEstimateRunsImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 96, 6))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			alpha := uint8(32)
			if (x/12+y)%2 == 1 {
				alpha = 220
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*7 + y*3),
				G: uint8(y*11 + x),
				B: uint8(x*5 + y*13),
				A: alpha,
			})
		}
	}
	return img
}

func newAlphaSizeEstimateNeighborhoodImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 96, 9))
	for y := 0; y < img.Rect.Dy(); y++ {
		shift := 0
		if y%2 == 1 {
			shift = -1
		}
		for x := 0; x < img.Rect.Dx(); x++ {
			index := x + shift
			if index < 0 {
				index += img.Rect.Dx()
			}
			alpha := uint8(32 + (index*37)%191)
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*3 + y),
				G: uint8(y*5 + x/2),
				B: uint8((x+y)*2 + x*y/17),
				A: alpha,
			})
		}
	}
	return img
}

func huffmanKraftSumForTest(lengths [nLiteralCodes + nLengthCodes]uint8) int {
	sum := 0
	for _, length := range lengths {
		if length != 0 {
			sum += 1 << (15 - length)
		}
	}
	return sum
}

func alphaCodeLengthCodeKraftSumForTest(lengths [alphaCodeLengthCodeCount]uint8) int {
	sum := 0
	for _, length := range lengths {
		if length != 0 {
			sum += 1 << (alphaCodeLengthCodeMaxLength - length)
		}
	}
	return sum
}

func assertCanonicalCodesForTest(t *testing.T, lengths []uint8, codes []uint16) {
	t.Helper()
	if len(codes) < len(lengths) {
		t.Fatalf("code count = %d, want at least %d", len(codes), len(lengths))
	}
	var seen [1 << 15]bool
	previousLength := uint8(0)
	previousCode := uint16(0)
	havePrevious := false
	for symbol, length := range lengths {
		if length == 0 {
			if codes[symbol] != 0 {
				t.Fatalf("unused code[%d] = %b, want 0", symbol, codes[symbol])
			}
			continue
		}
		if length > 15 {
			t.Fatalf("length[%d] = %d, want at most 15", symbol, length)
		}
		code := codes[symbol]
		if code >= 1<<length {
			t.Fatalf("code[%d] = %b exceeds length %d", symbol, code, length)
		}
		prefix := int(code) << (15 - length)
		span := 1 << (15 - length)
		for i := 0; i < span; i++ {
			if seen[prefix+i] {
				t.Fatalf("code[%d] = %b length %d overlaps an earlier code", symbol, code, length)
			}
			seen[prefix+i] = true
		}
		if havePrevious {
			if length < previousLength {
				t.Fatalf("length[%d] = %d after previous length %d", symbol, length, previousLength)
			}
			if length == previousLength && code <= previousCode {
				t.Fatalf("code[%d] = %b, want greater than previous code %b for equal length", symbol, code, previousCode)
			}
		}
		previousLength = length
		previousCode = code
		havePrevious = true
	}
}

func expandAlphaCodeLengthTokensForTest(tokens []alphaCodeLengthToken, n int) []uint8 {
	out := make([]uint8, 0, n)
	previousNonZero := uint8(8)
	for _, token := range tokens {
		switch token.symbol {
		case alphaCodeLengthRepeatPrevious:
			run := int(token.extra) + 3
			for i := 0; i < run; i++ {
				out = append(out, previousNonZero)
			}
		case alphaCodeLengthRepeatZero:
			run := int(token.extra) + 3
			for i := 0; i < run; i++ {
				out = append(out, 0)
			}
		case alphaCodeLengthRepeatZeroBig:
			run := int(token.extra) + 11
			for i := 0; i < run; i++ {
				out = append(out, 0)
			}
		default:
			out = append(out, token.symbol)
			if token.symbol != 0 {
				previousNonZero = token.symbol
			}
		}
	}
	if len(out) > n {
		return out[:n]
	}
	for len(out) < n {
		out = append(out, 0)
	}
	return out
}

func assertLossyVP8Frame(t *testing.T, frame []byte, wantWidth int, wantHeight int) {
	t.Helper()
	if len(frame) < 10 {
		t.Fatalf("VP8 frame length = %d, want at least 10", len(frame))
	}
	frameTag := uint32(frame[0]) | uint32(frame[1])<<8 | uint32(frame[2])<<16
	if frameTag&1 != 0 {
		t.Fatal("VP8 frame is not a key frame")
	}
	if frameTag>>4&1 != 1 {
		t.Fatal("VP8 frame show_frame flag is false")
	}
	firstPartitionLen := int(frameTag >> 5)
	if firstPartitionLen <= 0 || 10+firstPartitionLen >= len(frame) {
		t.Fatalf("first partition length = %d, frame length = %d", firstPartitionLen, len(frame))
	}
	if !bytes.Equal(frame[3:6], []byte{0x9d, 0x01, 0x2a}) {
		t.Fatalf("invalid VP8 start code: % x", frame[3:6])
	}
	width := int(binary.LittleEndian.Uint16(frame[6:8]) & 0x3fff)
	height := int(binary.LittleEndian.Uint16(frame[8:10]) & 0x3fff)
	if width != wantWidth || height != wantHeight {
		t.Fatalf("VP8 dimensions = %dx%d, want %dx%d", width, height, wantWidth, wantHeight)
	}
}

func readVP8LoopFilterHeader(t *testing.T, frame []byte) vp8LoopFilter {
	t.Helper()
	firstPart := readVP8FirstPartition(t, frame)
	var r testVP8PartitionReader
	r.init(firstPart)

	colorSpace := r.readUint(128, 1)
	pixelClamp := r.readUint(128, 1)
	readVP8SegmentationHeader(t, &r)
	simple := r.readBit(128)
	level := r.readUint(128, 6)
	sharpness := r.readUint(128, 3)
	deltaEnabled, refDeltas, modeDeltas := readVP8LoopFilterDeltas(t, &r)
	if r.unexpectedEOF {
		t.Fatal("unexpected end of VP8 first partition")
	}
	if colorSpace != 0 {
		t.Fatalf("VP8 color space = %d, want 0", colorSpace)
	}
	if pixelClamp != 0 {
		t.Fatalf("VP8 pixel clamp = %d, want 0", pixelClamp)
	}
	return vp8LoopFilter{
		simple:       simple,
		level:        int(level),
		sharpness:    int(sharpness),
		deltaEnabled: deltaEnabled,
		refDeltas:    refDeltas,
		modeDeltas:   modeDeltas,
	}
}

type testVP8SegmentationHeader struct {
	enabled      bool
	updateMap    bool
	updateData   bool
	absolute     bool
	quantizers   [vp8SegmentCount]int
	filterLevels [vp8SegmentCount]int
	mapProbs     [3]uint8
}

func readVP8SegmentationHeader(t *testing.T, r *testVP8PartitionReader) testVP8SegmentationHeader {
	t.Helper()
	header := testVP8SegmentationHeader{
		enabled:  r.readBit(128),
		mapProbs: [3]uint8{255, 255, 255},
	}
	if !header.enabled {
		return header
	}
	header.updateMap = r.readBit(128)
	header.updateData = r.readBit(128)
	if header.updateData {
		header.absolute = r.readBit(128)
		for i := range header.quantizers {
			header.quantizers[i] = readVP8OptionalSignedLiteral(r, 7)
		}
		for i := range header.filterLevels {
			header.filterLevels[i] = readVP8OptionalSignedLiteral(r, 6)
		}
	}
	if header.updateMap {
		for i := range header.mapProbs {
			if r.readBit(128) {
				header.mapProbs[i] = uint8(r.readUint(128, 8))
			}
		}
	}
	if r.unexpectedEOF {
		t.Fatal("unexpected end while reading segmentation header")
	}
	return header
}

func readVP8OptionalSignedLiteral(r *testVP8PartitionReader, bits uint8) int {
	if !r.readBit(128) {
		return 0
	}
	value := int(r.readUint(128, bits))
	if r.readBit(128) {
		return -value
	}
	return value
}

func readVP8QuantDeltas(r *testVP8PartitionReader) vp8QuantDeltas {
	return vp8QuantDeltas{
		y1DC: readVP8OptionalSignedLiteral(r, 4),
		y2DC: readVP8OptionalSignedLiteral(r, 4),
		y2AC: readVP8OptionalSignedLiteral(r, 4),
		uvDC: readVP8OptionalSignedLiteral(r, 4),
		uvAC: readVP8OptionalSignedLiteral(r, 4),
	}
}

func readVP8SegmentID(r *testVP8PartitionReader, probs [3]uint8) uint8 {
	if !r.readBit(probs[0]) {
		if r.readBit(probs[1]) {
			return 1
		}
		return 0
	}
	if r.readBit(probs[2]) {
		return 3
	}
	return 2
}

func readVP8LoopFilterDeltas(t *testing.T, r *testVP8PartitionReader) (bool, [4]int, [4]int) {
	t.Helper()
	var refDeltas [4]int
	var modeDeltas [4]int
	if !r.readBit(128) {
		return false, refDeltas, modeDeltas
	}
	if !r.readBit(128) {
		return true, refDeltas, modeDeltas
	}
	for i := range refDeltas {
		refDeltas[i] = readVP8LoopFilterDelta(r)
	}
	for i := range modeDeltas {
		modeDeltas[i] = readVP8LoopFilterDelta(r)
	}
	if r.unexpectedEOF {
		t.Fatal("unexpected end while reading loop filter deltas")
	}
	return true, refDeltas, modeDeltas
}

func readVP8LoopFilterDelta(r *testVP8PartitionReader) int {
	if !r.readBit(128) {
		return 0
	}
	delta := int(r.readUint(128, 6))
	if r.readBit(128) {
		return -delta
	}
	return delta
}

func readVP8FirstPartition(t *testing.T, frame []byte) []byte {
	t.Helper()
	if len(frame) < 10 {
		t.Fatalf("VP8 frame length = %d, want at least 10", len(frame))
	}
	frameTag := uint32(frame[0]) | uint32(frame[1])<<8 | uint32(frame[2])<<16
	firstPartitionLen := int(frameTag >> 5)
	if firstPartitionLen <= 0 || 10+firstPartitionLen >= len(frame) {
		t.Fatalf("first partition length = %d, frame length = %d", firstPartitionLen, len(frame))
	}
	return frame[10 : 10+firstPartitionLen]
}

func readVP8FirstPartitionY4Modes(t *testing.T, firstPart []byte) [16]uint8 {
	t.Helper()
	var r testVP8PartitionReader
	r.init(firstPart)

	readVP8FirstPartitionHeaderBeforeTokenProbs(t, &r)
	readVP8FirstPartitionTokenProbs(t, &r)
	r.readBit(128) // macroblock skip probability
	if r.unexpectedEOF {
		t.Fatal("unexpected end before Y4 modes")
	}
	if useY16 := r.readBit(145); useY16 {
		t.Fatal("macroblock uses Y16 mode, want Y4")
	}

	var modes [16]uint8
	var up [4]uint8
	for by := 0; by < 4; by++ {
		p := uint8(0)
		for bx := 0; bx < 4; bx++ {
			mode := readVP8Y4Mode(&r, vp8PredProb[up[bx]][p])
			modes[by*4+bx] = mode
			p = mode
			up[bx] = mode
		}
	}
	if r.unexpectedEOF {
		t.Fatal("unexpected end while reading Y4 modes")
	}
	return modes
}

func readVP8FirstPartitionHeaderBeforeTokenProbs(t *testing.T, r *testVP8PartitionReader) {
	t.Helper()
	r.readUint(128, 1) // color space
	r.readUint(128, 1) // pixel clamp
	readVP8SegmentationHeader(t, r)
	r.readBit(128)     // loop filter type
	r.readUint(128, 6) // loop filter level
	r.readUint(128, 3) // sharpness
	readVP8LoopFilterDeltas(t, r)
	r.readUint(128, 2) // token partitions
	r.readUint(128, 7) // base quantizer
	readVP8QuantDeltas(r)
	r.readBit(128) // refresh last frame buffer
	if r.unexpectedEOF {
		t.Fatal("unexpected end before token probability updates")
	}
}

func readVP8FirstPartitionTokenProbs(t *testing.T, r *testVP8PartitionReader) vp8TokenProbs {
	t.Helper()
	probs := vp8DefaultTokenProbs
	for plane := range vp8TokenProbUpdateProb {
		for band := range vp8TokenProbUpdateProb[plane] {
			for context := range vp8TokenProbUpdateProb[plane][band] {
				for node, updateProb := range vp8TokenProbUpdateProb[plane][band][context] {
					if r.readBit(updateProb) {
						probs[plane][band][context][node] = uint8(r.readUint(128, 8))
					}
				}
			}
		}
	}
	if r.unexpectedEOF {
		t.Fatal("unexpected end while reading token probability updates")
	}
	return probs
}

func readVP8Y4Mode(r *testVP8PartitionReader, prob [9]uint8) uint8 {
	if !r.readBit(prob[0]) {
		return vp8PredDC
	}
	if !r.readBit(prob[1]) {
		return vp8PredTM
	}
	if !r.readBit(prob[2]) {
		return vp8PredVE
	}
	if !r.readBit(prob[3]) {
		if !r.readBit(prob[4]) {
			return vp8PredHE
		}
		if !r.readBit(prob[5]) {
			return vp8PredRD
		}
		return vp8PredVR
	}
	if !r.readBit(prob[6]) {
		return vp8PredLD
	}
	if !r.readBit(prob[7]) {
		return vp8PredVL
	}
	if !r.readBit(prob[8]) {
		return vp8PredHD
	}
	return vp8PredHU
}

func TestEncodeRejectsInvalidInput(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, nil, nil); err == nil {
		t.Fatal("Encode with nil image succeeded")
	}
	if err := Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 0, 1)), nil); err == nil {
		t.Fatal("Encode with empty image succeeded")
	}
	if err := Encode(nil, image.NewNRGBA(image.Rect(0, 0, 1, 1)), nil); err == nil {
		t.Fatal("Encode with nil writer succeeded")
	}
	if err := Encode(&buf, image.NewNRGBA(image.Rect(0, 0, maxVP8Dimension+1, 1)), &Options{Compression: CompressionLossy}); err == nil {
		t.Fatal("Encode lossy with too-wide image succeeded")
	}
	if err := Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 1, 1)), &Options{Compression: Compression(99)}); err == nil {
		t.Fatal("Encode with unsupported compression succeeded")
	}
}

func TestEncodePropagatesWriterError(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	err := Encode(failingWriter{}, img, nil)
	if !errors.Is(err, errFailingWriter) {
		t.Fatalf("Encode error = %v, want %v", err, errFailingWriter)
	}
}

var errFailingWriter = errors.New("writer failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errFailingWriter
}

type testWebPChunk struct {
	name    string
	payload []byte
}

func readWebPChunks(t *testing.T, data []byte) []testWebPChunk {
	t.Helper()
	if len(data) < 12 {
		t.Fatalf("WebP length = %d, want at least 12", len(data))
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		t.Fatalf("unexpected WebP header: %q %q", data[0:4], data[8:12])
	}
	riffSize := int(binary.LittleEndian.Uint32(data[4:8]))
	if riffSize+8 != len(data) {
		t.Fatalf("RIFF size = %d, file length = %d", riffSize, len(data))
	}

	var chunks []testWebPChunk
	for offset := 12; offset < len(data); {
		if offset+8 > len(data) {
			t.Fatalf("short chunk header at offset %d", offset)
		}
		payloadSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		payloadStart := offset + 8
		payloadEnd := payloadStart + payloadSize
		if payloadEnd > len(data) {
			t.Fatalf("chunk %q payload size = %d exceeds file length %d", data[offset:offset+4], payloadSize, len(data))
		}
		chunks = append(chunks, testWebPChunk{
			name:    string(data[offset : offset+4]),
			payload: data[payloadStart:payloadEnd],
		})
		offset = payloadEnd
		if payloadSize&1 != 0 {
			if offset >= len(data) {
				t.Fatalf("missing padding byte after chunk %q", chunks[len(chunks)-1].name)
			}
			if data[offset] != 0 {
				t.Fatalf("padding byte after chunk %q = %#02x, want 0", chunks[len(chunks)-1].name, data[offset])
			}
			offset++
		}
	}
	return chunks
}

func readUint24LE(b []byte) int {
	return int(b[0]) | int(b[1])<<8 | int(b[2])<<16
}

func hasVP8LFirstTransform(t *testing.T, data []byte, wantType uint32) bool {
	t.Helper()
	if len(data) < 21 {
		t.Fatalf("WebP length = %d, want at least 21", len(data))
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" || string(data[12:16]) != "VP8L" {
		t.Fatalf("unexpected WebP header: %q %q %q", data[0:4], data[8:12], data[12:16])
	}
	payloadSize := int(binary.LittleEndian.Uint32(data[16:20]))
	if 20+payloadSize > len(data) {
		t.Fatalf("VP8L payload size = %d exceeds file length %d", payloadSize, len(data))
	}
	r := testBitReader{data: data[20 : 20+payloadSize]}
	if signature, err := r.read(8); err != nil || signature != 0x2f {
		t.Fatalf("invalid VP8L signature: signature=%#x err=%v", signature, err)
	}
	r.read(14)
	r.read(14)
	r.read(1)
	if version, err := r.read(3); err != nil || version != 0 {
		t.Fatalf("invalid VP8L version: version=%d err=%v", version, err)
	}
	transformPresent, err := r.read(1)
	if err != nil {
		t.Fatalf("reading transform presence failed: %v", err)
	}
	if transformPresent == 0 {
		return false
	}
	transformType, err := r.read(2)
	if err != nil {
		t.Fatalf("reading transform type failed: %v", err)
	}
	return transformType == wantType
}

type decodedTree struct {
	constant bool
	symbol   int
	lengths  []uint8
	codes    []uint16
}

type testPredictorTransform struct {
	sizeBits uint8
	width    int
	pixels   []color.NRGBA
}

type testColorTransform struct {
	sizeBits uint8
	width    int
	pixels   []color.NRGBA
}

type testColorIndexTransform struct {
	width     int
	widthBits uint8
	table     []color.NRGBA
}

type testVP8LTransformType uint8

const (
	testVP8LTransformPredictor     testVP8LTransformType = 0
	testVP8LTransformColor         testVP8LTransformType = 1
	testVP8LTransformSubtractGreen testVP8LTransformType = 2
	testVP8LTransformColorIndexing testVP8LTransformType = 3
)

func decodeEncoderOutput(data []byte) ([]color.NRGBA, int, int, bool, error) {
	if len(data) < 20 {
		return nil, 0, 0, false, errors.New("short WebP data")
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" || string(data[12:16]) != "VP8L" {
		return nil, 0, 0, false, errors.New("invalid WebP header")
	}
	riffSize := int(binary.LittleEndian.Uint32(data[4:8]))
	if riffSize+8 != len(data) {
		return nil, 0, 0, false, errors.New("invalid RIFF size")
	}
	payloadSize := int(binary.LittleEndian.Uint32(data[16:20]))
	if payloadSize < 0 || 20+payloadSize > len(data) {
		return nil, 0, 0, false, errors.New("invalid VP8L size")
	}
	if payloadSize%2 == 1 && data[20+payloadSize] != 0 {
		return nil, 0, 0, false, errors.New("invalid VP8L padding")
	}

	r := testBitReader{data: data[20 : 20+payloadSize]}
	signature, err := r.read(8)
	if err != nil {
		return nil, 0, 0, false, err
	}
	if signature != 0x2f {
		return nil, 0, 0, false, errors.New("invalid VP8L signature")
	}
	widthMinusOne, err := r.read(14)
	if err != nil {
		return nil, 0, 0, false, err
	}
	heightMinusOne, err := r.read(14)
	if err != nil {
		return nil, 0, 0, false, err
	}
	alphaHint, err := r.read(1)
	if err != nil {
		return nil, 0, 0, false, err
	}
	version, err := r.read(3)
	if err != nil {
		return nil, 0, 0, false, err
	}
	if version != 0 {
		return nil, 0, 0, false, errors.New("invalid VP8L version")
	}

	width, height := int(widthMinusOne+1), int(heightMinusOne+1)
	currentWidth := width
	var predictor *testPredictorTransform
	var colorTransform *testColorTransform
	var colorIndex *testColorIndexTransform
	var subtractGreen bool
	var transforms []testVP8LTransformType
	for {
		transformPresent, err := r.read(1)
		if err != nil {
			return nil, 0, 0, false, err
		}
		if transformPresent == 0 {
			break
		}
		transformType, err := r.read(2)
		if err != nil {
			return nil, 0, 0, false, err
		}
		switch testVP8LTransformType(transformType) {
		case testVP8LTransformPredictor:
			if predictor != nil {
				return nil, 0, 0, false, errors.New("duplicate predictor transform")
			}
			sizeBitsMinusTwo, err := r.read(3)
			if err != nil {
				return nil, 0, 0, false, err
			}
			sizeBits := uint8(sizeBitsMinusTwo + 2)
			transformWidth, transformHeight := vp8lTransformDimensions(currentWidth, height, sizeBits)
			transformPixels, err := decodeEncoderImageData(&r, transformWidth, transformHeight, false)
			if err != nil {
				return nil, 0, 0, false, err
			}
			predictor = &testPredictorTransform{
				sizeBits: sizeBits,
				width:    transformWidth,
				pixels:   transformPixels,
			}
			transforms = append(transforms, testVP8LTransformPredictor)
		case testVP8LTransformColor:
			if colorTransform != nil {
				return nil, 0, 0, false, errors.New("duplicate color transform")
			}
			sizeBitsMinusTwo, err := r.read(3)
			if err != nil {
				return nil, 0, 0, false, err
			}
			sizeBits := uint8(sizeBitsMinusTwo + 2)
			transformWidth, transformHeight := vp8lTransformDimensions(currentWidth, height, sizeBits)
			transformPixels, err := decodeEncoderImageData(&r, transformWidth, transformHeight, false)
			if err != nil {
				return nil, 0, 0, false, err
			}
			colorTransform = &testColorTransform{
				sizeBits: sizeBits,
				width:    transformWidth,
				pixels:   transformPixels,
			}
			transforms = append(transforms, testVP8LTransformColor)
		case testVP8LTransformSubtractGreen:
			if subtractGreen {
				return nil, 0, 0, false, errors.New("duplicate subtract green transform")
			}
			subtractGreen = true
			transforms = append(transforms, testVP8LTransformSubtractGreen)
		case testVP8LTransformColorIndexing:
			if colorIndex != nil {
				return nil, 0, 0, false, errors.New("duplicate color indexing transform")
			}
			colorTableSizeMinusOne, err := r.read(8)
			if err != nil {
				return nil, 0, 0, false, err
			}
			colorTableSize := int(colorTableSizeMinusOne) + 1
			tableDeltas, err := decodeEncoderImageData(&r, colorTableSize, 1, false)
			if err != nil {
				return nil, 0, 0, false, err
			}
			table := make([]color.NRGBA, colorTableSize)
			for i, delta := range tableDeltas {
				if i == 0 {
					table[i] = delta
				} else {
					table[i] = addNRGBA(table[i-1], delta)
				}
			}
			widthBits := vp8lColorIndexWidthBits(colorTableSize)
			colorIndex = &testColorIndexTransform{
				width:     currentWidth,
				widthBits: widthBits,
				table:     table,
			}
			currentWidth = vp8lDivRoundUp(currentWidth, 1<<widthBits)
			transforms = append(transforms, testVP8LTransformColorIndexing)
		default:
			return nil, 0, 0, false, errors.New("unexpected transform")
		}
	}

	pixels, err := decodeEncoderImageData(&r, currentWidth, height, true)
	if err != nil {
		return nil, 0, 0, false, err
	}
	imageWidth := currentWidth
	for i := len(transforms) - 1; i >= 0; i-- {
		switch transforms[i] {
		case testVP8LTransformPredictor:
			pixels = applyTestPredictorTransform(pixels, imageWidth, height, *predictor)
		case testVP8LTransformColor:
			applyTestColorTransform(pixels, imageWidth, height, *colorTransform)
		case testVP8LTransformSubtractGreen:
			applyTestSubtractGreenTransform(pixels)
		case testVP8LTransformColorIndexing:
			pixels = applyTestColorIndexTransform(pixels, imageWidth, height, *colorIndex)
			imageWidth = colorIndex.width
		}
	}

	return pixels, imageWidth, height, alphaHint != 0, nil
}

type decodedPrefixGroup struct {
	green    decodedTree
	red      decodedTree
	blue     decodedTree
	alpha    decodedTree
	distance decodedTree
}

func decodeEncoderImageData(r *testBitReader, width int, height int, metaPrefix bool) ([]color.NRGBA, error) {
	colorCacheBits := uint8(0)
	colorCacheSize := 0
	if v, err := r.read(1); err != nil {
		return nil, err
	} else if v != 0 {
		bits, err := r.read(4)
		if err != nil {
			return nil, err
		}
		if bits < 1 || bits > 11 {
			return nil, errors.New("invalid color cache bits")
		}
		colorCacheBits = uint8(bits)
		colorCacheSize = 1 << colorCacheBits
	}
	prefixBits := uint8(0)
	prefixImageWidth := 0
	var entropyImage []color.NRGBA
	groupCount := 1
	if metaPrefix {
		v, err := r.read(1)
		if err != nil {
			return nil, err
		}
		if v != 0 {
			rawPrefixBits, err := r.read(3)
			if err != nil {
				return nil, err
			}
			prefixBits = uint8(rawPrefixBits) + 2
			var prefixImageHeight int
			prefixImageWidth, prefixImageHeight = testVP8LMetaPrefixImageDimensions(width, height, prefixBits)
			entropyImage, err = decodeEncoderImageData(r, prefixImageWidth, prefixImageHeight, false)
			if err != nil {
				return nil, err
			}
			maxCode := 0
			for _, pixel := range entropyImage {
				if code := testVP8LMetaPrefixCode(pixel); code > maxCode {
					maxCode = code
				}
			}
			groupCount = maxCode + 1
		}
	}

	groups := make([]decodedPrefixGroup, groupCount)
	for i := range groups {
		green, err := decodeEncoderTree(r, nLiteralCodes+nLengthCodes+colorCacheSize)
		if err != nil {
			return nil, err
		}
		red, err := decodeEncoderTree(r, nLiteralCodes)
		if err != nil {
			return nil, err
		}
		blue, err := decodeEncoderTree(r, nLiteralCodes)
		if err != nil {
			return nil, err
		}
		alpha, err := decodeEncoderTree(r, nLiteralCodes)
		if err != nil {
			return nil, err
		}
		distance, err := decodeEncoderTree(r, nDistanceCodes)
		if err != nil {
			return nil, err
		}
		groups[i] = decodedPrefixGroup{
			green:    green,
			red:      red,
			blue:     blue,
			alpha:    alpha,
			distance: distance,
		}
	}

	pixels := make([]color.NRGBA, width*height)
	colorCache := make([]color.NRGBA, colorCacheSize)
	for i := 0; i < len(pixels); {
		group := groups[0]
		groupIndex := 0
		if entropyImage != nil {
			x := i % width
			y := i / width
			code := testVP8LMetaPrefixCode(entropyImage[testVP8LMetaPrefixIndex(x, y, prefixBits, prefixImageWidth)])
			if code < 0 || code >= len(groups) {
				return nil, errors.New("meta prefix code out of range")
			}
			groupIndex = code
			group = groups[code]
		}
		greenSymbol, err := decodeEncoderSymbolInt(r, group.green)
		if err != nil {
			return nil, fmt.Errorf("green symbol at pixel %d group %d: %w", i, groupIndex, err)
		}
		if greenSymbol >= nLiteralCodes+nLengthCodes {
			index := greenSymbol - nLiteralCodes - nLengthCodes
			if index < 0 || index >= len(colorCache) {
				return nil, fmt.Errorf("color cache index %d at pixel %d group %d out of range", index, i, groupIndex)
			}
			pixel := colorCache[index]
			pixels[i] = pixel
			updateTestVP8LColorCache(colorCache, colorCacheBits, pixel)
			i++
			continue
		}
		if greenSymbol >= nLiteralCodes && greenSymbol < nLiteralCodes+nLengthCodes {
			length, err := decodeVP8LPrefixValue(r, greenSymbol-nLiteralCodes)
			if err != nil {
				return nil, fmt.Errorf("length prefix %d at pixel %d group %d: %w", greenSymbol-nLiteralCodes, i, groupIndex, err)
			}
			distancePrefix, err := decodeEncoderSymbolInt(r, group.distance)
			if err != nil {
				return nil, fmt.Errorf("distance symbol at pixel %d group %d length %d: %w", i, groupIndex, length, err)
			}
			distanceCode, err := decodeVP8LPrefixValue(r, distancePrefix)
			if err != nil {
				return nil, fmt.Errorf("distance prefix %d at pixel %d group %d length %d: %w", distancePrefix, i, groupIndex, length, err)
			}
			distancePixels, err := testVP8LDistanceCodeToDistance(distanceCode, width)
			if err != nil {
				return nil, fmt.Errorf("distance code %d at pixel %d group %d length %d: %w", distanceCode, i, groupIndex, length, err)
			}
			if distancePixels > i {
				return nil, fmt.Errorf("backward reference before image start at pixel %d group %d length %d distancePrefix %d distanceCode %d distancePixels %d", i, groupIndex, length, distancePrefix, distanceCode, distancePixels)
			}
			if i+length > len(pixels) {
				return nil, fmt.Errorf("backward reference exceeds image at pixel %d group %d length %d distancePrefix %d distanceCode %d distancePixels %d total %d", i, groupIndex, length, distancePrefix, distanceCode, distancePixels, len(pixels))
			}
			for copied := 0; copied < length; copied++ {
				pixel := pixels[i-distancePixels]
				pixels[i] = pixel
				updateTestVP8LColorCache(colorCache, colorCacheBits, pixel)
				i++
			}
			continue
		}
		rr, err := decodeEncoderSymbol(r, group.red)
		if err != nil {
			return nil, fmt.Errorf("red symbol at pixel %d group %d green %d: %w", i, groupIndex, greenSymbol, err)
		}
		b, err := decodeEncoderSymbol(r, group.blue)
		if err != nil {
			return nil, fmt.Errorf("blue symbol at pixel %d group %d green %d: %w", i, groupIndex, greenSymbol, err)
		}
		a, err := decodeEncoderSymbol(r, group.alpha)
		if err != nil {
			return nil, fmt.Errorf("alpha symbol at pixel %d group %d green %d: %w", i, groupIndex, greenSymbol, err)
		}
		pixel := color.NRGBA{R: rr, G: uint8(greenSymbol), B: b, A: a}
		pixels[i] = pixel
		updateTestVP8LColorCache(colorCache, colorCacheBits, pixel)
		i++
	}

	return pixels, nil
}

func updateTestVP8LColorCache(cache []color.NRGBA, bits uint8, pixel color.NRGBA) {
	if len(cache) == 0 {
		return
	}
	cache[testVP8LColorCacheIndex(pixel, bits)] = pixel
}

func applyTestPredictorTransform(residual []color.NRGBA, width int, height int, transform testPredictorTransform) []color.NRGBA {
	pixels := make([]color.NRGBA, len(residual))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := y*width + x
			mode := transform.pixels[(y>>transform.sizeBits)*transform.width+(x>>transform.sizeBits)].G
			pred := testPredictorPixel(pixels, width, x, y, mode)
			pixels[i] = addNRGBA(residual[i], pred)
		}
	}
	return pixels
}

func applyTestSubtractGreenTransform(pixels []color.NRGBA) {
	for i := range pixels {
		pixels[i].R += pixels[i].G
		pixels[i].B += pixels[i].G
	}
}

func applyTestColorTransform(pixels []color.NRGBA, width int, height int, transform testColorTransform) {
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := y*width + x
			c := transform.pixels[(y>>transform.sizeBits)*transform.width+(x>>transform.sizeBits)]
			element := vp8lColorTransformElement{
				greenToRed:  c.B,
				greenToBlue: c.G,
				redToBlue:   c.R,
			}
			pixels[i] = inverseTestVP8LColorTransform(pixels[i], element)
		}
	}
}

func testVP8LMetaPrefixImageDimensions(width int, height int, prefixBits uint8) (int, int) {
	blockSize := 1 << prefixBits
	return vp8lDivRoundUp(width, blockSize), vp8lDivRoundUp(height, blockSize)
}

func testVP8LMetaPrefixIndex(x int, y int, prefixBits uint8, prefixImageWidth int) int {
	return (y>>prefixBits)*prefixImageWidth + (x >> prefixBits)
}

func testVP8LMetaPrefixCode(pixel color.NRGBA) int {
	return int(pixel.R)<<8 | int(pixel.G)
}

func testVP8LColorCacheIndex(pixel color.NRGBA, bits uint8) int {
	packed := uint32(pixel.A)<<24 | uint32(pixel.R)<<16 | uint32(pixel.G)<<8 | uint32(pixel.B)
	return int((0x1e35a7bd * packed) >> (32 - bits))
}

func inverseTestVP8LColorTransform(pixel color.NRGBA, element vp8lColorTransformElement) color.NRGBA {
	red := pixel.R + vp8lColorDelta(element.greenToRed, pixel.G)
	blue := pixel.B + vp8lColorDelta(element.greenToBlue, pixel.G) + vp8lColorDelta(element.redToBlue, red)
	return color.NRGBA{R: red, G: pixel.G, B: blue, A: pixel.A}
}

func applyTestColorIndexTransform(indexed []color.NRGBA, indexedWidth int, height int, transform testColorIndexTransform) []color.NRGBA {
	pixels := make([]color.NRGBA, transform.width*height)
	groupSize := 1 << transform.widthBits
	bitsPerIndex := 8 / groupSize
	mask := uint8((1 << bitsPerIndex) - 1)
	for y := 0; y < height; y++ {
		for x := 0; x < transform.width; x++ {
			packed := indexed[y*indexedWidth+(x>>transform.widthBits)].G
			index := (packed >> uint((x&(groupSize-1))*bitsPerIndex)) & mask
			if int(index) < len(transform.table) {
				pixels[y*transform.width+x] = transform.table[index]
			}
		}
	}
	return pixels
}

func testPredictorPixel(pixels []color.NRGBA, width int, x int, y int, mode uint8) color.NRGBA {
	if x == 0 && y == 0 {
		return color.NRGBA{A: 255}
	}
	if y == 0 {
		return pixels[y*width+x-1]
	}
	if x == 0 {
		return pixels[(y-1)*width+x]
	}

	left := pixels[y*width+x-1]
	top := pixels[(y-1)*width+x]
	topLeft := pixels[(y-1)*width+x-1]
	topRightX := x + 1
	topRightY := y - 1
	if x == width-1 {
		topRightX = 0
		topRightY = y
	}
	topRight := pixels[topRightY*width+topRightX]
	return vp8lPredictorFromNeighbors(mode, left, top, topRight, topLeft)
}

func addNRGBA(a color.NRGBA, b color.NRGBA) color.NRGBA {
	return color.NRGBA{
		R: a.R + b.R,
		G: a.G + b.G,
		B: a.B + b.B,
		A: a.A + b.A,
	}
}

func decodeEncoderTree(r *testBitReader, alphabetSize int) (decodedTree, error) {
	useSimple, err := r.read(1)
	if err != nil {
		return decodedTree{}, err
	}
	if useSimple != 0 {
		nSymbols, err := r.read(1)
		if err != nil {
			return decodedTree{}, err
		}
		use8Bits, err := r.read(1)
		if err != nil {
			return decodedTree{}, err
		}
		nBits := uint8(1)
		if use8Bits != 0 {
			nBits = 8
		}
		symbol, err := r.read(nBits)
		if err != nil {
			return decodedTree{}, err
		}
		if int(symbol) >= alphabetSize {
			return decodedTree{}, errors.New("simple tree symbol out of range")
		}
		if nSymbols == 0 {
			return decodedTree{constant: true, symbol: int(symbol)}, nil
		}
		symbol1, err := r.read(8)
		if err != nil {
			return decodedTree{}, err
		}
		if int(symbol1) >= alphabetSize {
			return decodedTree{}, errors.New("simple tree symbol out of range")
		}
		lengths := make([]uint8, alphabetSize)
		lengths[symbol] = 1
		lengths[symbol1] = 1
		return decodedTree{lengths: lengths, codes: testCanonicalCodes(lengths)}, nil
	}

	nCodesMinusFour, err := r.read(4)
	if err != nil {
		return decodedTree{}, err
	}
	nCodes := int(nCodesMinusFour) + 4
	if nCodes > len(normalCodeLengthCodeOrder) {
		return decodedTree{}, errors.New("unexpected code length code count")
	}
	codeLengthCodeLengths := make([]uint8, alphaCodeLengthCodeCount)
	for i := 0; i < nCodes; i++ {
		got, err := r.read(3)
		if err != nil {
			return decodedTree{}, err
		}
		codeLengthCodeLengths[normalCodeLengthCodeOrder[i]] = uint8(got)
	}
	useLength, err := r.read(1)
	if err != nil {
		return decodedTree{}, err
	}
	maxSymbol := alphabetSize
	if useLength != 0 {
		lengthNBitsSelector, err := r.read(3)
		if err != nil {
			return decodedTree{}, err
		}
		lengthNBits := uint8(2 + 2*lengthNBitsSelector)
		value, err := r.read(lengthNBits)
		if err != nil {
			return decodedTree{}, err
		}
		maxSymbol = int(value) + 2
		if maxSymbol > alphabetSize {
			return decodedTree{}, errors.New("max symbol limit out of range")
		}
	}

	codeLengthTree := decodedTree{
		lengths: codeLengthCodeLengths,
		codes:   testCanonicalCodes(codeLengthCodeLengths),
	}
	lengths := make([]uint8, alphabetSize)
	previousNonZero := uint8(8)
	for symbol, remainingTokens := 0, maxSymbol; symbol < alphabetSize && remainingTokens > 0; remainingTokens-- {
		codeLengthSymbol, err := decodeEncoderSymbolInt(r, codeLengthTree)
		if err != nil {
			return decodedTree{}, err
		}
		switch {
		case codeLengthSymbol >= 0 && codeLengthSymbol <= 15:
			lengths[symbol] = uint8(codeLengthSymbol)
			if codeLengthSymbol != 0 {
				previousNonZero = uint8(codeLengthSymbol)
			}
			symbol++
		case codeLengthSymbol == 16:
			repeatExtra, err := r.read(2)
			if err != nil {
				return decodedTree{}, err
			}
			repeat := int(repeatExtra) + 3
			for range repeat {
				if symbol >= alphabetSize {
					return decodedTree{}, errors.New("code length repeat exceeds max symbol")
				}
				lengths[symbol] = previousNonZero
				symbol++
			}
		case codeLengthSymbol == alphaCodeLengthRepeatZero:
			repeatExtra, err := r.read(3)
			if err != nil {
				return decodedTree{}, err
			}
			symbol += int(repeatExtra) + 3
			if symbol > alphabetSize {
				return decodedTree{}, errors.New("zero repeat exceeds max symbol")
			}
		case codeLengthSymbol == alphaCodeLengthRepeatZeroBig:
			repeatExtra, err := r.read(7)
			if err != nil {
				return decodedTree{}, err
			}
			symbol += int(repeatExtra) + 11
			if symbol > alphabetSize {
				return decodedTree{}, errors.New("zero repeat exceeds max symbol")
			}
		default:
			return decodedTree{}, errors.New("unexpected code length symbol")
		}
	}
	return decodedTree{lengths: lengths, codes: testCanonicalCodes(lengths)}, nil
}

func decodeEncoderSymbol(r *testBitReader, tree decodedTree) (uint8, error) {
	symbol, err := decodeEncoderSymbolInt(r, tree)
	if err != nil {
		return 0, err
	}
	if symbol > 255 {
		return 0, errors.New("symbol out of uint8 range")
	}
	return uint8(symbol), nil
}

func decodeEncoderSymbolInt(r *testBitReader, tree decodedTree) (int, error) {
	if tree.constant {
		return tree.symbol, nil
	}
	var code uint32
	for length := uint8(1); length <= 15; length++ {
		bit, err := r.read(1)
		if err != nil {
			return 0, err
		}
		code |= bit << (length - 1)
		for symbol, symbolLength := range tree.lengths {
			if symbolLength != length {
				continue
			}
			if code == uint32(reverseBits(tree.codes[symbol], length)) {
				return symbol, nil
			}
		}
	}
	return 0, errors.New("invalid Huffman symbol")
}

func testCanonicalCodes(lengths []uint8) []uint16 {
	var histogram [16]uint16
	for _, length := range lengths {
		if length != 0 {
			histogram[length]++
		}
	}

	code := uint16(0)
	var nextCodes [16]uint16
	for length := 1; length < len(nextCodes); length++ {
		code = (code + histogram[length-1]) << 1
		nextCodes[length] = code
	}

	codes := make([]uint16, len(lengths))
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		codes[symbol] = nextCodes[length]
		nextCodes[length]++
	}
	return codes
}

func decodeVP8LPrefixValue(r *testBitReader, prefixCode int) (int, error) {
	if prefixCode < 0 || prefixCode >= nDistanceCodes {
		return 0, errors.New("prefix code out of range")
	}
	if prefixCode < 4 {
		return prefixCode + 1, nil
	}
	extraBits := uint8((prefixCode - 2) >> 1)
	extra, err := r.read(extraBits)
	if err != nil {
		return 0, err
	}
	offset := (2 + prefixCode&1) << extraBits
	return offset + int(extra) + 1, nil
}

func testVP8LDistanceCodeToDistance(distanceCode int, width int) (int, error) {
	if distanceCode > 120 {
		return distanceCode - 120, nil
	}
	if distanceCode < 1 || distanceCode > len(vp8lDistanceMap) {
		return 0, errors.New("unsupported VP8L distance code")
	}
	offset := vp8lDistanceMap[distanceCode-1]
	distance := offset.x + offset.y*width
	if distance < 1 {
		distance = 1
	}
	return distance, nil
}

type testBitReader struct {
	data  []byte
	off   int
	bits  uint64
	nBits uint8
}

func (r *testBitReader) read(n uint8) (uint32, error) {
	for r.nBits < n {
		if r.off >= len(r.data) {
			return 0, errors.New("unexpected end of VP8L data")
		}
		r.bits |= uint64(r.data[r.off]) << r.nBits
		r.nBits += 8
		r.off++
	}
	v := uint32(r.bits & uint64(1<<n-1))
	r.bits >>= n
	r.nBits -= n
	return v, nil
}

type testVP8PartitionReader struct {
	buf           []byte
	r             int
	rangeM1       uint32
	bits          uint32
	nBits         uint8
	unexpectedEOF bool
}

func (p *testVP8PartitionReader) init(buf []byte) {
	p.buf = buf
	p.r = 0
	p.rangeM1 = 254
	p.bits = 0
	p.nBits = 0
	p.unexpectedEOF = false
}

func (p *testVP8PartitionReader) readBit(prob uint8) bool {
	if p.nBits < 8 {
		if p.r >= len(p.buf) {
			p.unexpectedEOF = true
			return false
		}
		p.bits |= uint32(p.buf[p.r]) << (8 - p.nBits)
		p.r++
		p.nBits += 8
	}

	split := (p.rangeM1*uint32(prob))>>8 + 1
	bit := p.bits >= split<<8
	if bit {
		p.rangeM1 -= split
		p.bits -= split << 8
	} else {
		p.rangeM1 = split - 1
	}
	for p.rangeM1 < 127 {
		p.rangeM1 = p.rangeM1<<1 | 1
		p.bits <<= 1
		p.nBits--
	}
	return bit
}

func (p *testVP8PartitionReader) readUint(prob uint8, n uint8) uint32 {
	var u uint32
	for n > 0 {
		n--
		if p.readBit(prob) {
			u |= 1 << n
		}
	}
	return u
}
