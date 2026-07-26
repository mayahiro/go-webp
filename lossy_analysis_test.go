package webp

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"testing"
)

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
