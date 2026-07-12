package webp

import (
	"bytes"
	"fmt"
	"image"
	"math"
	"testing"
	"unsafe"
)

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

func BenchmarkEncodeLossyAlphaNeighborhood512(b *testing.B) {
	benchmarkEncodeLossyCase(b, lossyBenchmarkCase{
		name:    "AlphaNeighborhood512Q75",
		kind:    benchmarkImageAlphaNeighborhood,
		width:   512,
		height:  512,
		quality: 75,
	})
}

func BenchmarkEncodeLossyYCbCr512(b *testing.B) {
	img := newBenchmarkYCbCrFixtureImage(512, 512)
	benchmarkEncodeLossyImage(b, img, 75, len(img.Y)+len(img.Cb)+len(img.Cr))
}

func BenchmarkEncodeLossyYCbCrFallback512(b *testing.B) {
	img := newBenchmarkYCbCrFixtureImage(512, 512)
	benchmarkEncodeLossyImage(b, benchmarkImageWrapper{Image: img}, 75, len(img.Y)+len(img.Cb)+len(img.Cr))
}

func BenchmarkEncodeLossyPaletted512(b *testing.B) {
	img := newBenchmarkPalettedFixtureImage(512, 512)
	benchmarkEncodeLossyImage(b, img, 75, len(img.Pix)+len(img.Palette)*4)
}

func BenchmarkEncodeLossyPalettedFallback512(b *testing.B) {
	img := newBenchmarkPalettedFixtureImage(512, 512)
	benchmarkEncodeLossyImage(b, benchmarkImageWrapper{Image: img}, 75, len(img.Pix)+len(img.Palette)*4)
}

func BenchmarkEncodeLosslessGradient128(b *testing.B) {
	benchmarkEncodeLosslessCase(b, losslessBenchmarkCase{
		name:   "Gradient128",
		kind:   benchmarkImageGradient,
		width:  128,
		height: 128,
	})
}

func BenchmarkEncodeLosslessPhotoLike512(b *testing.B) {
	benchmarkEncodeLosslessCase(b, losslessBenchmarkCase{
		name:   "PhotoLike512",
		kind:   benchmarkImagePhotoLike,
		width:  512,
		height: 512,
	})
}

func BenchmarkEncodeLosslessPaletted512(b *testing.B) {
	benchmarkEncodeLosslessCase(b, losslessBenchmarkCase{
		name:   "Palette512",
		width:  512,
		height: 512,
		format: benchmarkFixturePaletted,
	})
}

func BenchmarkEncodeLosslessEntropyRegions512(b *testing.B) {
	benchmarkEncodeLosslessCase(b, losslessBenchmarkCase{
		name:   "EntropyRegions512",
		kind:   benchmarkImageEntropyRegions,
		width:  512,
		height: 512,
	})
}

func benchmarkEncodeLossyImage(b *testing.B, img image.Image, quality int, inputBytes int) {
	opts := &Options{Compression: CompressionLossy, Quality: quality}
	encoded := encodeBenchmarkWebP(b, img, opts)
	b.SetBytes(int64(inputBytes))
	b.ReportAllocs()
	var buf bytes.Buffer
	buf.Grow(len(encoded))
	b.ResetTimer()
	for b.Loop() {
		buf.Reset()
		if err := Encode(&buf, img, opts); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(encoded)), "encoded_B")
	b.ReportMetric(float64(len(encoded))/float64(inputBytes), "encoded_per_input")
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

func BenchmarkEncodeLossyProfiles(b *testing.B) {
	fixtures := []struct {
		name string
		img  image.Image
	}{
		{name: "PhotoLike512", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImagePhotoLike, width: 512, height: 512})},
		{name: "UI512", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageUI, width: 512, height: 512})},
		{name: "YCbCr512", img: newBenchmarkYCbCrFixtureImage(512, 512)},
		{name: "AlphaBands512", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageAlphaBands, width: 512, height: 512})},
	}
	modes := []struct {
		name string
		mode Mode
	}{
		{name: "Default", mode: ModeDefault},
		{name: "BestCompression", mode: ModeBestCompression},
		{name: "LowMemory", mode: ModeLowMemory},
	}
	for _, fixture := range fixtures {
		for _, mode := range modes {
			b.Run(fixture.name+"/"+mode.name, func(b *testing.B) {
				benchmarkEncodeModeProfileImage(b, fixture.img, &Options{
					Compression: CompressionLossy,
					Quality:     75,
					Mode:        mode.mode,
				})
			})
		}
	}
}

