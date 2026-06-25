package webp

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
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

func TestEncodeModeNearLosslessQuantizesRGBAndPreservesAlpha(t *testing.T) {
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
	step := nearLosslessQuantizationStep(quality)
	for i, c := range src {
		want := color.NRGBA{
			R: quantizeNearLosslessChannel(c.R, step),
			G: quantizeNearLosslessChannel(c.G, step),
			B: quantizeNearLosslessChannel(c.B, step),
			A: c.A,
		}
		if got[i] != want {
			t.Fatalf("pixel %d = %#v, want %#v", i, got[i], want)
		}
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

func TestVP8LEncodingConfigForMode(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	readPixel := pixelReaderFor(img)
	bounds := img.Bounds()

	fast := vp8lEncodingConfigForMode(ModeFast, img, readPixel, bounds, bounds.Dx(), bounds.Dy())
	if fast.tryTransforms || fast.tryLZ77 || fast.tryMetaPrefix || fast.tryColorCache {
		t.Fatalf("ModeFast config enables expensive search: %#v", fast)
	}

	lowMemory := vp8lEncodingConfigForMode(ModeLowMemory, img, readPixel, bounds, bounds.Dx(), bounds.Dy())
	if !lowMemory.tryTransforms {
		t.Fatal("ModeLowMemory disabled transform search")
	}
	if lowMemory.tryLZ77 || lowMemory.tryMetaPrefix || lowMemory.tryColorCache {
		t.Fatalf("ModeLowMemory config enables buffered search: %#v", lowMemory)
	}

	best := vp8lEncodingConfigForMode(ModeBestCompression, img, readPixel, bounds, bounds.Dx(), bounds.Dy())
	if best.allowColorIndexEarlyExit {
		t.Fatal("ModeBestCompression kept color-index early exit enabled")
	}
	if len(best.predictorModes) <= len(vp8lPredictorModeCandidates) {
		t.Fatalf("ModeBestCompression predictor modes = %v, want broader search than %v", best.predictorModes, vp8lPredictorModeCandidates)
	}
	if !best.tryBlockPredictor {
		t.Fatal("ModeBestCompression disabled block predictor search")
	}
	if len(best.colorTransformCandidates) <= len(vp8lColorTransformCandidates) {
		t.Fatalf("ModeBestCompression color transform candidates = %v, want broader search than %v", best.colorTransformCandidates, vp8lColorTransformCandidates)
	}
	if best.maxMetaPrefixLZ77Tokens <= vp8lMaxMetaPrefixLZ77Tokens {
		t.Fatalf("ModeBestCompression max meta prefix LZ77 tokens = %d, want > %d", best.maxMetaPrefixLZ77Tokens, vp8lMaxMetaPrefixLZ77Tokens)
	}
	if best.maxTransformedLZ77CacheTokens <= vp8lMaxTransformedLZ77CacheTokens {
		t.Fatalf("ModeBestCompression max transformed LZ77 color cache tokens = %d, want > %d", best.maxTransformedLZ77CacheTokens, vp8lMaxTransformedLZ77CacheTokens)
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
	analysis := analyzeImage(readPixel, bounds)
	colorIndexPlan, ok := makeVP8LColorIndexingPlanForImage(img, readPixel, bounds, bounds.Dx(), bounds.Dy(), analysis.alpha)
	if !ok {
		t.Fatal("makeVP8LColorIndexingPlanForImage returned false")
	}
	colorIndexBits := vp8lPayloadBits(bounds.Dx(), bounds.Dy(), colorIndexPlan)
	gotMode, gotReason := vp8lAutoLosslessProfile(img, readPixel, bounds, bounds.Dx(), bounds.Dy())
	if gotMode != ModeFast {
		t.Fatalf("vp8lAutoLosslessProfile mode = %d, want ModeFast; colorIndexBits=%d pixels=%d", gotMode, colorIndexBits, bounds.Dx()*bounds.Dy())
	}
	if gotReason != vp8lAutoLosslessReasonLargeLowColor {
		t.Fatalf("vp8lAutoLosslessProfile reason = %d, want large low-color", gotReason)
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
	analysis := analyzeImage(readPixel, bounds)
	colorIndexPlan, ok := makeVP8LColorIndexingPlanForImage(img, readPixel, bounds, width, height, analysis.alpha)
	if !ok {
		t.Fatal("makeVP8LColorIndexingPlanForImage returned false")
	}
	colorIndexBits := vp8lPayloadBits(width, height, colorIndexPlan)
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
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 7, G: 25, B: 51, A: 0})
	img.SetNRGBA(1, 0, color.NRGBA{R: 63, G: 127, B: 191, A: 128})

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
}

func TestModeBestCompressionKeepsColorIndexCandidate(t *testing.T) {
	img := newBenchmarkLimitedPalettedFixtureImage(64, 64)
	readPixel := pixelReaderFor(img)
	bounds := img.Bounds()
	balanced := chooseVP8LEncodingPlanForImageMode(img, readPixel, bounds, bounds.Dx(), bounds.Dy(), ModeBalanced)
	best := chooseVP8LEncodingPlanForImageMode(img, readPixel, bounds, bounds.Dx(), bounds.Dy(), ModeBestCompression)
	if !best.colorIndexing {
		t.Fatal("ModeBestCompression did not keep the color indexing candidate")
	}
	if got, wantMax := vp8lPayloadBits(bounds.Dx(), bounds.Dy(), best), vp8lPayloadBits(bounds.Dx(), bounds.Dy(), balanced); got > wantMax {
		t.Fatalf("ModeBestCompression bits = %d, want <= balanced bits %d", got, wantMax)
	}
}

func encodeLosslessPlanForTest(t *testing.T, img image.Image, plan vp8lEncodingPlan) []byte {
	t.Helper()
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	payloadBits := vp8lPayloadBits(width, height, plan)
	payloadSize := (payloadBits + 7) / 8
	padding := payloadSize & 1
	riffSize := uint64(4) + 8 + payloadSize + padding

	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	if err := writeWebPHeader(bw, "VP8L", uint32(riffSize), uint32(payloadSize)); err != nil {
		t.Fatalf("writeWebPHeader failed: %v", err)
	}
	bits := newBitWriter(bw)
	writeVP8L(bits, pixelReaderFor(img), bounds, width, height, plan)
	if err := bits.flush(); err != nil {
		t.Fatalf("bit flush failed: %v", err)
	}
	if padding != 0 {
		if err := bw.WriteByte(0); err != nil {
			t.Fatalf("padding write failed: %v", err)
		}
	}
	if err := bw.Flush(); err != nil {
		t.Fatalf("buffer flush failed: %v", err)
	}
	return buf.Bytes()
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
		if got := readGrayLuma(p.X, p.Y); got != wantY {
			t.Fatalf("Gray luma at %v = %d, want %d", p, got, wantY)
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
		wantLuma := rgbToLuma(want.R, want.G, want.B)
		if got := readYCbCrLuma(p.X, p.Y); got != wantLuma {
			t.Fatalf("YCbCr luma at %v = %d, want %d", p, got, wantLuma)
		}
		gotCb, gotCr := readYCbCrChroma(p.X, p.Y)
		wantCb, wantCr := rgbToChroma(want.R, want.G, want.B)
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

func TestEncodeLosslessUsesPredictorTransformForPlane(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			v := uint8(3 * (x + 1) * (y + 1))
			img.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, nil); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if !hasVP8LFirstTransform(t, buf.Bytes(), 0) {
		t.Fatal("missing VP8L predictor transform")
	}

	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessUsesSubtractGreenTransformForGreenCorrelatedRGB(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			g := uint8((x*73 + y*151 + x*y*199 + x*x*17 + y*y*29) & 0xff)
			img.SetNRGBA(x, y, color.NRGBA{
				R: g + 7,
				G: g,
				B: g + 19,
				A: 255,
			})
		}
	}

	readPixel := pixelReaderFor(img)
	plan := chooseVP8LEncodingPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy())
	if !plan.subtractGreen {
		t.Fatal("missing VP8L subtract green transform")
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, nil); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessUsesPredictorWithSubtractGreen(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 256, 1))
	current := color.NRGBA{A: 255}
	for x := 0; x < img.Rect.Dx(); x++ {
		g := uint8((x*73 + x*x*19 + 11) & 0xff)
		residual := color.NRGBA{
			R: g + 7,
			G: g,
			B: g + 19,
			A: 0,
		}
		current.R += residual.R
		current.G += residual.G
		current.B += residual.B
		img.SetNRGBA(x, 0, current)
	}

	readPixel := pixelReaderFor(img)
	plan := chooseVP8LEncodingPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy())
	if !plan.predictor {
		t.Fatal("missing VP8L predictor transform")
	}
	if !plan.subtractGreen {
		t.Fatal("missing VP8L subtract green transform")
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, nil); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for x := 0; x < width; x++ {
		want := img.NRGBAAt(x, 0)
		if got[x] != want {
			t.Fatalf("pixel %d = %#v, want %#v", x, got[x], want)
		}
	}
}

func TestEncodeLosslessUsesColorTransformForRedBlueCorrelation(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			r := uint8((x*83 + y*157 + x*y*197 + x*x*19 + y*y*31) & 0xff)
			g := uint8((x*29 + y*47 + x*y*211 + x*x*41 + y*y*23) & 0xff)
			img.SetNRGBA(x, y, color.NRGBA{
				R: r,
				G: g,
				B: r + 23,
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, nil); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if !hasVP8LFirstTransform(t, buf.Bytes(), 1) {
		t.Fatal("missing VP8L color transform")
	}

	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessUsesPredictorWithColorTransform(t *testing.T) {
	img := newPredictorColorTransformFixture()
	readPixel := pixelReaderFor(img)
	plan := chooseVP8LEncodingPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy())
	if !plan.predictor {
		t.Fatal("missing VP8L predictor transform")
	}
	if !plan.colorTransform {
		t.Fatal("missing VP8L color transform")
	}
	if plan.subtractGreen {
		t.Fatal("unexpected VP8L subtract green transform")
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, nil); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for x := 0; x < width; x++ {
		want := img.NRGBAAt(x, 0)
		if got[x] != want {
			t.Fatalf("pixel %d = %#v, want %#v", x, got[x], want)
		}
	}
}

func TestVP8LBlockPredictorPlanUsesPerBlockModes(t *testing.T) {
	img := newMixedPredictorModeFixture()
	readPixel := pixelReaderFor(img)
	cfg := vp8lDefaultEncodingConfig()
	cfg.predictorModes = []uint8{1, 2}
	cfg.predictorBlockSizeBits = []uint8{6}

	plan, ok := makeVP8LBlockPredictorPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), false, cfg)
	if !ok {
		t.Fatal("missing block predictor plan")
	}
	if !plan.predictor || len(plan.predictorImage) != 18 {
		t.Fatalf("block predictor image length = %d, predictor=%v", len(plan.predictorImage), plan.predictor)
	}
	if got, want := plan.predictorImage, []uint8{
		1, 1, 1, 2, 2, 2,
		1, 1, 1, 2, 2, 2,
		1, 1, 1, 2, 2, 2,
	}; !slices.Equal(got, want) {
		t.Fatalf("predictor image = %v, want %v", got, want)
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	assertLosslessBenchmarkWebP(t, data, img)
}

func newMixedPredictorModeFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 384, 192))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			if x < 192 {
				img.SetNRGBA(x, y, color.NRGBA{
					R: uint8((y*73 + y*y*19 + 11) & 0xff),
					G: uint8((y*47 + y*y*23 + 29) & 0xff),
					B: uint8((y*31 + y*y*7 + 53) & 0xff),
					A: 255,
				})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{
					R: uint8((x*97 + x*x*13 + 53) & 0xff),
					G: uint8((x*43 + x*x*29 + 17) & 0xff),
					B: uint8((x*61 + x*x*11 + 71) & 0xff),
					A: 255,
				})
			}
		}
	}
	return img
}

func TestEncodeLosslessUsesColorTransformWithSubtractGreen(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 256, 1))
	for x := 0; x < img.Rect.Dx(); x++ {
		g := uint8((x*73 + x*x*19 + 11) & 0xff)
		img.SetNRGBA(x, 0, color.NRGBA{
			R: g*2 + 7,
			G: g,
			B: g*2 + 19,
			A: 255,
		})
	}

	readPixel := pixelReaderFor(img)
	plan := chooseVP8LEncodingPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy())
	if !plan.colorTransform {
		t.Fatal("missing VP8L color transform")
	}
	if !plan.subtractGreen {
		t.Fatal("missing VP8L subtract green transform")
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, nil); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for x := 0; x < width; x++ {
		want := img.NRGBAAt(x, 0)
		if got[x] != want {
			t.Fatalf("pixel %d = %#v, want %#v", x, got[x], want)
		}
	}
}

