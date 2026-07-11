package webp

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"math/rand/v2"
	"os"
	"os/exec"
	"testing"
	"unsafe"
)

func TestVP8LLiteralPlanRoundTrip(t *testing.T) {
	img := image.NewNRGBA(image.Rect(11, 17, 18, 22))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			alpha := uint8((x*37 + y*19) & 0xff)
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*31 + y),
				G: uint8(x + y*29),
				B: uint8(x*7 + y*11),
				A: alpha,
			})
		}
	}

	data := encodeLosslessForTest(t, img, ModeDefault)
	assertVP8LRoundTrip(t, data, img)
}

func TestVP8LPreservesTransparentPixelColor(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	pixels := []color.NRGBA{
		{R: 1, G: 2, B: 3, A: 0},
		{R: 254, G: 127, B: 63, A: 0},
		{R: 7, G: 9, B: 11, A: 1},
		{R: 13, G: 17, B: 19, A: 255},
	}
	for x, pixel := range pixels {
		img.SetNRGBA(x, 0, pixel)
	}

	data := encodeLosslessForTest(t, img, ModeDefault)
	assertVP8LRoundTrip(t, data, img)
}

func TestVP8LLiteralPlanPayloadBitsMatchEmission(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageGradient, width: 17, height: 13})
	source := newEncoderSource(img)
	plan, err := searchVP8L(newVP8LSource(source, source.pixels()), vp8lBudgetForMode(ModeDefault))
	if err != nil {
		t.Fatalf("searchVP8L failed: %v", err)
	}

	data := encodeLosslessForTest(t, img, ModeDefault)
	payloadBytes := uint64(binary.LittleEndian.Uint32(data[16:20]))
	if got, want := payloadBytes, (plan.payloadBitLen()+7)/8; got != want {
		t.Fatalf("payload bytes = %d, want %d", got, want)
	}
	counter := vp8lBitCounter()
	plan.writeTo(counter)
	if got, want := counter.bitLen, plan.payloadBitLen(); got != want {
		t.Fatalf("counted bits = %d, want %d", got, want)
	}
}

func TestVP8LScreeningLiteralBitsMatchSerializedPlan(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageGradient, width: 19, height: 13})
	source := newEncoderSource(img)
	pixels, alpha, err := newVP8LSource(source, source.pixels()).materialize(vp8lMaxSourceBytes)
	if err != nil {
		t.Fatal(err)
	}
	candidates := []vp8lTransformCandidate{{width: source.width, height: source.height, pixels: pixels}}
	budget := vp8lBudgetForMode(ModeDefault)
	vp8lForEachTransformCandidate(pixels, source.width, source.height, budget, func(candidate vp8lTransformCandidate) {
		candidates = append(candidates, candidate)
	})
	for i, candidate := range candidates {
		if i > 0 {
			if candidate.materialize == nil {
				t.Fatalf("candidate %d has no materializer", i)
			}
			if rematerialized := candidate.materialize(); !vp8lUint32SlicesEqual(rematerialized, candidate.pixels) {
				t.Fatalf("candidate %d changed when rematerialized", i)
			}
		}
		score := vp8lScoreCandidate(source.width, source.height, alpha, candidate)
		plan := vp8lPlan{
			width:      source.width,
			height:     source.height,
			alpha:      alpha,
			transforms: candidate.transforms,
			image:      buildVP8LLiteralImagePlan(candidate.pixels, candidate.width, candidate.height),
		}
		counter := vp8lBitCounter()
		plan.writeTo(counter)
		if score.literalBits != counter.bitLen {
			t.Fatalf("candidate %d screening bits = %d, serialized plan bits %d", i, score.literalBits, counter.bitLen)
		}
	}
}

func TestVP8LLiteralPlanIsDeterministic(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImagePhotoLike, width: 32, height: 24})
	first := encodeLosslessForTest(t, img, ModeDefault)
	second := encodeLosslessForTest(t, img, ModeDefault)
	if !bytes.Equal(first, second) {
		t.Fatal("VP8L output is not deterministic")
	}
}