func BenchmarkEncodeLossyParallelAlpha(b *testing.B) {
	for _, size := range []int{64, 128, 512} {
		img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageAlphaBands, width: size, height: size})
		for _, parallel := range []bool{false, true} {
			b.Run(fmt.Sprintf("%d/Parallel%t", size, parallel), func(b *testing.B) {
				cfg := vp8LossyConfigForModeQuality(ModeDefault, 75)
				cfg.parallelAlpha = parallel
				benchmarkEncodeLossyConfigImage(b, img, cfg)
			})
		}
	}
}

func BenchmarkEncodeLossyY4FlatnessFullSearch(b *testing.B) {
	fixtures := []struct {
		name string
		img  image.Image
	}{
		{name: "Flat512", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageFlat, width: 512, height: 512})},
		{name: "UI512", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageUI, width: 512, height: 512})},
		{name: "Gradient512", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageGradient, width: 512, height: 512})},
		{name: "PhotoLike512", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImagePhotoLike, width: 512, height: 512})},
	}
	for _, fixture := range fixtures {
		for _, limit := range []int{0, 1} {
			b.Run(fmt.Sprintf("%s/Limit%d", fixture.name, limit), func(b *testing.B) {
				cfg := vp8LossyConfigForModeQuality(ModeDefault, 75)
				cfg.y4FlatnessLimit = limit
				benchmarkEncodeLossyConfigImage(b, fixture.img, cfg)
			})
		}
	}
}

func BenchmarkEncodeLossyResidualCommit(b *testing.B) {
	fixtures := []struct {
		name string
		img  image.Image
	}{
		{name: "PhotoLike512", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImagePhotoLike, width: 512, height: 512})},
		{name: "UI512", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageUI, width: 512, height: 512})},
		{name: "YCbCr512", img: newBenchmarkYCbCrFixtureImage(512, 512)},
	}
	for _, fixture := range fixtures {
		for _, mode := range []struct {
			name string
			mode Mode
		}{
			{name: "Default", mode: ModeDefault},
			{name: "BestCompression", mode: ModeBestCompression},
		} {
			for _, commit := range []bool{false, true} {
				b.Run(fmt.Sprintf("%s/%s/Commit%t", fixture.name, mode.name, commit), func(b *testing.B) {
					cfg := vp8LossyConfigForModeQuality(mode.mode, 75)
					cfg.commitWinningResiduals = commit
					benchmarkEncodeLossyConfigImage(b, fixture.img, cfg)
				})
			}
		}
	}
}

func BenchmarkEncodeLossyTrellis(b *testing.B) {
	fixtures := []struct {
		name string
		img  image.Image
	}{
		{name: "PhotoLike512", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImagePhotoLike, width: 512, height: 512})},
		{name: "UI512", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageUI, width: 512, height: 512})},
	}
	for _, fixture := range fixtures {
		for _, enabled := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/Enabled%t", fixture.name, enabled), func(b *testing.B) {
				cfg := vp8LossyConfigForModeQuality(ModeBestCompression, 75)
				cfg.defaultFrameIncumbent = false
				cfg.trellis = enabled
				if enabled {
					cfg.trellisPasses = 1
				}
				benchmarkEncodeLossyConfigImage(b, fixture.img, cfg)
			})
		}
	}
}