func TestVP8LPayloadBitBreakdownSeparatesTransformCosts(t *testing.T) {
	colorElement := vp8lColorTransformElement{greenToRed: 32, greenToBlue: 32}
	analysis := imageAnalysis{
		channels: [4]channelPlan{
			newConstantChannelPlan(0),
			newConstantChannelPlan(0),
			newConstantChannelPlan(0),
			newConstantChannelPlan(255),
		},
	}
	plan := vp8lEncodingPlan{
		analysis:          analysis,
		predictor:         true,
		predictorMode:     1,
		predictorSizeBits: vp8lDefaultPredictorSizeBits,
		predictorAnalysis: vp8lPredictorImageAnalysis(1),
		colorTransform:    true,
		colorSizeBits:     vp8lDefaultColorTransformSizeBits,
		colorElement:      colorElement,
		colorAnalysis:     vp8lColorTransformImageAnalysis(colorElement),
		subtractGreen:     true,
	}

	breakdown := vp8lPayloadBitBreakdownFor(16, 16, plan)
	if got, want := breakdown.total(), vp8lPayloadBits(16, 16, plan); got != want {
		t.Fatalf("payload breakdown total = %d, want %d", got, want)
	}
	if breakdown.fileHeader != 40 {
		t.Fatalf("file header bits = %d, want 40", breakdown.fileHeader)
	}
	if got, want := breakdown.transformHeaderBits(), uint64(16); got != want {
		t.Fatalf("transform header bits = %d, want %d", got, want)
	}
	wantPredictorData := vp8lImageDataBits(1, 1, plan.predictorAnalysis, false)
	if breakdown.predictorImageData != wantPredictorData {
		t.Fatalf("predictor image data bits = %d, want %d", breakdown.predictorImageData, wantPredictorData)
	}
	wantColorData := vp8lImageDataBits(1, 1, plan.colorAnalysis, false)
	if breakdown.colorImageData != wantColorData {
		t.Fatalf("color transform image data bits = %d, want %d", breakdown.colorImageData, wantColorData)
	}
	if got, want := breakdown.transformImageDataBits(), wantPredictorData+wantColorData; got != want {
		t.Fatalf("transform image data bits = %d, want %d", got, want)
	}
	wantMainData := vp8lImageDataBits(16, 16, plan.analysis, true)
	if breakdown.mainImageData != wantMainData {
		t.Fatalf("main image data bits = %d, want %d", breakdown.mainImageData, wantMainData)
	}
}

func TestEncodeLosslessUsesColorIndexingTransformForAlphaPalette(t *testing.T) {
	palette := []color.NRGBA{
		{R: 240, G: 248, B: 255, A: 255},
		{R: 32, G: 48, B: 64, A: 255},
		{R: 220, G: 48, B: 64, A: 180},
		{R: 48, G: 180, B: 120, A: 96},
	}
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, palette[(x/3+y/5+x*y/17)%len(palette)])
		}
	}

	readPixel := pixelReaderFor(img)
	plan, ok := makeVP8LColorIndexingPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), true)
	if !ok {
		t.Fatal("makeVP8LColorIndexingPlan returned false")
	}
	data := encodeLosslessPlanForTest(t, img, plan)
	if !hasVP8LFirstTransform(t, data, 3) {
		t.Fatal("missing VP8L color indexing transform")
	}

	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if !alpha {
		t.Fatal("alpha hint = false, want true")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessColorIndexingSortsColorTableWhenCheaper(t *testing.T) {
	sortedPalette := newColorIndexSortedTablePalette()
	img := newColorIndexSortedTableFixture()
	readPixel := pixelReaderFor(img)
	firstTable, firstIndex, ok := buildVP8LColorTable(readPixel, img.Bounds())
	if !ok {
		t.Fatal("buildVP8LColorTable returned false")
	}
	firstAnalysis := analyzeImage(vp8lColorTableImageReader(firstTable), image.Rect(0, 0, len(firstTable), 1))
	firstPlan := makeVP8LColorIndexingPlanForTableAndIndex(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), false, firstTable, firstIndex, firstAnalysis)
	plan, ok := makeVP8LColorIndexingPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), false)
	if !ok {
		t.Fatal("makeVP8LColorIndexingPlan returned false")
	}
	if !vp8lColorTablesEqual(plan.colorTable, sortedPalette) {
		t.Fatalf("color table = %#v, want sorted palette %#v", plan.colorTable, sortedPalette)
	}
	if gotBits, firstBits := vp8lPayloadBits(img.Rect.Dx(), img.Rect.Dy(), plan), vp8lPayloadBits(img.Rect.Dx(), img.Rect.Dy(), firstPlan); gotBits >= firstBits {
		t.Fatalf("sorted color table bits = %d, want less than first-order bits %d", gotBits, firstBits)
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestVP8LColorIndexedImageReaderHandlesCacheCollisions(t *testing.T) {
	first := color.NRGBA{R: 0, G: 0, B: 0, A: 255}
	second := color.NRGBA{}
	found := false
	for r := 0; r < 256 && !found; r++ {
		for g := 0; g < 256 && !found; g++ {
			c := color.NRGBA{R: uint8(r), G: uint8(g), B: 17, A: 255}
			if c != first && vp8lColorIndexCacheIndex(c) == vp8lColorIndexCacheIndex(first) {
				second = c
				found = true
			}
		}
	}
	if !found {
		t.Fatal("missing color index cache collision fixture")
	}

	pixels := []color.NRGBA{first, second, first, second}
	readPixel := func(x int, y int) color.NRGBA {
		return pixels[x]
	}
	readIndexed := vp8lColorIndexedImageReader(readPixel, image.Rect(0, 0, len(pixels), 1), len(pixels), 0, map[color.NRGBA]uint8{
		first:  3,
		second: 7,
	})
	for x, want := range []uint8{3, 7, 3, 7} {
		if got := readIndexed(x, 0).G; got != want {
			t.Fatalf("indexed pixel %d = %d, want %d", x, got, want)
		}
	}
}

func TestBuildVP8LPalettedColorTableMatchesGeneric(t *testing.T) {
	img := newPalettedColorIndexReaderFixture(32)
	img.Palette[31] = img.Palette[2]
	bounds := image.Rect(3, 1, 35, 5)
	readPixel := pixelReaderFor(img)
	wantTable, wantIndex, ok := buildVP8LColorTable(readPixel, bounds)
	if !ok {
		t.Fatal("buildVP8LColorTable returned false")
	}
	gotTable, gotIndex, ok := buildVP8LPalettedColorTable(img, bounds)
	if !ok {
		t.Fatal("buildVP8LPalettedColorTable returned false")
	}
	if !slices.Equal(gotTable, wantTable) {
		t.Fatalf("paletted color table = %#v, want %#v", gotTable, wantTable)
	}
	if len(gotIndex) != len(wantIndex) {
		t.Fatalf("paletted color index size = %d, want %d", len(gotIndex), len(wantIndex))
	}
	for c, want := range wantIndex {
		if got, ok := gotIndex[c]; !ok || got != want {
			t.Fatalf("paletted color index[%#v] = %d, %v; want %d, true", c, got, ok, want)
		}
	}

	width := bounds.Dx()
	height := bounds.Dy()
	genericPlan, ok := makeVP8LColorIndexingPlan(readPixel, bounds, width, height, false)
	if !ok {
		t.Fatal("makeVP8LColorIndexingPlan returned false")
	}
	palettedPlan, ok := makeVP8LPalettedColorIndexingPlan(img, bounds, width, height, false)
	if !ok {
		t.Fatal("makeVP8LPalettedColorIndexingPlan returned false")
	}
	if palettedPlan.colorIndexReader == nil {
		t.Fatal("missing paletted indexed reader")
	}
	if !vp8lColorTablesEqual(palettedPlan.colorTable, genericPlan.colorTable) {
		t.Fatalf("paletted plan color table = %#v, want %#v", palettedPlan.colorTable, genericPlan.colorTable)
	}
	if !palettedPlan.analysis.codingEqual(genericPlan.analysis) {
		t.Fatalf("paletted plan analysis = %#v, want coding equivalent to %#v", palettedPlan.analysis, genericPlan.analysis)
	}
	if got, want := vp8lPayloadBits(width, height, palettedPlan), vp8lPayloadBits(width, height, genericPlan); got != want {
		t.Fatalf("paletted plan bits = %d, want %d", got, want)
	}
}

func TestChooseVP8LEncodingPlanPalettedEarlyExitMatchesExhaustive(t *testing.T) {
	img := newBenchmarkLimitedPalettedFixtureImage(512, 512)
	readPixel := pixelReaderFor(img)
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	analysis := analyzeImage(readPixel, bounds)
	colorIndexPlan, ok := makeVP8LColorIndexingPlanForImage(img, readPixel, bounds, width, height, analysis.alpha)
	if !ok {
		t.Fatal("makeVP8LColorIndexingPlanForImage returned false")
	}
	literalPlan := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
	if !vp8lShouldUsePalettedColorIndexEarlyExit(img, colorIndexPlan, vp8lPayloadBits(width, height, colorIndexPlan), vp8lPayloadBits(width, height, literalPlan), width, height) {
		t.Fatal("paletted color index early exit was not enabled for the fixture")
	}

	fast := chooseVP8LEncodingPlanForImage(img, readPixel, bounds, width, height)
	exhaustive := chooseVP8LEncodingPlanForImageExhaustive(img, readPixel, bounds, width, height)
	if !fast.colorIndexing {
		t.Fatal("fast plan did not use color indexing")
	}
	if got, want := vp8lPayloadBits(width, height, fast), vp8lPayloadBits(width, height, exhaustive); got != want {
		t.Fatalf("fast plan bits = %d, want exhaustive bits %d", got, want)
	}
}

func TestChooseVP8LEncodingPlanNRGBAColorIndexEarlyExitMatchesExhaustive(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{
		kind:   benchmarkImageUI,
		width:  512,
		height: 512,
	})
	readPixel := pixelReaderFor(img)
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	analysis := analyzeImage(readPixel, bounds)
	colorIndexPlan, ok := makeVP8LColorIndexingPlanForImage(img, readPixel, bounds, width, height, analysis.alpha)
	if !ok {
		t.Fatal("makeVP8LColorIndexingPlanForImage returned false")
	}
	literalPlan := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
	colorIndexBits := vp8lPayloadBits(width, height, colorIndexPlan)
	literalBits := vp8lPayloadBits(width, height, literalPlan)
	if !vp8lShouldUseColorIndexEarlyExit(img, colorIndexPlan, colorIndexBits, literalBits, width, height) {
		t.Fatalf("NRGBA color index early exit was not enabled for the fixture: colorIndexBits=%d literalBits=%d table=%d reader=%v", colorIndexBits, literalBits, len(colorIndexPlan.colorTable), colorIndexPlan.colorIndexReader != nil)
	}

	fast := chooseVP8LEncodingPlanForImage(img, readPixel, bounds, width, height)
	exhaustive := chooseVP8LEncodingPlanForImageExhaustive(img, readPixel, bounds, width, height)
	if !fast.colorIndexing {
		t.Fatal("fast plan did not use color indexing")
	}
	if got, want := vp8lPayloadBits(width, height, fast), vp8lPayloadBits(width, height, exhaustive); got != want {
		t.Fatalf("fast plan bits = %d, want exhaustive bits %d", got, want)
	}
}

func TestVP8LPalettedColorIndexedImageReaderMatchesGenericReader(t *testing.T) {
	for _, paletteSize := range []int{2, 4, 16, 32} {
		t.Run(fmt.Sprintf("palette-%d", paletteSize), func(t *testing.T) {
			img := newPalettedColorIndexReaderFixture(paletteSize)
			readPixel := pixelReaderFor(img)
			table, index, ok := buildVP8LColorTable(readPixel, img.Bounds())
			if !ok {
				t.Fatal("buildVP8LColorTable returned false")
			}
			widthBits := vp8lColorIndexWidthBits(len(table))
			generic := vp8lColorIndexedImageReader(readPixel, img.Bounds(), img.Rect.Dx(), widthBits, index)
			fast, ok := vp8lPalettedColorIndexedImageReader(img, img.Bounds(), img.Rect.Dx(), widthBits, index)
			if !ok {
				t.Fatal("vp8lPalettedColorIndexedImageReader returned false")
			}
			mainWidth := divRoundUp(img.Rect.Dx(), 1<<widthBits)
			for y := 0; y < img.Rect.Dy(); y++ {
				for x := 0; x < mainWidth; x++ {
					if got, want := fast(x, y), generic(x, y); got != want {
						t.Fatalf("indexed pixel (%d,%d) = %#v, want %#v", x, y, got, want)
					}
				}
			}

			plan, ok := makeVP8LColorIndexingPlanForImage(img, readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), false)
			if !ok {
				t.Fatal("makeVP8LColorIndexingPlanForImage returned false")
			}
			if plan.colorIndexReader == nil {
				t.Fatal("missing paletted color indexing fast reader")
			}
		})
	}
}

func newPalettedColorIndexReaderFixture(paletteSize int) *image.Paletted {
	palette := make(color.Palette, paletteSize)
	for i := range palette {
		palette[i] = color.NRGBA{
			R: uint8(i * 47),
			G: uint8(17 + i*29),
			B: uint8(31 + i*13),
			A: 255,
		}
	}
	img := image.NewPaletted(image.Rect(0, 0, 37, 5), palette)
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.Pix[img.PixOffset(x, y)] = uint8((x + y*7) % paletteSize)
		}
	}
	return img
}