func TestVP8LParallelFinalistsMatchSequentialOutput(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageUI, width: 96, height: 64})
	source := newEncoderSource(img)
	newSource := func() vp8lSource {
		return newVP8LSource(source, source.pixels())
	}
	sequentialBudget := vp8lBudgetForMode(ModeDefault)
	sequentialBudget.maxWorkers = 1
	sequential, err := searchVP8L(newSource(), sequentialBudget)
	if err != nil {
		t.Fatal(err)
	}
	parallelBudget := vp8lBudgetForMode(ModeDefault)
	parallelBudget.maxWorkers = 4
	parallelBudget.maxParallelBytes = 1 << 30
	parallel, err := searchVP8L(newSource(), parallelBudget)
	if err != nil {
		t.Fatal(err)
	}
	var sequentialOutput bytes.Buffer
	if err := writeLosslessVP8L(&sequentialOutput, sequential); err != nil {
		t.Fatal(err)
	}
	var parallelOutput bytes.Buffer
	if err := writeLosslessVP8L(&parallelOutput, parallel); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sequentialOutput.Bytes(), parallelOutput.Bytes()) {
		t.Fatalf("parallel output is not deterministic: sequential=%d bytes parallel=%d bytes", sequentialOutput.Len(), parallelOutput.Len())
	}
}

func TestVP8LBestCompressionIsNoLargerThanDefault(t *testing.T) {
	cases := []losslessBenchmarkCase{
		{name: "gradient", kind: benchmarkImageGradient, width: 64, height: 64},
		{name: "ui", kind: benchmarkImageUI, width: 64, height: 64},
		{name: "photo", kind: benchmarkImagePhotoLike, width: 64, height: 64},
		{name: "palette", width: 64, height: 64, format: benchmarkFixturePaletted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img := newLosslessBenchmarkFixtureImage(tc)
			source := newEncoderSource(img)
			vp8lSource := newVP8LSource(source, source.pixels())
			defaultPlan, err := vp8lPlanForMode(vp8lSource, ModeDefault)
			if err != nil {
				t.Fatal(err)
			}
			bestPlan, err := vp8lPlanForMode(vp8lSource, ModeBestCompression)
			if err != nil {
				t.Fatal(err)
			}
			if bestPlan.payloadBitLen() > defaultPlan.payloadBitLen() {
				t.Fatalf("BestCompression bits = %d, Default bits %d", bestPlan.payloadBitLen(), defaultPlan.payloadBitLen())
			}
		})
	}
}

func TestVP8LLiteralPlanRandomSmallImages(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for n := range 32 {
		width := 1 + rng.IntN(12)
		height := 1 + rng.IntN(12)
		img := image.NewNRGBA(image.Rect(-3, 5, -3+width, 5+height))
		for i := range img.Pix {
			img.Pix[i] = uint8(rng.Uint32())
		}
		data := encodeLosslessForTest(t, img, ModeDefault)
		if _, _, _, _, err := decodeEncoderOutput(data); err != nil {
			t.Fatalf("case %d %dx%d decode failed: %v", n, width, height, err)
		}
		assertVP8LRoundTrip(t, data, img)
	}
}

func TestVP8LLiteralPlanImageTypes(t *testing.T) {
	cases := []struct {
		name string
		img  image.Image
	}{
		{name: "NRGBA", img: newLosslessBenchmarkFixtureImage(losslessBenchmarkCase{kind: benchmarkImageGradient, width: 31, height: 23})},
		{name: "RGBA", img: newLosslessBenchmarkFixtureImage(losslessBenchmarkCase{kind: benchmarkImageGradient, width: 31, height: 23, format: benchmarkFixtureRGBA})},
		{name: "Gray", img: newLosslessBenchmarkFixtureImage(losslessBenchmarkCase{kind: benchmarkImageGradient, width: 31, height: 23, format: benchmarkFixtureGray})},
		{name: "Paletted", img: newLosslessBenchmarkFixtureImage(losslessBenchmarkCase{width: 31, height: 23, format: benchmarkFixturePaletted})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := encodeLosslessForTest(t, tc.img, ModeDefault)
			assertVP8LRoundTrip(t, data, tc.img)
		})
	}
}