func BenchmarkEncodeLossyY4RefinementBeam(b *testing.B) {
	fixtures := []struct {
		name string
		img  image.Image
	}{
		{name: "PhotoLike512", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImagePhotoLike, width: 512, height: 512})},
		{name: "UI512", img: newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageUI, width: 512, height: 512})},
	}
	for _, fixture := range fixtures {
		for _, width := range []int{0, 2} {
			b.Run(fmt.Sprintf("%s/Width%d", fixture.name, width), func(b *testing.B) {
				cfg := vp8LossyConfigForModeQuality(ModeBestCompression, 75)
				cfg.defaultFrameIncumbent = false
				cfg.y4RefinementBeamWidth = width
				benchmarkEncodeLossyConfigImage(b, fixture.img, cfg)
			})
		}
	}
}

func BenchmarkEncodeLosslessSmallFixtures(b *testing.B) {
	for _, tc := range losslessBenchmarkSmallCases() {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkEncodeLosslessCase(b, tc)
		})
	}
}

func BenchmarkEncodeLosslessLargeFixtures(b *testing.B) {
	for _, tc := range losslessBenchmarkLargeCases() {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkEncodeLosslessCase(b, tc)
		})
	}
}

func BenchmarkEncodeLosslessHugeFixtures(b *testing.B) {
	for _, tc := range losslessBenchmarkHugeCases() {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkEncodeLosslessCase(b, tc)
		})
	}
}

func BenchmarkEncodeModeProfiles(b *testing.B) {
	cases := []losslessBenchmarkCase{
		{name: "Gradient128", kind: benchmarkImageGradient, width: 128, height: 128},
		{name: "UI256", kind: benchmarkImageUI, width: 256, height: 256},
		{name: "Palette256", width: 256, height: 256, format: benchmarkFixturePaletted},
	}
	modes := []struct {
		name string
		opts *Options
	}{
		{name: "Fast", opts: &Options{Mode: ModeFast}},
		{name: "Balanced", opts: &Options{Mode: ModeBalanced}},
		{name: "BestCompression", opts: &Options{Mode: ModeBestCompression}},
		{name: "LowMemory", opts: &Options{Mode: ModeLowMemory}},
		{name: "Auto", opts: &Options{Mode: ModeAuto}},
		{name: "NearLossless75", opts: &Options{Mode: ModeNearLossless, Quality: 75}},
		{name: "LossyQ75", opts: &Options{Mode: ModeLossyQuality, Quality: 75}},
	}
	for _, tc := range cases {
		img := newLosslessBenchmarkFixtureImage(tc)
		for _, mode := range modes {
			b.Run(tc.name+"/"+mode.name, func(b *testing.B) {
				benchmarkEncodeModeProfileImage(b, img, mode.opts)
			})
		}
	}
}

func BenchmarkEncodeModeLargeProfiles(b *testing.B) {
	cases := []losslessBenchmarkCase{
		{name: "Gradient1024", kind: benchmarkImageGradient, width: 1024, height: 1024},
		{name: "UI1024", kind: benchmarkImageUI, width: 1024, height: 1024},
		{name: "Palette1024", width: 1024, height: 1024, format: benchmarkFixturePaletted},
	}
	modes := []struct {
		name string
		opts *Options
	}{
		{name: "Fast", opts: &Options{Mode: ModeFast}},
		{name: "Balanced", opts: &Options{Mode: ModeBalanced}},
		{name: "LowMemory", opts: &Options{Mode: ModeLowMemory}},
		{name: "Auto", opts: &Options{Mode: ModeAuto}},
	}
	for _, tc := range cases {
		img := newLosslessBenchmarkFixtureImage(tc)
		for _, mode := range modes {
			b.Run(tc.name+"/"+mode.name, func(b *testing.B) {
				benchmarkEncodeModeProfileImage(b, img, mode.opts)
			})
		}
	}
}

func BenchmarkEncodeModeHugeProfiles(b *testing.B) {
	cases := []losslessBenchmarkCase{
		{name: "Gradient4096", kind: benchmarkImageGradient, width: 4096, height: 4096},
		{name: "UI4096", kind: benchmarkImageUI, width: 4096, height: 4096},
		{name: "Palette4096", width: 4096, height: 4096, format: benchmarkFixturePaletted},
	}
	modes := []struct {
		name string
		opts *Options
	}{
		{name: "Fast", opts: &Options{Mode: ModeFast}},
		{name: "LowMemory", opts: &Options{Mode: ModeLowMemory}},
		{name: "Auto", opts: &Options{Mode: ModeAuto}},
	}
	for _, tc := range cases {
		img := newLosslessBenchmarkFixtureImage(tc)
		for _, mode := range modes {
			b.Run(tc.name+"/"+mode.name, func(b *testing.B) {
				benchmarkEncodeModeProfileImage(b, img, mode.opts)
			})
		}
	}
}