func TestAnalyzeVP8LColorIndexedImageMatchesGenericAnalysis(t *testing.T) {
	img := newBenchmarkLimitedPalettedFixtureImage(64, 8)
	readPixel := pixelReaderFor(img)
	table, index, ok := buildVP8LColorTable(readPixel, img.Bounds())
	if !ok {
		t.Fatal("buildVP8LColorTable returned false")
	}
	widthBits := vp8lColorIndexWidthBits(len(table))
	mainBounds := image.Rect(0, 0, divRoundUp(img.Rect.Dx(), 1<<widthBits), img.Rect.Dy())
	readIndexed := vp8lColorIndexedImageReader(readPixel, img.Bounds(), img.Rect.Dx(), widthBits, index)
	got := analyzeVP8LColorIndexedImage(readIndexed, mainBounds)
	want := analyzeImage(readIndexed, mainBounds)
	if !got.codingEqual(want) {
		t.Fatalf("indexed analysis = %#v, want coding equivalent to %#v", got, want)
	}
}

func TestVP8LLZ77WithBaseAnalysisMatchesGenericLiteralAnalysis(t *testing.T) {
	img := newBenchmarkLimitedPalettedFixtureImage(128, 32)
	readPixel := pixelReaderFor(img)
	plan, ok := makeVP8LColorIndexingPlanForImage(img, readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), false)
	if !ok {
		t.Fatal("makeVP8LColorIndexingPlanForImage returned false")
	}
	mainWidth, mainHeight := vp8lPlanImageDimensions(img.Rect.Dx(), img.Rect.Dy(), plan)
	mainBounds := image.Rect(0, 0, mainWidth, mainHeight)
	readIndexed := vp8lPlanPixelReader(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), plan)
	_, genericAnalysis, genericGreenCounts, genericDistanceCounts, genericCopyCount, genericTokenCount := vp8lBuildLZ77(readIndexed, mainBounds, mainWidth, false, 0)
	_, optimizedAnalysis, optimizedGreenCounts, optimizedDistanceCounts, optimizedCopyCount, optimizedTokenCount := vp8lBuildLZ77WithAnalysis(readIndexed, mainBounds, mainWidth, false, 0, plan.analysis)
	if genericGreenCounts != optimizedGreenCounts {
		t.Fatal("green counts differ")
	}
	if genericDistanceCounts != optimizedDistanceCounts {
		t.Fatal("distance counts differ")
	}
	if genericCopyCount != optimizedCopyCount || genericTokenCount != optimizedTokenCount {
		t.Fatalf("copy/token counts = %d/%d, want %d/%d", optimizedCopyCount, optimizedTokenCount, genericCopyCount, genericTokenCount)
	}
	for i := 1; i < len(optimizedAnalysis.channels); i++ {
		if !optimizedAnalysis.channels[i].codingEqual(genericAnalysis.channels[i]) {
			t.Fatalf("channel %d analysis = %#v, want coding equivalent to %#v", i, optimizedAnalysis.channels[i], genericAnalysis.channels[i])
		}
	}
	if !optimizedAnalysis.channels[1].constant || !optimizedAnalysis.channels[2].constant || !optimizedAnalysis.channels[3].constant {
		t.Fatalf("optimized non-green channels are not all constant: %#v", optimizedAnalysis.channels)
	}
}

func newColorIndexSortedTableFixture() *image.NRGBA {
	sortedPalette := newColorIndexSortedTablePalette()
	img := image.NewNRGBA(image.Rect(0, 0, len(sortedPalette)*8, 2))
	for x := range sortedPalette {
		paletteIndex := (x * 37) % len(sortedPalette)
		img.SetNRGBA(x, 0, sortedPalette[paletteIndex])
	}
	for x := len(sortedPalette); x < img.Rect.Dx(); x++ {
		img.SetNRGBA(x, 0, sortedPalette[x%len(sortedPalette)])
	}
	for x := 0; x < img.Rect.Dx(); x++ {
		img.SetNRGBA(x, 1, sortedPalette[x%len(sortedPalette)])
	}
	return img
}

func newColorIndexSortedTablePalette() []color.NRGBA {
	const size = 64
	sortedPalette := make([]color.NRGBA, size)
	for i := range sortedPalette {
		v := uint8(i * 3)
		sortedPalette[i] = color.NRGBA{R: v, G: v, B: v, A: 255}
	}
	return sortedPalette
}

func TestEncodeLosslessUsesLZ77BackwardReferencesForRuns(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			id := y*8 + x/8
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(id),
				G: uint8(id * 37),
				B: uint8(id * 73),
				A: 255,
			})
		}
	}

	readPixel := pixelReaderFor(img)
	analysis := analyzeImage(readPixel, img.Bounds())
	base := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
	plan, ok := makeVP8LLZ77Plan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), base, ^uint64(0))
	if !ok {
		t.Fatal("makeVP8LLZ77Plan returned false")
	}
	if !plan.lz77 {
		t.Fatal("missing VP8L LZ77 backward references")
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessUsesLZ77HashMatchesBeyondPreviousPixel(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 320, 4))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			id := x % 300
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(id),
				G: uint8(id * 37),
				B: uint8(id * 73),
				A: 255,
			})
		}
	}

	readPixel := pixelReaderFor(img)
	analysis := analyzeImage(readPixel, img.Bounds())
	base := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
	plan, ok := makeVP8LLZ77Plan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), base, ^uint64(0))
	if !ok {
		t.Fatal("makeVP8LLZ77Plan returned false")
	}
	if !plan.lz77 {
		t.Fatal("missing VP8L LZ77 backward references")
	}
	hasNonPreviousDistance := false
	tokens, _, _, _, _, _ := vp8lBuildLZ77(readPixel, img.Bounds(), img.Rect.Dx(), true, 0)
	for _, token := range tokens {
		if token.copyLength > 0 && token.distanceCode != 2 {
			hasNonPreviousDistance = true
			break
		}
	}
	if !hasNonPreviousDistance {
		t.Fatal("missing VP8L LZ77 hash match beyond previous pixel")
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessUsesColorCacheForRepeatedRecentColors(t *testing.T) {
	img := newColorCacheRecentColorsFixture()
	readPixel := pixelReaderFor(img)
	analysis := analyzeImage(readPixel, img.Bounds())
	base := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
	baseBits := vp8lPayloadBits(img.Rect.Dx(), img.Rect.Dy(), base)
	plan, ok := makeVP8LColorCachePlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), base, ^uint64(0))
	if !ok {
		t.Fatal("makeVP8LColorCachePlan returned false")
	}
	if plan.colorCache == nil {
		t.Fatal("missing color cache plan")
	}
	if gotBits := vp8lPayloadBits(img.Rect.Dx(), img.Rect.Dy(), plan); gotBits >= baseBits {
		t.Fatalf("color cache bits = %d, want less than literal-only bits %d", gotBits, baseBits)
	}
	if len(plan.colorCache.tokens) == 0 {
		t.Fatal("missing color cache tokens")
	}
	hasCacheToken := false
	for _, token := range plan.colorCache.tokens {
		if token.colorCache {
			hasCacheToken = true
			break
		}
	}
	if !hasCacheToken {
		t.Fatal("missing VP8L color cache code")
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for x := 0; x < width; x++ {
		want := img.NRGBAAt(x, 0)
		if got[x] != want {
			t.Fatalf("pixel %d = %#v, want %#v", x, got[x], want)
		}
	}
}

func TestEncodeLosslessDefaultUsesColorCacheForRepeatedRecentColors(t *testing.T) {
	img := newColorCacheRecentColorsFixture()
	readPixel := pixelReaderFor(img)
	plan := chooseVP8LEncodingPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy())
	if plan.colorCache == nil {
		t.Fatalf("missing VP8L color cache in default plan: predictor=%v colorTransform=%v subtractGreen=%v colorIndexing=%v lz77=%v", plan.predictor, plan.colorTransform, plan.subtractGreen, plan.colorIndexing, plan.lz77)
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, nil); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for x := 0; x < width; x++ {
		want := img.NRGBAAt(x, 0)
		if got[x] != want {
			t.Fatalf("pixel %d = %#v, want %#v", x, got[x], want)
		}
	}
}

func TestEncodeLosslessUsesMetaPrefixWithColorCache(t *testing.T) {
	img := newMetaPrefixColorCacheFixture()
	readPixel := pixelReaderFor(img)
	analysis := analyzeImage(readPixel, img.Bounds())
	base := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
	plan, ok := makeVP8LColorCachePlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), base, ^uint64(0))
	if !ok {
		t.Fatal("makeVP8LColorCachePlan returned false")
	}
	if plan.colorCache == nil {
		t.Fatal("missing color cache plan")
	}
	if plan.metaPrefix == nil {
		t.Fatal("missing meta prefix on color cache plan")
	}
	if len(plan.metaPrefix.colorCacheGroups) == 0 {
		t.Fatal("missing meta prefix color cache groups")
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessUsesWideColorCacheWhenNeeded(t *testing.T) {
	img := newWideColorCacheFixture()
	readPixel := pixelReaderFor(img)
	analysis := analyzeImage(readPixel, img.Bounds())
	base := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
	plan, ok := makeVP8LColorCachePlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), base, ^uint64(0))
	if !ok {
		t.Fatal("makeVP8LColorCachePlan returned false")
	}
	if plan.colorCache == nil {
		t.Fatal("missing color cache plan")
	}
	if plan.colorCache.bits < 5 {
		t.Fatalf("color cache bits = %d, want at least 5", plan.colorCache.bits)
	}

	defaultPlan := chooseVP8LEncodingPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy())
	if defaultPlan.colorCache == nil {
		t.Fatal("missing VP8L color cache in default plan")
	}
	if defaultPlan.colorCache.bits < 5 {
		t.Fatalf("default color cache bits = %d, want at least 5", defaultPlan.colorCache.bits)
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, nil); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessUsesLZ77WithColorCache(t *testing.T) {
	img := newLZ77ColorCacheFixture()
	readPixel := pixelReaderFor(img)
	plan := chooseVP8LEncodingPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy())
	if !plan.lz77 {
		t.Fatal("missing VP8L LZ77 in default plan")
	}
	if plan.colorCache == nil {
		t.Fatal("missing VP8L color cache in default plan")
	}
	hasCopyToken := false
	hasCacheToken := false
	for _, token := range plan.colorCache.tokens {
		if token.copyLength > 0 {
			hasCopyToken = true
		}
		if token.colorCache {
			hasCacheToken = true
		}
	}
	if !hasCopyToken {
		t.Fatal("missing LZ77 copy token")
	}
	if !hasCacheToken {
		t.Fatal("missing color cache token")
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, nil); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessLZ77GeneratedWriterMatchesPayloadBits(t *testing.T) {
	for _, tc := range []losslessBenchmarkCase{
		{name: "Alpha128", kind: benchmarkImageAlpha, width: 128, height: 128},
		{name: "Gradient512", kind: benchmarkImageGradient, width: 512, height: 512},
		{name: "RGBA512", kind: benchmarkImageGradient, width: 512, height: 512, format: benchmarkFixtureRGBA},
		{name: "PhotoLike256", kind: benchmarkImagePhotoLike, width: 256, height: 256},
		{name: "AlphaBands512", kind: benchmarkImageAlphaBands, width: 512, height: 512},
		{name: "EntropyRegions512", kind: benchmarkImageEntropyRegions, width: 512, height: 512},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := newLosslessBenchmarkFixtureImage(tc)
			readPixel := pixelReaderFor(img)
			bounds := img.Bounds()
			width, height := bounds.Dx(), bounds.Dy()
			plan := chooseVP8LEncodingPlanForImage(img, readPixel, bounds, width, height)
			if !plan.lz77 {
				t.Fatal("missing LZ77 plan")
			}
			if plan.colorCache != nil && len(plan.colorCache.tokens) == 0 {
				t.Fatal("missing LZ77 color cache tokens")
			}

			var buf bytes.Buffer
			bw := bufio.NewWriter(&buf)
			bits := newBitWriter(bw)
			writeVP8L(bits, readPixel, bounds, width, height, plan)
			if err := bits.flush(); err != nil {
				t.Fatal(err)
			}
			if err := bw.Flush(); err != nil {
				t.Fatal(err)
			}

			want := int((vp8lPayloadBits(width, height, plan) + 7) / 8)
			if got := buf.Len(); got != want {
				t.Fatalf("payload bytes = %d, want %d", got, want)
			}
		})
	}
}