func TestVP8LLiteralPlanWithDWebP(t *testing.T) {
	if _, err := exec.LookPath("dwebp"); err != nil {
		t.Skip("dwebp is not available")
	}
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageAlpha, width: 32, height: 24})
	data := encodeLosslessForTest(t, img, ModeDefault)
	dir := t.TempDir()
	webpPath := dir + "/vp8l-literal.webp"
	pngPath := dir + "/vp8l-literal.png"
	if err := os.WriteFile(webpPath, data, 0o600); err != nil {
		t.Fatalf("write WebP: %v", err)
	}
	cmd := exec.Command("dwebp", "-quiet", webpPath, "-o", pngPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dwebp failed: %v: %s", err, output)
	}
}

func TestVP8LLiteralPlanPropagatesWriterError(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	err := encodeLossless(failingWriter{}, newEncoderSource(img), ModeDefault)
	if err == nil {
		t.Fatal("encodeLossless succeeded with failing writer")
	}
}

func TestVP8LTransformCandidatesRoundTrip(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			value := uint8((x/4 + y/4*4) * 13)
			img.SetNRGBA(x, y, color.NRGBA{R: value, G: value + uint8(x), B: value + uint8(y), A: 255})
		}
	}
	source := newEncoderSource(img)
	pixels, alpha, err := newVP8LSource(source, source.pixels()).materialize(vp8lMaxSourceBytes)
	if err != nil {
		t.Fatal(err)
	}
	budget := vp8lBudgetForMode(ModeDefault)
	seen := make(map[vp8lTransformKind]bool)
	candidateCount := 0
	vp8lForEachTransformCandidate(pixels, source.width, source.height, budget, func(candidate vp8lTransformCandidate) {
		candidateCount++
		for _, transform := range candidate.transforms {
			seen[transform.kind] = true
		}
		plan := newVP8LPlan(source.width, source.height, alpha, candidate.transforms, candidate.pixels, candidate.width, candidate.height, budget)
		var output bytes.Buffer
		if err := writeLosslessVP8L(&output, plan); err != nil {
			t.Fatalf("candidate %d write failed: %v", candidateCount, err)
		}
		assertVP8LRoundTrip(t, output.Bytes(), img)
	})
	if candidateCount == 0 {
		t.Fatal("no transform candidates")
	}
	for _, kind := range []vp8lTransformKind{
		vp8lTransformPredictor,
		vp8lTransformColor,
		vp8lTransformSubtractGreen,
		vp8lTransformColorIndex,
	} {
		if !seen[kind] {
			t.Fatalf("transform kind %d was not generated", kind)
		}
	}
}

func TestVP8LTokenEngineUsesBackwardReference(t *testing.T) {
	pixels := make([]uint32, 64*8)
	for y := range 8 {
		for x := range 64 {
			value := uint8(x % 8)
			pixels[y*64+x] = 0xff000000 | uint32(value)<<16 | uint32(value*3)<<8 | uint32(value*7)
		}
	}
	plan := buildVP8LImagePlan(pixels, 64, 8, vp8lBudgetForMode(ModeDefault))
	found := false
	for _, token := range plan.tokens {
		found = found || token.kind() == vp8lTokenCopy
	}
	if !found {
		t.Fatal("token engine produced no backward reference")
	}
}