func TestEstimateLosslessWorkspaceScalesWithImageSize(t *testing.T) {
	small := estimateLosslessWorkspace(newLosslessBenchmarkFixtureImage(losslessBenchmarkCase{
		name:   "Gradient512",
		kind:   benchmarkImageGradient,
		width:  512,
		height: 512,
	}))
	large := estimateLosslessWorkspace(newLosslessBenchmarkFixtureImage(losslessBenchmarkCase{
		name:   "Gradient4096",
		kind:   benchmarkImageGradient,
		width:  4096,
		height: 4096,
	}))
	if small.totalBytes <= 0 {
		t.Fatalf("small lossless workspace total = %d, want positive", small.totalBytes)
	}
	if large.totalBytes <= small.totalBytes {
		t.Fatalf("large lossless workspace total = %d, want greater than small total %d", large.totalBytes, small.totalBytes)
	}
	if large.workerBytes <= small.workerBytes {
		t.Fatalf("large worker estimate = %d, want greater than small worker estimate %d", large.workerBytes, small.workerBytes)
	}
}

func benchmarkEncodeLossyCase(b *testing.B, tc lossyBenchmarkCase) {
	img := newBenchmarkFixtureImage(tc)
	opts := &Options{Compression: CompressionLossy, Quality: tc.quality}
	inputBytes := img.Bounds().Dx() * img.Bounds().Dy() * 4
	encoded := encodeBenchmarkWebP(b, img, opts)
	yPSNR, uvPSNR := lossyYUVPSNRProxy(img, tc.quality)
	workspace := estimateLossyWorkspace(tc)

	b.SetBytes(int64(inputBytes))
	b.ReportAllocs()

	var buf bytes.Buffer
	buf.Grow(len(encoded))
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
	b.ReportMetric(float64(workspace.recBytes), "rec_workspace_B")
	b.ReportMetric(float64(workspace.modeBytes), "mode_workspace_B")
	b.ReportMetric(float64(workspace.residualBytes), "residual_workspace_B")
	b.ReportMetric(float64(workspace.partitionBytes), "partition_workspace_B")
	b.ReportMetric(float64(workspace.totalBytes), "workspace_est_B")
}

func benchmarkEncodeLosslessCase(b *testing.B, tc losslessBenchmarkCase) {
	img := newLosslessBenchmarkFixtureImage(tc)
	benchmarkEncodeLosslessImage(b, img)
}

func benchmarkEncodeLossyConfigImage(b *testing.B, img image.Image, cfg vp8LossyConfig) {
	inputBytes := benchmarkImageInputBytes(img)
	var encoded bytes.Buffer
	if err := encodeLossyConfig(&encoded, newEncoderSource(img), cfg, lossyAlphaConfigForMode(ModeDefault)); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(inputBytes))
	b.ReportAllocs()

	var buf bytes.Buffer
	buf.Grow(encoded.Len())
	b.ResetTimer()
	for b.Loop() {
		buf.Reset()
		if err := encodeLossyConfig(&buf, newEncoderSource(img), cfg, lossyAlphaConfigForMode(ModeDefault)); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(encoded.Len()), "encoded_B")
	b.ReportMetric(float64(encoded.Len())/float64(inputBytes), "encoded_per_input")
}