func TestEncodeLosslessUsesPredictorLZ77WithColorCache(t *testing.T) {
	img := newPredictorLZ77ColorCacheFixture()
	readPixel := pixelReaderFor(img)
	readResidual := vp8lPredictorResidualReader(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), 1)
	candidate := vp8lEncodingPlan{
		analysis:          analyzeImage(readResidual, img.Bounds()),
		alpha:             false,
		predictor:         true,
		predictorMode:     1,
		predictorSizeBits: vp8lDefaultPredictorSizeBits,
		predictorAnalysis: vp8lPredictorImageAnalysis(1),
	}
	plan, ok := makeVP8LLZ77Plan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), candidate, ^uint64(0))
	if !ok {
		t.Fatal("makeVP8LLZ77Plan returned false")
	}
	if !plan.predictor {
		t.Fatal("missing VP8L predictor transform")
	}
	if !plan.lz77 {
		t.Fatal("missing VP8L LZ77 on predictor residuals")
	}
	if plan.colorCache == nil {
		t.Fatal("missing VP8L color cache on predictor residuals")
	}
	hasCopyToken := false
	hasCacheToken := false
	for _, token := range plan.colorCache.tokens {
		if token.copyLength > 0 {
			hasCopyToken = true
		}
		if token.colorCache {
			hasCacheToken = true
		}
	}
	if !hasCopyToken {
		t.Fatal("missing LZ77 copy token")
	}
	if !hasCacheToken {
		t.Fatal("missing color cache token")
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for x := 0; x < width; x++ {
		want := img.NRGBAAt(x, 0)
		if got[x] != want {
			t.Fatalf("pixel %d = %#v, want %#v", x, got[x], want)
		}
	}
}

func TestEncodeLosslessUsesColorTransformLZ77WithColorCache(t *testing.T) {
	img := newColorTransformLZ77ColorCacheFixture()
	readPixel := pixelReaderFor(img)
	element := vp8lColorTransformElement{greenToRed: 32}
	readTransformed := vp8lColorTransformReader(readPixel, element)
	candidate := vp8lEncodingPlan{
		analysis:       analyzeImage(readTransformed, img.Bounds()),
		alpha:          false,
		colorTransform: true,
		colorSizeBits:  vp8lDefaultColorTransformSizeBits,
		colorElement:   element,
		colorAnalysis:  vp8lColorTransformImageAnalysis(element),
	}
	plan, ok := makeVP8LLZ77Plan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), candidate, ^uint64(0))
	if !ok {
		t.Fatal("makeVP8LLZ77Plan returned false")
	}
	if !plan.colorTransform {
		t.Fatal("missing VP8L color transform")
	}
	if !plan.lz77 {
		t.Fatal("missing VP8L LZ77 on color transform residuals")
	}
	if plan.colorCache == nil {
		t.Fatal("missing VP8L color cache on color transform residuals")
	}
	hasCopyToken := false
	hasCacheToken := false
	for _, token := range plan.colorCache.tokens {
		if token.copyLength > 0 {
			hasCopyToken = true
		}
		if token.colorCache {
			hasCacheToken = true
		}
	}
	if !hasCopyToken {
		t.Fatal("missing LZ77 copy token")
	}
	if !hasCacheToken {
		t.Fatal("missing color cache token")
	}

	defaultPlan := chooseVP8LEncodingPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy())
	if !defaultPlan.colorTransform || !defaultPlan.lz77 || defaultPlan.colorCache == nil {
		t.Fatalf("default plan = colorTransform:%v lz77:%v colorCache:%v, want all enabled", defaultPlan.colorTransform, defaultPlan.lz77, defaultPlan.colorCache != nil)
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for x := 0; x < width; x++ {
		want := img.NRGBAAt(x, 0)
		if got[x] != want {
			t.Fatalf("pixel %d = %#v, want %#v", x, got[x], want)
		}
	}
}

func TestEncodeLosslessLZ77ColorCacheCountsMixedGreenSymbols(t *testing.T) {
	img := newLZ77ColorCacheFixture()
	readPixel := pixelReaderFor(img)
	plan := chooseVP8LEncodingPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy())
	if !plan.lz77 {
		t.Fatal("missing VP8L LZ77 in default plan")
	}
	if plan.colorCache == nil {
		t.Fatal("missing VP8L color cache in default plan")
	}

	var wantGreenCounts [nColorCacheGreenCodes]uint32
	var wantDistanceCounts [nDistanceCodes]uint32
	hasLiteral := false
	hasLength := false
	hasCache := false
	hasDistance := false
	for _, token := range plan.colorCache.tokens {
		switch {
		case token.copyLength > 0:
			lengthPrefix := vp8lPrefixCode(token.copyLength)
			wantGreenCounts[nLiteralCodes+lengthPrefix.code]++
			hasLength = true
			distancePrefix := vp8lDistancePrefixCode(token.distanceCode)
			wantDistanceCounts[distancePrefix.code]++
			hasDistance = true
		case token.colorCache:
			wantGreenCounts[nLiteralCodes+nLengthCodes+token.cacheIndex]++
			hasCache = true
		default:
			wantGreenCounts[token.pixel.G]++
			hasLiteral = true
		}
	}
	if !hasLiteral || !hasLength || !hasCache || !hasDistance {
		t.Fatalf("mixed symbols: literal=%v length=%v cache=%v distance=%v", hasLiteral, hasLength, hasCache, hasDistance)
	}
	greenLimit := nLiteralCodes + nLengthCodes + 1<<plan.colorCache.bits
	for symbol := 0; symbol < greenLimit; symbol++ {
		if plan.colorCache.counts[symbol] != wantGreenCounts[symbol] {
			t.Fatalf("green count[%d] = %d, want %d", symbol, plan.colorCache.counts[symbol], wantGreenCounts[symbol])
		}
	}
	for symbol := range plan.lz77DistanceCounts {
		if plan.lz77DistanceCounts[symbol] != wantDistanceCounts[symbol] {
			t.Fatalf("distance count[%d] = %d, want %d", symbol, plan.lz77DistanceCounts[symbol], wantDistanceCounts[symbol])
		}
	}
}

func TestEncodeLosslessDefaultSkipsColorCacheWhenColorIndexingWins(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	palette := []color.NRGBA{
		{R: 10, G: 20, B: 30, A: 255},
		{R: 80, G: 20, B: 120, A: 192},
		{R: 10, G: 160, B: 90, A: 128},
		{R: 220, G: 180, B: 40, A: 64},
	}
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, palette[(x/8+y/8)%len(palette)])
		}
	}

	readPixel := pixelReaderFor(img)
	plan := chooseVP8LEncodingPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy())
	if !plan.colorIndexing {
		t.Fatal("missing VP8L color indexing")
	}
	if plan.colorCache != nil {
		t.Fatal("default plan used VP8L color cache despite color indexing win")
	}
}

func TestEncodeLosslessSkipsColorCacheWhenOverheadWins(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	first := color.NRGBA{R: 10, G: 20, B: 30, A: 255}
	img.SetNRGBA(0, 0, first)
	img.SetNRGBA(1, 0, first)

	readPixel := pixelReaderFor(img)
	analysis := analyzeImage(readPixel, img.Bounds())
	base := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
	baseBits := vp8lPayloadBits(img.Rect.Dx(), img.Rect.Dy(), base)
	if plan, ok := makeVP8LColorCachePlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), base, baseBits); ok {
		t.Fatalf("makeVP8LColorCachePlan returned bits %d, want cache overhead to lose against literal-only bits %d", vp8lPayloadBits(img.Rect.Dx(), img.Rect.Dy(), plan), baseBits)
	}
}

func newColorCacheRecentColorsFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 1200, 1))
	for x := 0; x < img.Rect.Dx(); x += 3 {
		group := x / 3
		first := colorCacheFixtureColor(group, 0)
		second := colorCacheFixtureColor(group, 1)
		img.SetNRGBA(x, 0, first)
		if x+1 < img.Rect.Dx() {
			img.SetNRGBA(x+1, 0, second)
		}
		if x+2 < img.Rect.Dx() {
			img.SetNRGBA(x+2, 0, first)
		}
	}
	return img
}

func colorCacheFixtureColor(group int, salt uint32) color.NRGBA {
	v := uint32(group)*747796405 + salt*2891336453 + 0x9e3779b9
	v ^= v >> 16
	v *= 2246822519
	v ^= v >> 13
	v *= 3266489917
	v ^= v >> 16
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

func newMetaPrefixColorCacheFixture() *image.NRGBA {
	const (
		cacheBits = 4
		width     = 128
		height    = 128
	)
	colors := colorCacheFixtureColorsByIndex(cacheBits, 71)
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := x % 8
			if x >= width/2 {
				index += 8
			}
			img.SetNRGBA(x, y, colors[index])
		}
	}
	return img
}

func newWideColorCacheFixture() *image.NRGBA {
	const bits = 5
	size := 1 << bits
	img := image.NewNRGBA(image.Rect(0, 0, size*2, 20))
	for y := 0; y < img.Rect.Dy(); y++ {
		colors := colorCacheFixtureColorsByIndex(bits, uint32(17+y))
		for x, c := range colors {
			img.SetNRGBA(x, y, c)
		}
		for i := 0; i < size; i++ {
			index := i * 2
			if index >= size {
				index = (i-size/2)*2 + 1
			}
			img.SetNRGBA(size+i, y, colors[index])
		}
	}
	return img
}

func newLZ77ColorCacheFixture() *image.NRGBA {
	recent := newColorCacheRecentColorsFixture()
	img := image.NewNRGBA(image.Rect(0, 0, recent.Rect.Dx()+96, 1))
	for x := 0; x < recent.Rect.Dx(); x++ {
		img.SetNRGBA(x, 0, recent.NRGBAAt(x, 0))
	}
	for x := 0; x < 96; x++ {
		img.SetNRGBA(recent.Rect.Dx()+x, 0, recent.NRGBAAt(x, 0))
	}
	return img
}

func newPredictorLZ77ColorCacheFixture() *image.NRGBA {
	residuals := newLZ77ColorCacheFixture()
	img := image.NewNRGBA(residuals.Rect)
	current := color.NRGBA{A: 255}
	for x := 0; x < residuals.Rect.Dx(); x++ {
		residual := residuals.NRGBAAt(x, 0)
		current.R += residual.R
		current.G += residual.G
		current.B += residual.B
		img.SetNRGBA(x, 0, current)
	}
	return img
}

func newPredictorColorTransformFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 512, 1))
	current := color.NRGBA{A: 255}
	state := uint32(0x12345678)
	for x := 0; x < img.Rect.Dx(); x++ {
		state = state*1664525 + 1013904223
		g := uint8(state >> 24)
		state = state*1664525 + 1013904223
		b := uint8(state >> 24)
		delta := uint8(7)
		if x&1 != 0 {
			delta = 13
		}
		residual := color.NRGBA{
			R: g + delta,
			G: g,
			B: b,
		}
		current.R += residual.R
		current.G += residual.G
		current.B += residual.B
		img.SetNRGBA(x, 0, current)
	}
	return img
}

func newColorTransformLZ77ColorCacheFixture() *image.NRGBA {
	residuals := newColorTransformLZ77ColorCacheResidualFixture()
	img := image.NewNRGBA(residuals.Rect)
	element := vp8lColorTransformElement{greenToRed: 32}
	for x := 0; x < residuals.Rect.Dx(); x++ {
		img.SetNRGBA(x, 0, inverseVP8LColorTransform(residuals.NRGBAAt(x, 0), element))
	}
	return img
}

func newColorTransformLZ77ColorCacheResidualFixture() *image.NRGBA {
	const groups = 800
	const repeatedPrefix = 192
	width := groups*3 + repeatedPrefix
	img := image.NewNRGBA(image.Rect(0, 0, width, 1))
	for x := 0; x < groups*3; x += 3 {
		group := x / 3
		first := colorTransformLZ77ColorCacheResidualColor(group, 0)
		second := colorTransformLZ77ColorCacheResidualColor(group, 1)
		img.SetNRGBA(x, 0, first)
		img.SetNRGBA(x+1, 0, second)
		img.SetNRGBA(x+2, 0, first)
	}
	for x := 0; x < repeatedPrefix; x++ {
		img.SetNRGBA(groups*3+x, 0, img.NRGBAAt(x, 0))
	}
	return img
}

func colorTransformLZ77ColorCacheResidualColor(group int, salt uint32) color.NRGBA {
	v := uint32(group)*747796405 + salt*2891336453 + 0x85ebca6b
	v ^= v >> 16
	v *= 2246822519
	v ^= v >> 13
	v *= 3266489917
	v ^= v >> 16
	return color.NRGBA{R: 7, G: uint8(v >> 8), B: uint8(v), A: 255}
}

func colorCacheFixtureColorsByIndex(bits uint8, salt uint32) []color.NRGBA {
	size := 1 << bits
	colors := make([]color.NRGBA, size)
	seen := make([]bool, size)
	for seed, found := 0, 0; found < size; seed++ {
		c := colorCacheFixtureColor(seed, salt)
		index := vp8lColorCacheIndex(c, bits)
		if seen[index] {
			continue
		}
		seen[index] = true
		colors[index] = c
		found++
	}
	return colors
}