func TestVP8LMatchGraphPropagatesRunLength(t *testing.T) {
	const width = 64
	pixels := make([]uint32, 8192)
	for i := range pixels {
		pixels[i] = 0xff123456
	}
	graph := buildVP8LMatchGraph(pixels, width, vp8lBudgetForMode(ModeDefault))
	distanceCode, ok := vp8lDistanceCodeForPositionDistance(1, width)
	if !ok {
		t.Fatal("distance 1 has no VP8L code")
	}
	for _, position := range []int{1, 2, len(pixels) / 2, len(pixels) - vp8lMinBackwardRefLength} {
		wantLength := minInt(vp8lMaxBackwardRefLength, len(pixels)-position)
		if !vp8lGraphContainsMatch(graph, position, distanceCode, wantLength) {
			t.Fatalf("position %d has no distance-1 match of length %d: %#v", position, wantLength, graph.at(position))
		}
	}
}

func TestVP8LMatchGraphPropagatesPreviousRow(t *testing.T) {
	const width, height = 32, 8
	pixels := make([]uint32, width*height)
	for y := range height {
		for x := range width {
			pixels[y*width+x] = 0xff000000 | uint32(x)<<16 | uint32(x*3)<<8 | uint32(x*7)
		}
	}
	graph := buildVP8LMatchGraph(pixels, width, vp8lBudgetForMode(ModeDefault))
	distanceCode, ok := vp8lDistanceCodeForPositionDistance(width, width)
	if !ok {
		t.Fatalf("distance %d has no VP8L code", width)
	}
	for _, position := range []int{width, width + 7, len(pixels) - vp8lMinBackwardRefLength} {
		wantLength := minInt(vp8lMaxBackwardRefLength, len(pixels)-position)
		if !vp8lGraphContainsMatch(graph, position, distanceCode, wantLength) {
			t.Fatalf("position %d has no previous-row match of length %d: %#v", position, wantLength, graph.at(position))
		}
	}
}

func TestVP8LMatchGraphContainsOnlyValidMatches(t *testing.T) {
	const width, height = 47, 31
	pixels := make([]uint32, width*height)
	state := uint32(1)
	for position := range pixels {
		if position >= width && position%11 != 0 {
			pixels[position] = pixels[position-width]
			continue
		}
		state = state*1664525 + 1013904223
		pixels[position] = 0xff000000 | state&0x00ffffff
	}
	graph := buildVP8LMatchGraph(pixels, width, vp8lBudgetForMode(ModeBestCompression))
	for position := range pixels {
		for _, match := range graph.at(position) {
			distance := vp8lPositionDistance(int(match.distanceCode), width)
			if distance <= 0 || distance > position {
				t.Fatalf("position %d has invalid distance code %d", position, match.distanceCode)
			}
			if got := vp8lMatchLength(pixels, position-distance, position); got < int(match.length) {
				t.Fatalf("position %d distance %d length = %d, actual match length %d", position, distance, match.length, got)
			}
		}
	}
}

func TestVP8LTokenEngineUsesColorCache(t *testing.T) {
	const width = 1024
	pixels := make([]uint32, width)
	state := uint32(1)
	for i := range pixels {
		state = state*1664525 + 1013904223
		value := state >> 28
		pixels[i] = 0xff000000 | value<<16 | (value*5&0xff)<<8 | value*11&0xff
	}
	budget := vp8lBudgetForMode(ModeDefault)
	plan := buildVP8LImagePlan(pixels, width, 1, budget)
	if plan.cacheBits == 0 {
		t.Fatal("token engine did not select a color cache")
	}
	found := false
	for _, token := range plan.tokens {
		found = found || token.kind() == vp8lTokenCache
	}
	if !found {
		t.Fatal("color-cache plan has no cache token")
	}
}