func benchmarkEncodeLosslessImage(b *testing.B, img image.Image) {
	opts := &Options{Compression: CompressionLossless}
	inputBytes := benchmarkImageInputBytes(img)
	encoded := encodeBenchmarkWebP(b, img, opts)
	workspace := estimateLosslessWorkspace(img)

	b.SetBytes(int64(inputBytes))
	b.ReportAllocs()

	var buf bytes.Buffer
	buf.Grow(len(encoded))
	b.ResetTimer()
	for b.Loop() {
		buf.Reset()
		if err := Encode(&buf, img, opts); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(encoded)), "encoded_B")
	b.ReportMetric(float64(len(encoded))/float64(inputBytes), "encoded_per_input")
	bounds := img.Bounds()
	b.ReportMetric(float64(bounds.Dx()*bounds.Dy()), "pixels")
	b.ReportMetric(float64(workspace.sourceBytes), "source_est_B")
	b.ReportMetric(float64(workspace.workerBytes), "worker_est_B")
	b.ReportMetric(float64(workspace.searchBytes), "search_est_B")
	b.ReportMetric(float64(workspace.limitBytes), "workspace_limit_B")
	b.ReportMetric(float64(workspace.totalBytes), "workspace_est_B")
}

func benchmarkEncodeModeProfileImage(b *testing.B, img image.Image, opts *Options) {
	inputBytes := benchmarkImageInputBytes(img)
	encoded := encodeBenchmarkWebP(b, img, opts)
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	autoMode := ModeDefault
	autoReason := vp8lAutoLosslessReasonBalanced
	reportAutoMode := false
	var nearLosslessMetrics nearLosslessErrorMetrics
	reportNearLosslessMetrics := false
	if opts != nil && opts.Mode == ModeAuto {
		bounds := img.Bounds()
		autoMode, autoReason = vp8lAutoLosslessProfile(img, pixelReaderFor(img), bounds, bounds.Dx(), bounds.Dy())
		reportAutoMode = true
	}
	if opts != nil && opts.Mode == ModeNearLossless {
		nearLosslessMetrics = estimateNearLosslessError(img, nearLosslessQuality(opts))
		reportNearLosslessMetrics = true
	}

	b.SetBytes(int64(inputBytes))
	b.ReportAllocs()

	var buf bytes.Buffer
	buf.Grow(len(encoded))
	b.ResetTimer()
	for b.Loop() {
		buf.Reset()
		if err := Encode(&buf, img, opts); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(encoded)), "encoded_B")
	b.ReportMetric(float64(len(encoded))/float64(inputBytes), "encoded_per_input")
	b.ReportMetric(float64(width*height), "pixels")
	if modeProfileUsesLossy(opts) {
		workspace := estimateLossyWorkspaceForDimensions(width, height, lossyQuality(opts))
		b.ReportMetric(float64(workspace.totalBytes), "workspace_est_B")
	} else {
		workspace := estimateLosslessWorkspace(img)
		b.ReportMetric(float64(workspace.totalBytes), "workspace_est_B")
	}
	if reportAutoMode {
		b.ReportMetric(float64(autoMode), "auto_mode")
		b.ReportMetric(float64(autoReason), "auto_reason")
	}
	if reportNearLosslessMetrics {
		b.ReportMetric(nearLosslessMetrics.rgbMAE, "rgb_mae")
		b.ReportMetric(float64(nearLosslessMetrics.rgbMaxAbs), "rgb_max_abs")
		b.ReportMetric(float64(nearLosslessMetrics.alphaExact), "alpha_exact")
	}
}

func modeProfileUsesLossy(opts *Options) bool {
	if opts != nil && opts.Mode == ModeLossyQuality {
		return true
	}
	return compression(opts) == CompressionLossy
}

type lossyWorkspaceMetrics struct {
	recBytes       int
	modeBytes      int
	residualBytes  int
	partitionBytes int
	totalBytes     int
}

type losslessWorkspaceMetrics struct {
	sourceBytes uint64
	workerBytes uint64
	searchBytes uint64
	limitBytes  uint64
	totalBytes  uint64
}

func estimateLossyWorkspace(tc lossyBenchmarkCase) lossyWorkspaceMetrics {
	return estimateLossyWorkspaceForDimensions(tc.width, tc.height, tc.quality)
}

