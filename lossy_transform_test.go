package webp

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"os"
	"os/exec"
	"testing"
)

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
			block := vp8Source.chromaRGBBlock(readPixel, x, y)
			baselineScores[by*halfWidth+bx] = block.score(cb, cr)
		}
	}
	vp8Source.applySharpChroma(readPixel)
	for by := 0; by < halfHeight; by++ {
		for bx := 0; bx < halfWidth; bx++ {
			x, y := bx*2, by*2
			cb, cr := chromaSamplePair(vp8Source.readChroma, source.bounds, x, y)
			block := vp8Source.chromaRGBBlock(readPixel, x, y)
			gotScore := block.score(cb, cr)
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