func TestEncodeLosslessUsesPredictorTransformWithLZ77ForRepeatingResiduals(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 512, 1))
	residuals := []color.NRGBA{
		{R: 1, G: 3, B: 5},
		{R: 2, G: 5, B: 7},
		{R: 3, G: 7, B: 11},
		{R: 5, G: 11, B: 13},
		{R: 7, G: 13, B: 17},
		{R: 11, G: 17, B: 19},
		{R: 13, G: 19, B: 23},
		{R: 17, G: 23, B: 29},
	}
	current := color.NRGBA{A: 255}
	for x := 0; x < img.Rect.Dx(); x++ {
		r := residuals[x%len(residuals)]
		current.R += r.R
		current.G += r.G
		current.B += r.B
		img.SetNRGBA(x, 0, current)
	}

	readPixel := pixelReaderFor(img)
	candidate := vp8lEncodingPlan{
		analysis:          analyzeImage(vp8lPredictorResidualReader(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), 1), img.Bounds()),
		alpha:             false,
		predictor:         true,
		predictorMode:     1,
		predictorSizeBits: vp8lDefaultPredictorSizeBits,
		predictorAnalysis: vp8lPredictorImageAnalysis(1),
	}
	plan, ok := makeVP8LLZ77Plan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), candidate, ^uint64(0))
	if !ok {
		t.Fatal("makeVP8LLZ77Plan returned false")
	}
	if !plan.predictor {
		t.Fatal("missing VP8L predictor transform")
	}
	if !plan.lz77 {
		t.Fatal("missing VP8L LZ77 on predictor residuals")
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for x := 0; x < width; x++ {
		want := img.NRGBAAt(x, 0)
		if got[x] != want {
			t.Fatalf("pixel %d = %#v, want %#v", x, got[x], want)
		}
	}
}

func TestEncodeLosslessUsesLZ77AfterCombinedTransforms(t *testing.T) {
	img := newCombinedTransformLZ77Fixture(512, 512)
	readPixel := pixelReaderFor(img)
	plan := chooseVP8LEncodingPlanForImage(img, readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy())
	if !plan.predictor || !plan.subtractGreen || !plan.lz77 {
		t.Fatalf("plan predictor=%v subtractGreen=%v lz77=%v, want predictor+subtractGreen+LZ77", plan.predictor, plan.subtractGreen, plan.lz77)
	}
	withoutLZ77 := plan
	withoutLZ77.lz77 = false
	if got, wantMax := vp8lPayloadBits(img.Rect.Dx(), img.Rect.Dy(), plan), vp8lPayloadBits(img.Rect.Dx(), img.Rect.Dy(), withoutLZ77); got >= wantMax {
		t.Fatalf("combined-transform LZ77 bits = %d, want less than non-LZ77 bits %d", got, wantMax)
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func newCombinedTransformLZ77Fixture(width int, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			base := (x*37 + y*53 + x*y*3) & 0xff
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((base + x/3 + y/5) & 0xff),
				G: uint8((base + x/7 + y/2 + 17) & 0xff),
				B: uint8((base + x/5 + y/11 + 41) & 0xff),
				A: 255,
			})
		}
	}
	return img
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

func TestVP8LMetaPrefixImageDimensionsAndIndex(t *testing.T) {
	width, height := vp8lMetaPrefixImageDimensions(513, 257, 4)
	if width != 33 || height != 17 {
		t.Fatalf("meta prefix dimensions = %dx%d, want 33x17", width, height)
	}
	if got := vp8lMetaPrefixIndex(0, 0, 4, width); got != 0 {
		t.Fatalf("index at origin = %d, want 0", got)
	}
	if got := vp8lMetaPrefixIndex(15, 15, 4, width); got != 0 {
		t.Fatalf("index at last pixel in first block = %d, want 0", got)
	}
	if got := vp8lMetaPrefixIndex(16, 0, 4, width); got != 1 {
		t.Fatalf("index at second column block = %d, want 1", got)
	}
	if got := vp8lMetaPrefixIndex(0, 16, 4, width); got != width {
		t.Fatalf("index at second row block = %d, want %d", got, width)
	}
	if got := vp8lMetaPrefixIndex(512, 256, 4, width); got != width*height-1 {
		t.Fatalf("index at final partial block = %d, want %d", got, width*height-1)
	}
}

func TestVP8LMetaPrefixCopyStaysInGroup(t *testing.T) {
	metaPrefix := &vp8lMetaPrefixPlan{
		prefixBits: 2,
		width:      2,
		height:     1,
		image:      []uint16{0, 1},
	}
	if !vp8lMetaPrefixCopyStaysInGroup(metaPrefix, 8, 0, 4) {
		t.Fatal("copy inside one meta prefix group was rejected")
	}
	if vp8lMetaPrefixCopyStaysInGroup(metaPrefix, 8, 3, 2) {
		t.Fatal("copy crossing meta prefix groups was accepted")
	}
}

func TestVP8LSplitLZ77TokensAtMetaPrefixBoundaries(t *testing.T) {
	metaPrefix := &vp8lMetaPrefixPlan{
		prefixBits: 2,
		width:      2,
		height:     1,
		image:      []uint16{0, 1},
		groups:     make([]imageAnalysis, 2),
	}
	bounds := image.Rect(0, 0, 8, 1)
	readPixel := func(x int, y int) color.NRGBA {
		return color.NRGBA{R: uint8(x), G: uint8(x + 1), B: uint8(x + 2), A: 255}
	}
	tokens := []vp8lToken{
		{pixel: readPixel(0, 0)},
		{pixel: readPixel(1, 0)},
		{copyLength: 6, distanceCode: 1},
	}

	split, ok := vp8lSplitLZ77TokensAtMetaPrefixBoundaries(readPixel, bounds, metaPrefix, tokens, 8, 8)
	if !ok {
		t.Fatal("vp8lSplitLZ77TokensAtMetaPrefixBoundaries returned false")
	}
	if len(split) != 5 {
		t.Fatalf("split token count = %d, want 5", len(split))
	}
	if split[2].copyLength != 0 || split[2].pixel != readPixel(2, 0) {
		t.Fatalf("first boundary fragment = %+v, want literal pixel at x=2", split[2])
	}
	if split[3].copyLength != 0 || split[3].pixel != readPixel(3, 0) {
		t.Fatalf("second boundary fragment = %+v, want literal pixel at x=3", split[3])
	}
	if split[4].copyLength != 4 || split[4].distanceCode != 1 {
		t.Fatalf("second split copy = %+v, want length 4 distance 1", split[4])
	}

	_, groupTokens, ok := vp8lBuildMetaPrefixLZ77Groups(metaPrefix, split, 8, 8, imageAnalysis{})
	if !ok {
		t.Fatal("vp8lBuildMetaPrefixLZ77Groups rejected split tokens")
	}
	if groupTokens[0] != 4 || groupTokens[1] != 1 {
		t.Fatalf("group token counts = %v, want [4 1]", groupTokens)
	}

	sameGroupMetaPrefix := &vp8lMetaPrefixPlan{
		prefixBits: 2,
		width:      2,
		height:     1,
		image:      []uint16{0, 0},
		groups:     make([]imageAnalysis, 1),
	}
	sameGroupSplit, ok := vp8lSplitLZ77TokensAtMetaPrefixBoundaries(readPixel, bounds, sameGroupMetaPrefix, tokens, 8, 8)
	if !ok {
		t.Fatal("same-group split returned false")
	}
	if len(sameGroupSplit) != 5 || sameGroupSplit[2].copyLength != 0 || sameGroupSplit[3].copyLength != 0 || sameGroupSplit[4].copyLength != 4 {
		t.Fatalf("same-group split tokens = %+v, want short literals and length 4 copy at block boundary", sameGroupSplit)
	}
}

func TestEncodeLosslessUsesMetaPrefixForLocalEntropy(t *testing.T) {
	img := newMetaPrefixLocalEntropyFixture()
	readPixel := pixelReaderFor(img)
	analysis := analyzeImage(readPixel, img.Bounds())
	base := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
	baseBits := vp8lPayloadBits(img.Rect.Dx(), img.Rect.Dy(), base)
	plan, ok := makeVP8LMetaPrefixPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), base, baseBits)
	if !ok {
		t.Fatal("makeVP8LMetaPrefixPlan returned false")
	}
	if plan.metaPrefix == nil {
		t.Fatal("missing VP8L meta prefix plan")
	}
	if got := len(plan.metaPrefix.groups); got != 2 {
		t.Fatalf("meta prefix group count = %d, want 2", got)
	}
	if gotBits := vp8lPayloadBits(img.Rect.Dx(), img.Rect.Dy(), plan); gotBits >= baseBits {
		t.Fatalf("meta prefix bits = %d, want less than literal-only bits %d", gotBits, baseBits)
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if !alpha {
		t.Fatal("alpha hint = false, want true")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessDefaultUsesMetaPrefixForLocalEntropy(t *testing.T) {
	img := newMetaPrefixLocalEntropyFixture()
	readPixel := pixelReaderFor(img)
	plan := chooseVP8LEncodingPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy())
	if plan.metaPrefix == nil {
		t.Fatalf("missing VP8L meta prefix in default plan: predictor=%v colorTransform=%v subtractGreen=%v colorIndexing=%v lz77=%v colorCache=%v", plan.predictor, plan.colorTransform, plan.subtractGreen, plan.colorIndexing, plan.lz77, plan.colorCache != nil)
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, nil); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if !alpha {
		t.Fatal("alpha hint = false, want true")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessUsesTwoSymbolChannelTree(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 1))
	first := color.NRGBA{R: 12, G: 10, B: 200, A: 255}
	second := color.NRGBA{R: 12, G: 200, B: 200, A: 255}
	for x := 0; x < img.Rect.Dx(); x++ {
		if x%2 == 0 {
			img.SetNRGBA(x, 0, first)
		} else {
			img.SetNRGBA(x, 0, second)
		}
	}

	readPixel := pixelReaderFor(img)
	analysis := analyzeImage(readPixel, img.Bounds())
	if !analysis.channels[0].twoSymbol() {
		t.Fatal("green channel did not record a two-symbol plan")
	}
	if got := channelDataBits(analysis.channels[0], nLiteralCodes+nLengthCodes, img.Rect.Dx()*img.Rect.Dy()); got != uint64(img.Rect.Dx()*img.Rect.Dy()) {
		t.Fatalf("two-symbol channel data bits = %d, want %d", got, img.Rect.Dx()*img.Rect.Dy())
	}
	if got, wantLessThan := huffmanTreeBits(analysis.channels[0], nLiteralCodes+nLengthCodes), full8TreeBits(nLiteralCodes+nLengthCodes); got >= wantLessThan {
		t.Fatalf("two-symbol tree bits = %d, want less than full8 tree bits %d", got, wantLessThan)
	}

	plan := vp8lEncodingPlan{analysis: analysis}
	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for x := 0; x < width; x++ {
		want := img.NRGBAAt(x, 0)
		if got[x] != want {
			t.Fatalf("pixel %d = %#v, want %#v", x, got[x], want)
		}
	}
}

func TestEncodeLosslessUsesNormalChannelTreeForSmallSymbolSet(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 96, 1))
	values := []uint8{17, 93, 211}
	for x := 0; x < img.Rect.Dx(); x++ {
		img.SetNRGBA(x, 0, color.NRGBA{
			R: 12,
			G: values[x%len(values)],
			B: 200,
			A: 255,
		})
	}

	readPixel := pixelReaderFor(img)
	analysis := analyzeImage(readPixel, img.Bounds())
	if !analysis.channels[0].normal {
		t.Fatal("green channel did not use a normal Huffman tree")
	}
	if got, wantLessThan := huffmanTreeBits(analysis.channels[0], nLiteralCodes+nLengthCodes), full8TreeBits(nLiteralCodes+nLengthCodes); got >= wantLessThan {
		t.Fatalf("normal tree bits = %d, want less than full8 tree bits %d", got, wantLessThan)
	}
	if got, wantLessThan := channelDataBits(analysis.channels[0], nLiteralCodes+nLengthCodes, img.Rect.Dx()*img.Rect.Dy()), uint64(img.Rect.Dx()*img.Rect.Dy()*8); got >= wantLessThan {
		t.Fatalf("normal tree data bits = %d, want less than full8 data bits %d", got, wantLessThan)
	}

	plan := vp8lEncodingPlan{analysis: analysis}
	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for x := 0; x < width; x++ {
		want := img.NRGBAAt(x, 0)
		if got[x] != want {
			t.Fatalf("pixel %d = %#v, want %#v", x, got[x], want)
		}
	}
}