func estimateLossyWorkspaceForDimensions(width int, height int, quality int) lossyWorkspaceMetrics {
	mbw := (width + 15) >> 4
	mbh := (height + 15) >> 4
	yStride := mbw * 16
	cStride := mbw * 8
	qIndex := qualityToVP8QIndex(quality)
	recBytes := yStride*minInt(mbh*16, 32) + cStride*minInt(mbh*8, 16)*2
	modeBytes := mbw * mbh * int(unsafe.Sizeof(vp8MBMode{}))
	residualBytes := 0
	if vp8ResidualBufferFits(mbw, mbh) {
		residualBytes = mbw * mbh * (vp8ResidualBlocksPerMacroblock*int(unsafe.Sizeof(vp8ResidualBlock{})) + int(unsafe.Sizeof(vp8ResidualMacroblock{})))
	}
	partitionBytes := vp8FirstPartitionCapacity(mbw, mbh) + vp8ResidualPartitionCapacity(width, height, qIndex)
	return lossyWorkspaceMetrics{
		recBytes:       recBytes,
		modeBytes:      modeBytes,
		residualBytes:  residualBytes,
		partitionBytes: partitionBytes,
		totalBytes:     recBytes + modeBytes + residualBytes + partitionBytes,
	}
}

func estimateLosslessWorkspace(img image.Image) losslessWorkspaceMetrics {
	bounds := img.Bounds()
	pixels := uint64(bounds.Dx()) * uint64(bounds.Dy())
	budget := vp8lBudgetForMode(ModeDefault)
	sourceBytes := pixels * 4
	workerBytes := vp8lWorkerBytes(pixels, budget)
	searchBytes := vp8lBufferedSearchBytes(pixels, budget)

	return losslessWorkspaceMetrics{
		sourceBytes: sourceBytes,
		workerBytes: workerBytes,
		searchBytes: searchBytes,
		limitBytes:  budget.maxWorkspaceBytes,
		totalBytes:  searchBytes,
	}
}

func benchmarkImageInputBytes(img image.Image) int {
	switch img := img.(type) {
	case *image.NRGBA:
		return len(img.Pix)
	case *image.NRGBA64:
		return len(img.Pix)
	case *image.RGBA:
		return len(img.Pix)
	case *image.RGBA64:
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

func lossyYUVPSNRProxy(m image.Image, quality int) (float64, float64) {
	bounds := m.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	mbw := (width + 15) >> 4
	mbh := (height + 15) >> 4
	readPixel := pixelReaderFor(m)
	readLuma := lumaReaderFor(m)
	readChroma := chromaReaderFor(m)
	quant := vp8QuantForIndex(qualityToVP8QIndex(quality))
	work := newVP8EncodeBuffers(mbw, mbh)
	modes := analyzeVP8Modes(readLuma, readChroma, bounds, mbw, mbh, quant, work)

	yStride := mbw * 16
	cStride := mbw * 8
	clear(work.recY)
	for mby := 0; mby < mbh; mby++ {
		for mbx := 0; mbx < mbw; mbx++ {
			mode := modes[mby*mbw+mbx]
			reconstructVP8LumaMB(readLuma, bounds, mbx, mby, work.recY, yStride, quant, mode)
			reconstructVP8ChromaMB(readChroma, bounds, mbx, mby, work.recCb, work.recCr, cStride, quant, mode)
		}
	}

	var yErr float64
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c := samplePixel(readPixel, bounds, x, y)
			got := vp8ReconstructionAt(work.recY, yStride, x, y)
			yErr += squareFloat(float64(int(rgbToLuma(c.R, c.G, c.B)) - int(got)))
		}
	}

	chromaWidth := (width + 1) >> 1
	chromaHeight := (height + 1) >> 1
	var uvErr float64
	for y := 0; y < chromaHeight; y++ {
		for x := 0; x < chromaWidth; x++ {
			wantCb := chromaSample(readChroma, bounds, x*2, y*2, true)
			wantCr := chromaSample(readChroma, bounds, x*2, y*2, false)
			gotCb := vp8ReconstructionAt(work.recCb, cStride, x, y)
			gotCr := vp8ReconstructionAt(work.recCr, cStride, x, y)
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
