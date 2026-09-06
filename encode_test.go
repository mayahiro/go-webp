package webp

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
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
	if got := vp8lAutoLosslessMode(img, readPixel, bounds, bounds.Dx(), bounds.Dy()); got != gotMode {
		t.Fatalf("mode-only selection = %d, profile selection = %d", got, gotMode)
	}
}

func TestVP8LAutoLosslessModeSkipsDiagnosticReads(t *testing.T) {
	for _, size := range []image.Point{{X: 32, Y: 32}, {X: 511, Y: 512}} {
		bounds := image.Rect(-3, 5, size.X-3, size.Y+5)
		img := image.NewNRGBA(bounds)
		reads := 0
		readPixel := func(x, y int) color.NRGBA {
			reads++
			return img.NRGBAAt(x, y)
		}
		if got := vp8lAutoLosslessMode(img, readPixel, bounds, size.X, size.Y); got != ModeBalanced {
			t.Fatalf("%v: mode = %d, want ModeBalanced", size, got)
		}
		if reads != 0 {
			t.Errorf("%v: read %d pixels for a mode fixed by image size", size, reads)
		}
		reads = 0
		mode, reason := vp8lAutoLosslessProfile(img, readPixel, bounds, size.X, size.Y)
		if mode != ModeBalanced || reason != vp8lAutoLosslessReasonAlphaHeavy || reads == 0 {
			t.Errorf("%v: diagnostic profile = (%d, %d) with %d reads, want Balanced/AlphaHeavy and sampled pixels", size, mode, reason, reads)
		}
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
	if got := vp8lAutoLosslessMode(img, readPixel, bounds, width, height); got != gotMode {
		t.Fatalf("mode-only selection = %d, profile selection = %d", got, gotMode)
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
	if got := vp8lAutoLosslessMode(img, readPixel, bounds, bounds.Dx(), bounds.Dy()); got != gotMode {
		t.Fatalf("mode-only selection = %d, profile selection = %d", got, gotMode)
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
			if got := vp8lAutoLosslessMode(tc.img, pixelReaderFor(tc.img), bounds, bounds.Dx(), bounds.Dy()); got != gotMode {
				t.Fatalf("mode-only selection = %d, profile selection = %d", got, gotMode)
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