func TestVP8LLiteralCacheSeedBitsMatchPlan(t *testing.T) {
	const cacheBits = 5
	pixels := make([]uint32, 2048)
	state := uint32(7)
	for i := range pixels {
		state = state*1664525 + 1013904223
		value := state >> 27
		pixels[i] = 0xff000000 | value<<16 | (value*5&0xff)<<8 | value*11&0xff
	}
	hits := vp8lColorCacheHits(pixels, cacheBits)
	group, dataBits := vp8lLiteralCacheGroupAndDataBits(pixels, hits, cacheBits)
	counter := vp8lBitCounter()
	counter.writeBits(1, 1)
	counter.writeBits(uint32(cacheBits), 4)
	counter.writeBits(0, 1)
	group.writeHeaders(counter)
	plan := vp8lImagePlan{
		width:     len(pixels),
		height:    1,
		cacheBits: cacheBits,
		tokens:    vp8lLiteralCacheTokens(pixels, hits),
		group:     group,
	}
	if got, want := counter.bitLen+dataBits, plan.bitLen(true); got != want {
		t.Fatalf("literal/cache seed bits = %d, serialized plan bits %d", got, want)
	}
}

func TestVP8LCacheAppliedSeedBitsMatchPlan(t *testing.T) {
	const width, height, cacheBits = 64, 16, 5
	pixels := make([]uint32, width*height)
	state := uint32(11)
	for i := range pixels {
		if i >= width && i%9 != 0 {
			pixels[i] = pixels[i-width]
			continue
		}
		state = state*1664525 + 1013904223
		value := state >> 27
		pixels[i] = 0xff000000 | value<<16 | (value*5&0xff)<<8 | value*11&0xff
	}
	budget := vp8lBudgetForMode(ModeDefault)
	graph := buildVP8LMatchGraph(pixels, width, budget)
	seed := vp8lGreedyTokens(pixels, graph)
	hits := vp8lColorCacheHits(pixels, cacheBits)
	group, dataBits := vp8lCacheAppliedGroupAndDataBits(seed, hits, cacheBits)
	counter := vp8lBitCounter()
	counter.writeBits(1, 1)
	counter.writeBits(cacheBits, 4)
	counter.writeBits(0, 1)
	group.writeHeaders(counter)
	plan := vp8lImagePlan{
		width:     width,
		height:    height,
		cacheBits: cacheBits,
		tokens:    vp8lApplyCacheToTokens(seed, hits),
		group:     group,
	}
	if got, want := counter.bitLen+dataBits, plan.bitLen(true); got != want {
		t.Fatalf("cache-applied seed bits = %d, serialized plan bits %d", got, want)
	}
}

func TestVP8LTokenUsesCompactRepresentation(t *testing.T) {
	if got, want := unsafe.Sizeof(vp8lToken(0)), uintptr(8); got != want {
		t.Fatalf("vp8lToken size = %d, want %d", got, want)
	}
	if got, want := unsafe.Sizeof(vp8lMatch{}), uintptr(8); got != want {
		t.Fatalf("vp8lMatch size = %d, want %d", got, want)
	}
	copyToken := vp8lCopyToken(vp8lMaxBackwardRefLength, vp8lMaxDistanceCode)
	if copyToken.kind() != vp8lTokenCopy || copyToken.copyLength() != vp8lMaxBackwardRefLength || copyToken.distanceCode() != vp8lMaxDistanceCode {
		t.Fatalf("copy token round-trip = kind %d length %d distance %d", copyToken.kind(), copyToken.copyLength(), copyToken.distanceCode())
	}
}