func TestChannelTreeSelectionUsesHeaderAndDataCost(t *testing.T) {
	ch := channelPlan{
		n:       3,
		normal:  true,
		symbols: [vp8lMaxChannelSmallSymbols]uint8{0, 1, 2},
		counts:  [vp8lMaxChannelSmallSymbols]uint32{100, 100, 100},
		lengths: [vp8lMaxChannelSmallSymbols]uint8{15, 15, 15},
		codes:   [vp8lMaxChannelSmallSymbols]uint16{0, 1, 2},
	}
	if channelUseNormal(ch, nLiteralCodes) {
		t.Fatal("normal tree selected despite larger header and data cost")
	}
	if got, want := huffmanTreeBits(ch, nLiteralCodes), full8TreeBits(nLiteralCodes); got != want {
		t.Fatalf("tree bits = %d, want full8 tree bits %d", got, want)
	}
	if got, want := channelDataBits(ch, nLiteralCodes, 300), uint64(2400); got != want {
		t.Fatalf("data bits = %d, want full8 data bits %d", got, want)
	}

	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	bits := newBitWriter(bw)
	writeChannelTree(bits, ch, nLiteralCodes)
	writeChannelSymbol(bits, ch, nLiteralCodes, 0)
	writeChannelSymbol(bits, ch, nLiteralCodes, 1)
	writeChannelSymbol(bits, ch, nLiteralCodes, 2)
	if err := bits.flush(); err != nil {
		t.Fatalf("bit flush failed: %v", err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatalf("buffer flush failed: %v", err)
	}

	r := testBitReader{data: buf.Bytes()}
	tree, err := decodeEncoderTree(&r, nLiteralCodes)
	if err != nil {
		t.Fatalf("decodeEncoderTree failed: %v", err)
	}
	for want := 0; want < 3; want++ {
		got, err := decodeEncoderSymbolInt(&r, tree)
		if err != nil {
			t.Fatalf("decodeEncoderSymbolInt failed: %v", err)
		}
		if got != want {
			t.Fatalf("symbol = %d, want %d", got, want)
		}
	}
}

func TestChannelPlanObserveSymbolCountTracksRepeatedSymbols(t *testing.T) {
	var ch channelPlan
	ch.observeSymbolCount(9, 1)
	ch.observeSymbolCount(3, 2)
	ch.observeSymbolCount(9, 4)
	ch.observeSymbolCount(9, 8)
	if ch.n != 2 {
		t.Fatalf("symbol count = %d, want 2", ch.n)
	}
	if ch.symbols[0] != 3 || ch.counts[0] != 2 {
		t.Fatalf("first symbol = %d count = %d, want symbol 3 count 2", ch.symbols[0], ch.counts[0])
	}
	if ch.symbols[1] != 9 || ch.counts[1] != 13 {
		t.Fatalf("second symbol = %d count = %d, want symbol 9 count 13", ch.symbols[1], ch.counts[1])
	}
}

func TestEncodeLosslessMetaPrefixChoosesPrefixBits(t *testing.T) {
	img := newMetaPrefixFineLocalEntropyFixture()
	readPixel := pixelReaderFor(img)
	analysis := analyzeImage(readPixel, img.Bounds())
	base := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
	baseBits := vp8lPayloadBits(img.Rect.Dx(), img.Rect.Dy(), base)
	plan, ok := makeVP8LMetaPrefixPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), base, baseBits)
	if !ok {
		t.Fatal("makeVP8LMetaPrefixPlan returned false")
	}
	if plan.metaPrefix == nil {
		t.Fatal("missing VP8L meta prefix plan")
	}
	if got := plan.metaPrefix.prefixBits; got != 6 {
		t.Fatalf("meta prefix bits = %d, want 6", got)
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, _, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessMetaPrefixMergesCodingEquivalentBlocks(t *testing.T) {
	img := newMetaPrefixCodingEquivalentFixture()
	readPixel := pixelReaderFor(img)
	analysis := analyzeImage(readPixel, img.Bounds())
	base := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
	plan, ok := makeVP8LMetaPrefixPlanForBits(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), base, 6)
	if !ok {
		t.Fatal("makeVP8LMetaPrefixPlanForBits returned false")
	}
	if got := len(plan.metaPrefix.groups); got != 2 {
		t.Fatalf("meta prefix group count = %d, want 2", got)
	}
	wantImage := []uint16{0, 0, 1}
	if !slices.Equal(plan.metaPrefix.image, wantImage) {
		t.Fatalf("meta prefix image = %v, want %v", plan.metaPrefix.image, wantImage)
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, _, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessUsesMetaPrefixWithPredictorTransform(t *testing.T) {
	img := newPredictorMetaPrefixFixture()
	readPixel := pixelReaderFor(img)
	residualReader := vp8lPredictorResidualReader(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), 1)
	residualAnalysis := analyzeImage(residualReader, img.Bounds())
	candidate := vp8lEncodingPlan{
		analysis:          residualAnalysis,
		alpha:             true,
		predictor:         true,
		predictorMode:     1,
		predictorSizeBits: vp8lDefaultPredictorSizeBits,
		predictorAnalysis: vp8lPredictorImageAnalysis(1),
	}
	candidateBits := vp8lPayloadBits(img.Rect.Dx(), img.Rect.Dy(), candidate)
	plan, ok := makeVP8LMetaPrefixPlan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), candidate, candidateBits)
	if !ok {
		t.Fatal("makeVP8LMetaPrefixPlan returned false")
	}
	if !plan.predictor {
		t.Fatal("missing VP8L predictor transform")
	}
	if plan.metaPrefix == nil {
		t.Fatal("missing VP8L meta prefix plan")
	}
	if gotBits := vp8lPayloadBits(img.Rect.Dx(), img.Rect.Dy(), plan); gotBits >= candidateBits {
		t.Fatalf("predictor meta prefix bits = %d, want less than predictor-only bits %d", gotBits, candidateBits)
	}
	best, _ := vp8lConsiderCandidateMetaPrefix(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), candidate, candidate, candidateBits)
	if best.metaPrefix == nil {
		t.Fatal("vp8lConsiderCandidateMetaPrefix did not select predictor meta prefix")
	}

	data := encodeLosslessPlanForTest(t, img, plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if !alpha {
		t.Fatal("alpha hint = false, want true")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func TestEncodeLosslessUsesMetaPrefixWithLZ77(t *testing.T) {
	img := newMetaPrefixLZ77Fixture()
	readPixel := pixelReaderFor(img)
	analysis := analyzeImage(readPixel, img.Bounds())
	base := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
	lz77Plan, ok := makeVP8LLZ77Plan(readPixel, img.Bounds(), img.Rect.Dx(), img.Rect.Dy(), base, ^uint64(0))
	if !ok {
		t.Fatal("makeVP8LLZ77Plan returned false")
	}
	if !lz77Plan.lz77 {
		t.Fatal("missing VP8L LZ77 plan")
	}
	if lz77Plan.metaPrefix == nil {
		t.Fatal("missing VP8L meta prefix on LZ77 plan")
	}
	if len(lz77Plan.metaPrefix.lz77Groups) == 0 {
		t.Fatal("missing meta prefix LZ77 groups")
	}

	data := encodeLosslessPlanForTest(t, img, lz77Plan)
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != img.Rect.Dx() || height != img.Rect.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, img.Rect.Dx(), img.Rect.Dy())
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := img.NRGBAAt(x, y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func newMetaPrefixLZ77Fixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 1))
	for x := 0; x < 16; x++ {
		c := color.NRGBA{
			R: uint8(17 + x*3),
			G: uint8(41 + x*5),
			B: uint8(83 + x*7),
			A: 255,
		}
		img.SetNRGBA(x, 0, c)
		img.SetNRGBA(16+x, 0, c)
		d := color.NRGBA{
			R: uint8(191 - x*3),
			G: uint8(149 - x*5),
			B: uint8(107 - x*7),
			A: 255,
		}
		img.SetNRGBA(32+x, 0, d)
		img.SetNRGBA(48+x, 0, d)
	}
	return img
}

func newMetaPrefixLocalEntropyFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 512, 256))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			v := uint32(x)*747796405 + uint32(y)*2891336453 + uint32(x*y)*3266489917 + 0x9e3779b9
			v ^= v >> 16
			v *= 2246822519
			v ^= v >> 13
			v *= 3266489917
			v ^= v >> 16
			a := uint8(255)
			if x >= 256 {
				a = uint8(32 + (v>>24)%224)
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(v >> 16),
				G: uint8(v >> 8),
				B: uint8(v),
				A: a,
			})
		}
	}
	return img
}

func newPredictorMetaPrefixFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 512, 256))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			residual := predictorMetaPrefixResidual(x, y)
			pred := color.NRGBA{A: 255}
			if x == 0 && y > 0 {
				pred = img.NRGBAAt(x, y-1)
			} else if x > 0 {
				pred = img.NRGBAAt(x-1, y)
			}
			img.SetNRGBA(x, y, addNRGBA(pred, residual))
		}
	}
	return img
}

func predictorMetaPrefixResidual(x int, y int) color.NRGBA {
	v := uint32(x)*2246822519 + uint32(y)*3266489917 + uint32(x*y)*668265263 + 0x85ebca6b
	v ^= v >> 15
	v *= 2246822519
	v ^= v >> 13
	v *= 3266489917
	v ^= v >> 16
	residual := color.NRGBA{
		R: uint8(v >> 16),
		G: uint8(v >> 8),
		B: uint8(v),
	}
	if x >= 256 {
		residual.A = uint8(32 + (v>>24)%224)
	}
	return residual
}

func newMetaPrefixCodingEquivalentFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 192, 64))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			v := uint32(x)*2246822519 + uint32(y)*3266489917 + uint32(x/64+1)*668265263 + uint32(x*y)*374761393
			v ^= v >> 15
			v *= 2246822519
			v ^= v >> 13
			v *= 3266489917
			v ^= v >> 16
			a := uint8(255)
			if x >= 128 {
				a = uint8(32 + (v>>24)%224)
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(v >> 16),
				G: uint8(v >> 8),
				B: uint8(v),
				A: a,
			})
		}
	}
	return img
}

func newMetaPrefixFineLocalEntropyFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 128, 64))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			v := uint32(x)*2246822519 + uint32(y)*3266489917 + uint32(x*y)*668265263 + 0x85ebca6b
			v ^= v >> 15
			v *= 2246822519
			v ^= v >> 13
			v *= 3266489917
			v ^= v >> 16
			a := uint8(255)
			if x >= 64 {
				a = uint8(32 + (v>>24)%224)
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(v >> 16),
				G: uint8(v >> 8),
				B: uint8(v),
				A: a,
			})
		}
	}
	return img
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

func TestVP8LBestHashMatchChoosesLongerOlderCandidate(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 1))
	pattern := []color.NRGBA{
		{R: 1, G: 10, B: 20, A: 255},
		{R: 2, G: 11, B: 21, A: 255},
		{R: 3, G: 12, B: 22, A: 255},
		{R: 4, G: 13, B: 23, A: 255},
		{R: 5, G: 14, B: 24, A: 255},
		{R: 6, G: 15, B: 25, A: 255},
		{R: 7, G: 16, B: 26, A: 255},
		{R: 8, G: 17, B: 27, A: 255},
	}
	for i, c := range pattern {
		img.SetNRGBA(i, 0, c)
		img.SetNRGBA(16+i, 0, c)
	}
	for i := 0; i < 4; i++ {
		img.SetNRGBA(8+i, 0, pattern[i])
	}
	img.SetNRGBA(12, 0, color.NRGBA{R: 200, G: 201, B: 202, A: 255})
	img.SetNRGBA(24, 0, color.NRGBA{R: 210, G: 211, B: 212, A: 255})

	readPixel := pixelReaderFor(img)
	match := vp8lBestHashMatch(
		vp8lHashCandidateList{8, 0, -1, -1},
		vp8lMinHashCandidates,
		readPixel,
		img.Bounds(),
		img.Rect.Dx(),
		16,
		img.Rect.Dx()*img.Rect.Dy(),
	)
	if match.length != len(pattern) {
		t.Fatalf("match length = %d, want %d", match.length, len(pattern))
	}
	if match.distance != 16 {
		t.Fatalf("match distance = %d, want 16", match.distance)
	}
}

func TestVP8LBestHashMatchStopsAtMaximumLength(t *testing.T) {
	pixels := []color.NRGBA{
		{R: 1, G: 10, B: 20, A: 255},
		{R: 2, G: 11, B: 21, A: 255},
		{R: 3, G: 12, B: 22, A: 255},
		{R: 4, G: 13, B: 23, A: 255},
		{R: 1, G: 10, B: 20, A: 255},
		{R: 2, G: 11, B: 21, A: 255},
		{R: 3, G: 12, B: 22, A: 255},
		{R: 4, G: 13, B: 23, A: 255},
	}
	readCount := 0
	readPixel := func(x int, y int) color.NRGBA {
		if y != 0 {
			t.Fatalf("unexpected y = %d", y)
		}
		readCount++
		return pixels[x]
	}
	match := vp8lBestHashMatch(
		vp8lHashCandidateList{0, 1, -1, -1},
		vp8lMinHashCandidates,
		readPixel,
		image.Rect(0, 0, len(pixels), 1),
		len(pixels),
		4,
		len(pixels),
	)
	if match.length != 4 {
		t.Fatalf("match length = %d, want 4", match.length)
	}
	if match.distance != 4 {
		t.Fatalf("match distance = %d, want 4", match.distance)
	}
	if readCount != 8 {
		t.Fatalf("read count = %d, want 8", readCount)
	}
}

