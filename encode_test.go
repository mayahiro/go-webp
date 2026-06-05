package webp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
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
		{quality: 90, want: 7},
		{quality: 75, want: 20},
		{quality: 50, want: 48},
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

func TestVP8ResidualPartitionCapacityTracksQualityAndBounds(t *testing.T) {
	if got := vp8ResidualPartitionCapacity(8, 8, qualityToVP8QIndex(75)); got != 1024 {
		t.Fatalf("small image capacity = %d, want 1024", got)
	}
	if got := vp8ResidualPartitionCapacity(1024, 1024, qualityToVP8QIndex(100)); got != 1<<20 {
		t.Fatalf("high quality capacity = %d, want %d", got, 1<<20)
	}
	medium := vp8ResidualPartitionCapacity(1024, 1024, qualityToVP8QIndex(75))
	low := vp8ResidualPartitionCapacity(1024, 1024, qualityToVP8QIndex(25))
	if medium != 1<<19 {
		t.Fatalf("medium quality capacity = %d, want %d", medium, 1<<19)
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
	}}, vp8DefaultTokenProbs)
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

func TestVP8FirstPartitionWritesTokenProbUpdate(t *testing.T) {
	probs := vp8DefaultTokenProbs
	probs[vp8PlaneY1SansY2][1][0][0] = 17
	firstPart, err := vp8FirstPartition(1, 1, qualityToVP8QIndex(75), vp8LoopFilterForIndex(qualityToVP8QIndex(75)), []vp8MBMode{{
		useY16: true,
		yMode:  vp8PredDC,
		cMode:  vp8PredDC,
	}}, probs)
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
	for _, token := range tokens {
		wantTokenBits += uint64(alphaCodeLengthCodeLengths[token.symbol] + token.extraBits)
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

func expandAlphaCodeLengthTokensForTest(tokens []alphaCodeLengthToken, n int) []uint8 {
	out := make([]uint8, 0, n)
	for _, token := range tokens {
		switch token.symbol {
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

type decodedTree struct {
	constant bool
	symbol   uint8
}

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
	if v, err := r.read(1); err != nil || v != 0 {
		return nil, 0, 0, false, errors.New("unexpected transform")
	}
	if v, err := r.read(1); err != nil || v != 0 {
		return nil, 0, 0, false, errors.New("unexpected color cache")
	}
	if v, err := r.read(1); err != nil || v != 0 {
		return nil, 0, 0, false, errors.New("unexpected meta prefix image")
	}

	green, err := decodeEncoderTree(&r, nLiteralCodes+nLengthCodes)
	if err != nil {
		return nil, 0, 0, false, err
	}
	red, err := decodeEncoderTree(&r, nLiteralCodes)
	if err != nil {
		return nil, 0, 0, false, err
	}
	blue, err := decodeEncoderTree(&r, nLiteralCodes)
	if err != nil {
		return nil, 0, 0, false, err
	}
	alpha, err := decodeEncoderTree(&r, nLiteralCodes)
	if err != nil {
		return nil, 0, 0, false, err
	}
	distance, err := decodeEncoderTree(&r, nDistanceCodes)
	if err != nil {
		return nil, 0, 0, false, err
	}
	if !distance.constant || distance.symbol != 0 {
		return nil, 0, 0, false, errors.New("unexpected distance tree")
	}

	width, height := int(widthMinusOne+1), int(heightMinusOne+1)
	pixels := make([]color.NRGBA, width*height)
	for i := range pixels {
		g, err := decodeEncoderSymbol(&r, green)
		if err != nil {
			return nil, 0, 0, false, err
		}
		rr, err := decodeEncoderSymbol(&r, red)
		if err != nil {
			return nil, 0, 0, false, err
		}
		b, err := decodeEncoderSymbol(&r, blue)
		if err != nil {
			return nil, 0, 0, false, err
		}
		a, err := decodeEncoderSymbol(&r, alpha)
		if err != nil {
			return nil, 0, 0, false, err
		}
		pixels[i] = color.NRGBA{R: rr, G: g, B: b, A: a}
	}

	return pixels, width, height, alphaHint != 0, nil
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
		if nSymbols != 0 {
			return decodedTree{}, errors.New("unexpected two-symbol tree")
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
		return decodedTree{constant: true, symbol: uint8(symbol)}, nil
	}

	nCodes, err := r.read(4)
	if err != nil {
		return decodedTree{}, err
	}
	if nCodes != 8 {
		return decodedTree{}, errors.New("unexpected code length code count")
	}
	for _, want := range full8CodeLengthCodeLengths {
		got, err := r.read(3)
		if err != nil {
			return decodedTree{}, err
		}
		if got != uint32(want) {
			return decodedTree{}, errors.New("unexpected code length code")
		}
	}
	useLength, err := r.read(1)
	if err != nil {
		return decodedTree{}, err
	}
	if useLength != 0 {
		return decodedTree{}, errors.New("unexpected max symbol limit")
	}
	for symbol := 0; symbol < alphabetSize; symbol++ {
		got, err := r.read(1)
		if err != nil {
			return decodedTree{}, err
		}
		want := uint32(1)
		if symbol >= nLiteralCodes {
			want = 0
		}
		if got != want {
			return decodedTree{}, errors.New("unexpected code length")
		}
	}
	return decodedTree{}, nil
}

func decodeEncoderSymbol(r *testBitReader, tree decodedTree) (uint8, error) {
	if tree.constant {
		return tree.symbol, nil
	}
	v, err := r.read(8)
	if err != nil {
		return 0, err
	}
	return reverse8(uint8(v)), nil
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