func TestVP8LSpatialEntropyPlanReducesExactBits(t *testing.T) {
	const width, height = 128, 64
	pixels := make([]uint32, width*height)
	state := uint32(7)
	for y := range height {
		for x := range width {
			if x < width/2 {
				value := uint32((x + y) & 1)
				pixels[y*width+x] = 0xff000000 | value<<16 | value<<8 | value
				continue
			}
			state = state*1664525 + 1013904223
			pixels[y*width+x] = 0xff000000 | state&0x00ffffff
		}
	}
	base := buildVP8LLiteralImagePlan(pixels, width, height)
	budget := vp8lBudgetForMode(ModeBestCompression)
	got := vp8lChooseEntropyPlan(base, budget)
	if got.meta == nil {
		t.Fatal("spatial entropy search selected no meta-prefix image")
	}
	if got.bitLen(true) >= base.bitLen(true) {
		t.Fatalf("meta-prefix bits = %d, want less than single-group %d", got.bitLen(true), base.bitLen(true))
	}
	tiles, _, _ := vp8lTileHistograms(base, got.meta.prefixBits)
	groupCosts, greenSize := vp8lBuildCodeGroupCosts(got.meta.groups, got.cacheBits, nil)
	estimatedBits := vp8lEntropyCandidateBitLen(got.cacheBits, got.meta, tiles, vp8lTokenExtraBits(got.tokens), groupCosts, greenSize)
	if estimatedBits != got.bitLen(true) {
		t.Fatalf("histogram bit count = %d, serialized bit count %d", estimatedBits, got.bitLen(true))
	}
	plan := &vp8lPlan{width: width, height: height, image: got}
	counter := vp8lBitCounter()
	plan.writeTo(counter)
	plan.payloadBits = counter.bitLen
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i, pixel := range pixels {
		img.SetNRGBA(i%width, i/width, vp8lUnpackPixel(pixel))
	}
	var output bytes.Buffer
	if err := writeLosslessVP8L(&output, plan); err != nil {
		t.Fatal(err)
	}
	assertVP8LRoundTrip(t, output.Bytes(), img)
}

func FuzzVP8LLiteralPlanRoundTrip(f *testing.F) {
	f.Add(uint8(1), uint8(1), []byte{1, 2, 3, 4})
	f.Add(uint8(3), uint8(2), []byte{
		1, 2, 3, 0, 4, 5, 6, 255, 7, 8, 9, 128,
		10, 11, 12, 64, 13, 14, 15, 1, 16, 17, 18, 254,
	})
	f.Fuzz(func(t *testing.T, rawWidth uint8, rawHeight uint8, pixels []byte) {
		width := int(rawWidth%8) + 1
		height := int(rawHeight%8) + 1
		if len(pixels) < width*height*4 {
			t.Skip()
		}
		img := image.NewNRGBA(image.Rect(-2, 3, -2+width, 3+height))
		for y := range height {
			copy(img.Pix[y*img.Stride:y*img.Stride+width*4], pixels[y*width*4:(y+1)*width*4])
		}
		data := encodeLosslessForTest(t, img, ModeDefault)
		assertVP8LRoundTrip(t, data, img)
	})
}

func BenchmarkVP8LMatchGraphRunLength(b *testing.B) {
	const width, height = 256, 256
	pixels := make([]uint32, width*height)
	for i := range pixels {
		pixels[i] = 0xff123456
	}
	budget := vp8lBudgetForMode(ModeDefault)
	b.ReportAllocs()
	for b.Loop() {
		graph := buildVP8LMatchGraph(pixels, width, budget)
		if len(graph.edges) == 0 {
			b.Fatal("match graph is empty")
		}
	}
}

func vp8lGraphContainsMatch(graph vp8lMatchGraph, position int, distanceCode int, length int) bool {
	for _, match := range graph.at(position) {
		if int(match.distanceCode) == distanceCode && int(match.length) == length {
			return true
		}
	}
	return false
}

func vp8lPositionDistance(distanceCode int, width int) int {
	if distanceCode <= 0 {
		return 0
	}
	if distanceCode > len(vp8lDistanceMap) {
		return distanceCode - len(vp8lDistanceMap)
	}
	offset := vp8lDistanceMap[distanceCode-1]
	return offset.x + offset.y*width
}

func encodeLosslessForTest(t *testing.T, img image.Image, mode Mode) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := encodeLossless(&output, newEncoderSource(img), mode); err != nil {
		t.Fatalf("encodeLossless failed: %v", err)
	}
	return output.Bytes()
}

func assertVP8LRoundTrip(t *testing.T, data []byte, img image.Image) {
	t.Helper()
	got, width, height, _, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	bounds := img.Bounds()
	if width != bounds.Dx() || height != bounds.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, bounds.Dx(), bounds.Dy())
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			want := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			index := (y-bounds.Min.Y)*width + x - bounds.Min.X
			if got[index] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[index], want)
			}
		}
	}
}