func TestVP8LHashCandidateCountBalancesMatchQualityAndMemory(t *testing.T) {
	tests := []struct {
		name  string
		total int
		want  int
	}{
		{name: "small", total: 128 * 128, want: vp8lMaxHashCandidates},
		{name: "medium", total: 512 * 512, want: vp8lMidHashCandidates},
		{name: "large", total: 512*512 + 1, want: vp8lMinHashCandidates},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vp8lHashCandidateCount(tt.total); got != tt.want {
				t.Fatalf("candidate count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestVP8LLZ77CandidateCountsTryExpandedSearchForMediumImages(t *testing.T) {
	tests := []struct {
		name  string
		total int
		want  []int
	}{
		{name: "small", total: 128 * 128, want: []int{vp8lMaxHashCandidates}},
		{name: "medium", total: 512 * 512, want: []int{vp8lMidHashCandidates, vp8lMaxHashCandidates}},
		{name: "large", total: 512*512 + 1, want: []int{vp8lMinHashCandidates}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vp8lLZ77CandidateCounts(tt.total); !slices.Equal(got, tt.want) {
				t.Fatalf("candidate counts = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVP8LShouldTryCandidateLZ77(t *testing.T) {
	base := vp8lEncodingPlan{
		analysis: imageAnalysis{
			channels: [4]channelPlan{
				{constant: true, value: 0},
				{constant: false},
				{constant: true, value: 0},
				{constant: true, value: 255},
			},
		},
	}
	constant := vp8lEncodingPlan{
		analysis: imageAnalysis{
			channels: [4]channelPlan{
				{constant: true, value: 0},
				{constant: true, value: 0},
				{constant: true, value: 0},
				{constant: true, value: 255},
			},
		},
	}

	tests := []struct {
		name          string
		candidate     vp8lEncodingPlan
		candidateBits uint64
		bestBits      uint64
		want          bool
	}{
		{name: "candidate beats best", candidate: base, candidateBits: 9000, bestBits: 10000, want: true},
		{name: "near candidate", candidate: base, candidateBits: 19000, bestBits: 10000, want: true},
		{name: "distant candidate", candidate: base, candidateBits: 20000, bestBits: 10000, want: false},
		{name: "constant candidate", candidate: constant, candidateBits: 9000, bestBits: 10000, want: false},
		{name: "already lz77", candidate: vp8lEncodingPlan{analysis: base.analysis, lz77: true}, candidateBits: 9000, bestBits: 10000, want: false},
		{name: "multiple transforms", candidate: vp8lEncodingPlan{analysis: base.analysis, predictor: true, subtractGreen: true}, candidateBits: 9000, bestBits: 10000, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vp8lShouldTryCandidateLZ77(tt.candidate, tt.candidateBits, tt.bestBits); got != tt.want {
				t.Fatalf("vp8lShouldTryCandidateLZ77 = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVP8LShouldTryTransformedLZ77ColorCache(t *testing.T) {
	predictor := vp8lEncodingPlan{
		predictor: true,
		lz77:      true,
	}
	tests := []struct {
		name      string
		plan      vp8lEncodingPlan
		breakdown vp8lPayloadBitBreakdown
		lz77Bits  uint64
		maxBits   uint64
		want      bool
	}{
		{
			name: "single predictor transform with small overhead",
			plan: predictor,
			breakdown: vp8lPayloadBitBreakdown{
				predictorHeader:     6,
				predictorImageData:  64,
				transformTerminator: 1,
				mainImageData:       vp8lMinTransformedLZ77CacheBits,
			},
			lz77Bits: 900,
			maxBits:  1000,
			want:     true,
		},
		{
			name: "single color transform with cheap residual channel",
			plan: vp8lEncodingPlan{
				colorTransform: true,
				lz77:           true,
				analysis: imageAnalysis{
					channels: [4]channelPlan{
						newConstantChannelPlan(0),
						newConstantChannelPlan(7),
						{constant: false, n: -1},
						newConstantChannelPlan(255),
					},
				},
			},
			breakdown: vp8lPayloadBitBreakdown{
				colorHeader:         6,
				colorImageData:      64,
				transformTerminator: 1,
				mainImageData:       vp8lMinTransformedLZ77CacheBits,
			},
			lz77Bits: 900,
			maxBits:  1000,
			want:     true,
		},
		{
			name: "single color transform with noisy residual channels",
			plan: vp8lEncodingPlan{
				colorTransform: true,
				lz77:           true,
				analysis: imageAnalysis{
					channels: [4]channelPlan{
						newConstantChannelPlan(0),
						{constant: false, n: -1},
						{constant: false, n: -1},
						newConstantChannelPlan(255),
					},
				},
			},
			breakdown: vp8lPayloadBitBreakdown{
				colorHeader:         6,
				colorImageData:      64,
				transformTerminator: 1,
				mainImageData:       vp8lMinTransformedLZ77CacheBits,
			},
			lz77Bits: 900,
			maxBits:  1000,
			want:     false,
		},
		{
			name: "lz77 can be tied with best",
			plan: predictor,
			breakdown: vp8lPayloadBitBreakdown{
				predictorHeader:     6,
				predictorImageData:  64,
				transformTerminator: 1,
				mainImageData:       vp8lMinTransformedLZ77CacheBits,
			},
			lz77Bits: 1000,
			maxBits:  1000,
			want:     true,
		},
		{
			name: "lz77 too far from best",
			plan: predictor,
			breakdown: vp8lPayloadBitBreakdown{
				predictorHeader:     6,
				predictorImageData:  64,
				transformTerminator: 1,
				mainImageData:       vp8lMinTransformedLZ77CacheBits,
			},
			lz77Bits: 1000 + vp8lTransformedLZ77ColorCacheTrialSlack(1000) + 1,
			maxBits:  1000,
			want:     false,
		},
		{
			name: "small transformed stream",
			plan: predictor,
			breakdown: vp8lPayloadBitBreakdown{
				predictorHeader:     6,
				predictorImageData:  64,
				transformTerminator: 1,
				mainImageData:       vp8lMinTransformedLZ77CacheBits - 1,
			},
			lz77Bits: 900,
			maxBits:  1000,
			want:     false,
		},
		{
			name: "high transform overhead",
			plan: predictor,
			breakdown: vp8lPayloadBitBreakdown{
				predictorHeader:     6,
				predictorImageData:  5000,
				transformTerminator: 1,
				mainImageData:       vp8lMinTransformedLZ77CacheBits,
			},
			lz77Bits: 900,
			maxBits:  1000,
			want:     false,
		},
		{
			name: "multiple transforms",
			plan: vp8lEncodingPlan{
				predictor:     true,
				subtractGreen: true,
				lz77:          true,
			},
			breakdown: vp8lPayloadBitBreakdown{
				predictorHeader:     6,
				predictorImageData:  64,
				subtractGreenHeader: 3,
				transformTerminator: 1,
				mainImageData:       vp8lMinTransformedLZ77CacheBits,
			},
			lz77Bits: 900,
			maxBits:  1000,
			want:     false,
		},
		{
			name: "subtract green only",
			plan: vp8lEncodingPlan{
				subtractGreen: true,
				lz77:          true,
			},
			breakdown: vp8lPayloadBitBreakdown{
				subtractGreenHeader: 3,
				transformTerminator: 1,
				mainImageData:       vp8lMinTransformedLZ77CacheBits,
			},
			lz77Bits: 900,
			maxBits:  1000,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vp8lShouldTryTransformedLZ77ColorCache(tt.plan, tt.breakdown, tt.lz77Bits, tt.maxBits)
			if got != tt.want {
				t.Fatalf("vp8lShouldTryTransformedLZ77ColorCache = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVP8LShouldTryLZ77ColorCacheSkipsHugeTransformedTokenStream(t *testing.T) {
	if !vp8lShouldTryLZ77ColorCache(vp8lEncodingPlan{}, 16, 16, 100, 100, vp8lMaxTransformedLZ77CacheTokens+1) {
		t.Fatal("non-transform LZ77 color cache trial was skipped")
	}
	if vp8lShouldTryLZ77ColorCache(vp8lEncodingPlan{predictor: true}, 16, 16, 100, 100, vp8lMaxTransformedLZ77CacheTokens+1) {
		t.Fatal("huge transformed LZ77 color cache trial was not skipped")
	}

	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	cfg := vp8lEncodingConfigForMode(ModeBestCompression, img, pixelReaderFor(img), img.Bounds(), img.Rect.Dx(), img.Rect.Dy())
	plan := vp8lEncodingPlan{
		predictor:         true,
		predictorMode:     1,
		predictorSizeBits: vp8lDefaultPredictorSizeBits,
		predictorAnalysis: vp8lPredictorImageAnalysis(1),
	}
	breakdown := vp8lPayloadBitBreakdown{
		predictorHeader:     6,
		predictorImageData:  64,
		transformTerminator: 1,
		mainImageData:       vp8lMinTransformedLZ77CacheBits,
	}
	if !vp8lShouldTryLZ77ColorCacheConfig(plan, 64, 64, vp8lMinTransformedLZ77CacheBits, vp8lMinTransformedLZ77CacheBits, vp8lMaxTransformedLZ77CacheTokens+1, cfg) {
		t.Fatal("ModeBestCompression did not relax transformed LZ77 color cache token guard")
	}
	if !vp8lShouldTryTransformedLZ77ColorCache(plan, breakdown, vp8lMinTransformedLZ77CacheBits, vp8lMinTransformedLZ77CacheBits) {
		t.Fatal("test setup no longer satisfies transformed LZ77 color cache cost guard")
	}
}

func TestVP8LTransformedLZ77ColorCacheMinSavings(t *testing.T) {
	tests := []struct {
		name     string
		lz77Bits uint64
		want     uint64
	}{
		{name: "fixed floor", lz77Bits: 8192, want: 1024},
		{name: "relative floor", lz77Bits: 20000, want: 2000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vp8lTransformedLZ77ColorCacheMinSavings(tt.lz77Bits); got != tt.want {
				t.Fatalf("vp8lTransformedLZ77ColorCacheMinSavings = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestVP8LShouldTryColorTransformAfterTransform(t *testing.T) {
	improvesRed := image.NewNRGBA(image.Rect(0, 0, 64, 1))
	for x := 0; x < improvesRed.Rect.Dx(); x++ {
		g := uint8((x*73 + x*x*19 + 11) & 0xff)
		improvesRed.SetNRGBA(x, 0, color.NRGBA{
			R: g + 7,
			G: g,
			B: uint8((x*97 + x*x*13 + 53) & 0xff),
			A: 255,
		})
	}
	if !vp8lShouldTryColorTransformAfterTransform(pixelReaderFor(improvesRed), improvesRed.Bounds(), improvesRed.Rect.Dx(), vp8lColorTransformElement{greenToRed: 32}) {
		t.Fatal("green-to-red color transform guard rejected a clear residual reduction")
	}

	noisy := image.NewNRGBA(image.Rect(0, 0, 64, 1))
	for x := 0; x < noisy.Rect.Dx(); x++ {
		noisy.SetNRGBA(x, 0, color.NRGBA{
			R: uint8((x*83 + x*x*17 + 29) & 0xff),
			G: uint8((x*41 + x*x*31 + 7) & 0xff),
			B: uint8((x*97 + x*x*13 + 53) & 0xff),
			A: 255,
		})
	}
	if vp8lShouldTryColorTransformAfterTransform(pixelReaderFor(noisy), noisy.Bounds(), noisy.Rect.Dx(), vp8lColorTransformElement{greenToRed: 32}) {
		t.Fatal("green-to-red color transform guard accepted a noisy residual")
	}
}

func TestVP8LBestHashMatchUsesExpandedCandidateWindow(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 1))
	for x := 0; x < img.Rect.Dx(); x++ {
		img.SetNRGBA(x, 0, color.NRGBA{
			R: uint8(120 + x),
			G: uint8(31 * x),
			B: uint8(17 * x),
			A: 255,
		})
	}
	long := []color.NRGBA{
		{R: 1, G: 10, B: 20, A: 255},
		{R: 2, G: 11, B: 21, A: 255},
		{R: 3, G: 12, B: 22, A: 255},
		{R: 4, G: 13, B: 23, A: 255},
		{R: 5, G: 14, B: 24, A: 255},
		{R: 6, G: 15, B: 25, A: 255},
		{R: 7, G: 16, B: 26, A: 255},
		{R: 8, G: 17, B: 27, A: 255},
	}
	for i, c := range long {
		img.SetNRGBA(i, 0, c)
		img.SetNRGBA(48+i, 0, c)
	}
	for _, x := range []int{8, 16, 24, 32, 40} {
		for i := 0; i < vp8lMinBackwardRefLength; i++ {
			img.SetNRGBA(x+i, 0, long[i])
		}
		img.SetNRGBA(x+vp8lMinBackwardRefLength, 0, color.NRGBA{
			R: uint8(200 + x),
			G: uint8(201 + x),
			B: uint8(202 + x),
			A: 255,
		})
	}

	readPixel := pixelReaderFor(img)
	candidates := vp8lHashCandidateList{40, 32, 24, 16, 8, 0, -1, -1}
	firstWindow := vp8lBestHashMatch(
		candidates,
		vp8lMinHashCandidates,
		readPixel,
		img.Bounds(),
		img.Rect.Dx(),
		48,
		img.Rect.Dx()*img.Rect.Dy(),
	)
	expandedWindow := vp8lBestHashMatch(
		candidates,
		vp8lMaxHashCandidates,
		readPixel,
		img.Bounds(),
		img.Rect.Dx(),
		48,
		img.Rect.Dx()*img.Rect.Dy(),
	)
	if firstWindow.length >= len(long) {
		t.Fatalf("first window match length = %d, want less than %d", firstWindow.length, len(long))
	}
	if expandedWindow.length != len(long) {
		t.Fatalf("expanded window match length = %d, want %d", expandedWindow.length, len(long))
	}
	if expandedWindow.distance != 48 {
		t.Fatalf("expanded window distance = %d, want 48", expandedWindow.distance)
	}
}

func TestVP8LLazyMatchingChoosesLongerNextMatch(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 48, 1))
	a := color.NRGBA{R: 1, G: 10, B: 20, A: 255}
	b := color.NRGBA{R: 2, G: 11, B: 21, A: 255}
	c := color.NRGBA{R: 3, G: 12, B: 22, A: 255}
	d := color.NRGBA{R: 4, G: 13, B: 23, A: 255}
	q := color.NRGBA{R: 250, G: 1, B: 2, A: 255}
	long := []color.NRGBA{
		b,
		c,
		d,
		{R: 5, G: 14, B: 24, A: 255},
		{R: 6, G: 15, B: 25, A: 255},
		{R: 7, G: 16, B: 26, A: 255},
		{R: 8, G: 17, B: 27, A: 255},
		{R: 9, G: 18, B: 28, A: 255},
	}
	for x := 0; x < img.Rect.Dx(); x++ {
		img.SetNRGBA(x, 0, color.NRGBA{R: uint8(100 + x), G: uint8(80 + x), B: uint8(60 + x), A: 255})
	}
	img.SetNRGBA(0, 0, a)
	img.SetNRGBA(1, 0, b)
	img.SetNRGBA(2, 0, c)
	img.SetNRGBA(3, 0, d)
	img.SetNRGBA(4, 0, q)
	for i, pixel := range long {
		img.SetNRGBA(8+i, 0, pixel)
	}
	img.SetNRGBA(24, 0, a)
	for i, pixel := range long {
		img.SetNRGBA(25+i, 0, pixel)
	}

	tokens, _, _, _, _, _ := vp8lBuildLZ77(pixelReaderFor(img), img.Bounds(), img.Rect.Dx(), true, 0)
	firstCopy := -1
	for i, token := range tokens {
		if token.copyLength > 0 {
			firstCopy = i
			break
		}
	}
	if firstCopy < 1 {
		t.Fatalf("first copy index = %d, want at least 1", firstCopy)
	}
	if tokens[firstCopy].copyLength != len(long) {
		t.Fatalf("first copy length = %d, want %d", tokens[firstCopy].copyLength, len(long))
	}
	if tokens[firstCopy-1].copyLength != 0 || tokens[firstCopy-1].pixel != a {
		t.Fatalf("token before first copy = %#v, want lazy literal %#v", tokens[firstCopy-1], a)
	}
}

func TestVP8LShouldTryLZ77(t *testing.T) {
	tiny := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	if vp8lShouldTryLZ77(pixelReaderFor(tiny), tiny.Bounds(), tiny.Rect.Dx()) {
		t.Fatal("tiny image should not try LZ77")
	}

	repeated := image.NewNRGBA(image.Rect(0, 0, 32, 2))
	for y := 0; y < repeated.Rect.Dy(); y++ {
		for x := 0; x < repeated.Rect.Dx(); x++ {
			id := x % 8
			repeated.SetNRGBA(x, y, color.NRGBA{
				R: uint8(id),
				G: uint8(id * 17),
				B: uint8(id * 31),
				A: 255,
			})
		}
	}
	if !vp8lShouldTryLZ77(pixelReaderFor(repeated), repeated.Bounds(), repeated.Rect.Dx()) {
		t.Fatal("repeated image should try LZ77")
	}
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
		{quality: 90, want: 31},
		{quality: 75, want: 48},
		{quality: 50, want: 85},
		{quality: 1, want: 127},
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
	if fast.tryY4 {
		t.Fatal("ModeFast enabled Y4 mode search")
	}
	if fast.updateTokenProb {
		t.Fatal("ModeFast enabled token probability update search")
	}

	lossyQuality := vp8LossyConfigForModeQuality(ModeLossyQuality, 75)
	if lossyQuality.tryY4 {
		t.Fatal("ModeLossyQuality enabled Y4 mode search")
	}
	if !lossyQuality.updateTokenProb {
		t.Fatal("ModeLossyQuality disabled token probability updates")
	}
	best := vp8LossyConfigForModeQuality(ModeBestCompression, 75)
	if !best.tryY4 {
		t.Fatal("ModeBestCompression disabled Y4 mode search")
	}
	if low := vp8LossyConfigForModeQuality(ModeLossyQuality, 10); low.rd.yLambda <= lossyQuality.rd.yLambda {
		t.Fatalf("low quality luma lambda = %d, want greater than q75 lambda %d", low.rd.yLambda, lossyQuality.rd.yLambda)
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
	if medium != (1024*1024)/3 {
		t.Fatalf("medium quality capacity = %d, want %d", medium, (1024*1024)/3)
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
	processVP8ChromaMB(nil, readChroma, bounds, mbx, mby, freshCb, freshCr, stride, quant, mode, &freshLeft, &freshUp, nil, nil)
	processVP8ChromaTargetMB(&target, nil, mbx, mby, reuseCb, reuseCr, stride, quant, mode, &reuseLeft, &reuseUp, nil, nil)
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

func TestVP8Y16ModeSelectionChoosesVertical(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 32))
	recY := make([]uint8, 16*32)
	for x := 0; x < 16; x++ {
		v := uint8(32 + x*8)
		recY[15*16+x] = v
		for y := 16; y < 32; y++ {
			img.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: 255})
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
	var zero [16]int
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

	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			v := pred[yy*4+xx]
			img.SetNRGBA(x+xx, y+yy, color.NRGBA{R: v, G: v, B: v, A: 255})
		}
	}

	quant := vp8QuantForIndex(qualityToVP8QIndex(1))
	rd := newVP8RDConfig(quant)
	var target [16]uint8
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			c := img.NRGBAAt(x+xx, y+yy)
			target[yy*4+xx] = rgbToLuma(c.R, c.G, c.B)
		}
	}
	mode, score, nz, _ := chooseVP8Y4Mode(&target, x, y, recY, stride, quant, rd, vp8PredVE, vp8PredVE, 0)
	if mode != vp8PredVE {
		t.Fatalf("Y4 mode = %d, want vertical", mode)
	}
	if nz != 0 {
		t.Fatalf("Y4 vertical nz = %d, want 0", nz)
	}
	var zero [16]int
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

func TestVP8FirstPartitionWritesSelectedY4Modes(t *testing.T) {
	want := [16]uint8{
		vp8PredDC, vp8PredTM, vp8PredVE, vp8PredHE,
		vp8PredRD, vp8PredVR, vp8PredLD, vp8PredVL,
		vp8PredHD, vp8PredHU, vp8PredDC, vp8PredTM,
		vp8PredVE, vp8PredHE, vp8PredRD, vp8PredVR,
	}
	firstPart, err := vp8FirstPartition(1, 1, qualityToVP8QIndex(75), vp8LoopFilterForIndex(qualityToVP8QIndex(75)), []vp8MBMode{{
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

func TestVP8BlockBitCostAccountsForNonZeroCoefficients(t *testing.T) {
	var zero [16]int
	var dc [16]int
	dc[0] = 1
	zeroCost := vp8BlockBitCost(vp8PlaneY1SansY2, 0, zero)
	if got := vp8BlockBitCost(vp8PlaneY1SansY2, 0, dc); got <= zeroCost {
		t.Fatalf("non-zero DC bit cost = %d, want greater than zero block cost %d", got, zeroCost)
	}

	var ac [16]int
	ac[1] = 1
	zeroSkipCost := vp8BlockBitCostFrom(vp8PlaneY1WithY2, 0, zero, 1)
	if got := vp8BlockBitCostFrom(vp8PlaneY1WithY2, 0, ac, 1); got <= zeroSkipCost {
		t.Fatalf("non-zero AC bit cost = %d, want greater than zero skip-first cost %d", got, zeroSkipCost)
	}
}

func TestVP8BlockBitCostDefaultMatchesExplicitDefaultProbs(t *testing.T) {
	coeff := [16]int{
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

	var zero [16]int
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
	var zero [16]int
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
	var zero [16]int
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
	var zero [16]int
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
	var coeff [16]int
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
	var coeff [16]int
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
	firstPart, err := vp8FirstPartition(1, 1, qualityToVP8QIndex(75), vp8LoopFilterForIndex(qualityToVP8QIndex(75)), []vp8MBMode{{
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

func TestLossyAlphaConfigForMode(t *testing.T) {
	fast := lossyAlphaConfigForMode(ModeFast)
	if !fast.filters[alphFilterNone] || fast.filters[alphFilterHorizontal] || fast.filters[alphFilterVertical] || fast.filters[alphFilterGradient] {
		t.Fatalf("ModeFast alpha filters = %#v, want none only", fast.filters)
	}
	if !fast.tryRLE {
		t.Fatal("ModeFast alpha config disabled RLE")
	}
	if fast.trySpatialRef {
		t.Fatal("ModeFast alpha config enabled spatial references")
	}

	lowMemory := lossyAlphaConfigForMode(ModeLowMemory)
	for filter, enabled := range lowMemory.filters {
		if !enabled {
			t.Fatalf("ModeLowMemory alpha filter %d disabled", filter)
		}
	}
	if !lowMemory.tryRLE {
		t.Fatal("ModeLowMemory alpha config disabled RLE")
	}
	if lowMemory.trySpatialRef {
		t.Fatal("ModeLowMemory alpha config enabled spatial references")
	}
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
	var channelLengths [nColorCacheGreenCodes]uint8
	channelLengths[0] = 1
	channelLengths[nLiteralCodes+nLengthCodes-1] = 2
	channelLengths[nColorCacheGreenCodes-1] = 2
	channelCodes := canonicalChannelCodes(channelLengths[:])
	assertCanonicalCodesForTest(t, channelLengths[:], channelCodes[:])
	if channelCodes[0] != 0 {
		t.Fatalf("channel code for first symbol = %b, want 0", channelCodes[0])
	}
	if channelCodes[nLiteralCodes+nLengthCodes-1] != 2 {
		t.Fatalf("channel code for sparse length-2 symbol = %b, want 10", channelCodes[nLiteralCodes+nLengthCodes-1])
	}
	if channelCodes[nColorCacheGreenCodes-1] != 3 {
		t.Fatalf("channel code for high color-cache symbol = %b, want 11", channelCodes[nColorCacheGreenCodes-1])
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

	if plan.distanceCounts[alphaDistanceAbove] == 0 {
		t.Fatal("missing previous-row distance reference")
	}
	if plan.distanceCounts[alphaDistancePrevious] != 0 {
		t.Fatalf("previous-pixel distance references = %d, want 0", plan.distanceCounts[alphaDistancePrevious])
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
	if topLeftPlan.distanceCounts[alphaDistanceTopLeft] == 0 {
		t.Fatal("missing top-left distance reference")
	}

	topRight := []uint8{20, 30, 40, 50, 60, 70, 80, 90, 99}
	var topRightPlan alphaResidualPlan
	topRightPlan.observeLZ77Row(topRight, previous, true)
	topRightPlan.flushRLE()
	if topRightPlan.distanceCounts[alphaDistanceTopRight] == 0 {
		t.Fatal("missing top-right distance reference")
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
	for _, symbol := range []uint8{
		alphaDistanceAbove,
		alphaDistancePrevious,
		alphaDistanceTopLeft,
		alphaDistanceTopRight,
	} {
		if code.distanceLengths[symbol] == 0 {
			t.Fatalf("distance symbol %d has zero code length", symbol)
		}
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
		analysis := analyzeLossyAlpha(readPixel, bounds, width, height)
		candidates := appendAlphaPayloadCandidates(nil, analysis)
		if len(candidates) == 0 {
			t.Fatal("no alpha payload candidates")
		}
		for _, candidate := range candidates {
			stream, err := encodeAlphaVP8LStream(readPixel, bounds, width, height, candidate.filter, candidate.code)
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
	segmentation := r.readBit(128)
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
	if segmentation {
		t.Fatal("VP8 segmentation is enabled, want disabled")
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
	r.readBit(128)     // segmentation
	r.readBit(128)     // loop filter type
	r.readUint(128, 6) // loop filter level
	r.readUint(128, 3) // sharpness
	readVP8LoopFilterDeltas(t, r)
	r.readUint(128, 2) // token partitions
	r.readUint(128, 7) // base quantizer
	for i := 0; i < 5; i++ {
		r.readBit(128)
	}
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
			currentWidth = divRoundUp(currentWidth, 1<<widthBits)
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
			prefixBits = uint8(rawPrefixBits) + vp8lMinMetaPrefixBits
			prefixImageWidth, prefixImageHeight := vp8lMetaPrefixImageDimensions(width, height, prefixBits)
			entropyImage, err = decodeEncoderImageData(r, prefixImageWidth, prefixImageHeight, false)
			if err != nil {
				return nil, err
			}
			maxCode := 0
			for _, pixel := range entropyImage {
				if code := vp8lMetaPrefixCode(pixel); code > maxCode {
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
			code := vp8lMetaPrefixCode(entropyImage[vp8lMetaPrefixIndex(x, y, prefixBits, prefixImageWidth)])
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
	cache[vp8lColorCacheIndex(pixel, bits)] = pixel
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
			pixels[i] = inverseVP8LColorTransform(pixels[i], element)
		}
	}
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
