package webp

import (
	"bufio"
	"bytes"
	"image"
	"image/color"
	"testing"
)

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
