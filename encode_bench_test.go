package webp

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"
	"unsafe"
)

type benchmarkImageKind uint8

const (
	benchmarkImageGradient benchmarkImageKind = iota
	benchmarkImagePhotoLike
	benchmarkImageChecker
	benchmarkImageLineArt
	benchmarkImageUI
	benchmarkImageFlat
	benchmarkImageAlpha
	benchmarkImageAlphaBands
	benchmarkImageAlphaNeighborhood
	benchmarkImageColorEdge
	benchmarkImageEntropyRegions
)

type lossyBenchmarkCase struct {
	name    string
	kind    benchmarkImageKind
	width   int
	height  int
	quality int
}

type benchmarkFixtureFormat uint8

const (
	benchmarkFixtureNRGBA benchmarkFixtureFormat = iota
	benchmarkFixturePaletted
	benchmarkFixtureGray
	benchmarkFixtureRGBA
)

type losslessBenchmarkCase struct {
	name   string
	kind   benchmarkImageKind
	width  int
	height int
	format benchmarkFixtureFormat
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

func BenchmarkEncodeLosslessMetaPrefix512x256(b *testing.B) {
	benchmarkEncodeLosslessImage(b, newMetaPrefixLocalEntropyFixture())
}

func BenchmarkEncodeLosslessMetaPrefixFine128x64(b *testing.B) {
	benchmarkEncodeLosslessImage(b, newMetaPrefixFineLocalEntropyFixture())
}

func BenchmarkEncodeLosslessPredictorMetaPrefix512x256(b *testing.B) {
	benchmarkEncodeLosslessImage(b, newPredictorMetaPrefixFixture())
}

func BenchmarkEncodeLosslessPredictorColorTransform(b *testing.B) {
	benchmarkEncodeLosslessImage(b, newPredictorColorTransformFixture())
}

func BenchmarkEncodeLosslessColorIndexSortedTable(b *testing.B) {
	benchmarkEncodeLosslessImage(b, newColorIndexSortedTableFixture())
}

func BenchmarkEncodeLosslessWideColorCache(b *testing.B) {
	benchmarkEncodeLosslessImage(b, newWideColorCacheFixture())
}

func BenchmarkEncodeLosslessLZ77ColorCache(b *testing.B) {
	benchmarkEncodeLosslessImage(b, newLZ77ColorCacheFixture())
}

func BenchmarkEncodeLosslessPredictorLZ77ColorCache(b *testing.B) {
	benchmarkEncodeLosslessImage(b, newPredictorLZ77ColorCacheFixture())
}

func BenchmarkEncodeLosslessColorTransformLZ77ColorCache(b *testing.B) {
	benchmarkEncodeLosslessImage(b, newColorTransformLZ77ColorCacheFixture())
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

type benchmarkImageWrapper struct {
	image.Image
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

func TestEncodeLosslessBenchmarkFixtures(t *testing.T) {
	for _, tc := range losslessBenchmarkRoundTripCases() {
		t.Run(tc.name, func(t *testing.T) {
			img := newLosslessBenchmarkFixtureImage(tc)
			data := encodeBenchmarkWebP(t, img, &Options{Compression: CompressionLossless})
			assertLosslessBenchmarkWebP(t, data, img)
		})
	}
}

func TestLosslessEntropyRegionsFixtureHasDistinctRegions(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{
		kind:   benchmarkImageEntropyRegions,
		width:  512,
		height: 512,
	})
	regions := []struct {
		name string
		rect image.Rectangle
	}{
		{name: "flat", rect: image.Rect(0, 0, 256, 256)},
		{name: "line", rect: image.Rect(256, 0, 512, 256)},
		{name: "gradient", rect: image.Rect(0, 256, 256, 512)},
		{name: "photo", rect: image.Rect(256, 256, 512, 512)},
	}
	uniqueCounts := make(map[int]string)
	for _, region := range regions {
		unique := make(map[color.NRGBA]struct{})
		for y := region.rect.Min.Y; y < region.rect.Max.Y; y++ {
			for x := region.rect.Min.X; x < region.rect.Max.X; x++ {
				unique[img.NRGBAAt(x, y)] = struct{}{}
			}
		}
		count := len(unique)
		if previous, ok := uniqueCounts[count]; ok {
			t.Fatalf("regions %s and %s have the same unique color count %d", previous, region.name, count)
		}
		uniqueCounts[count] = region.name
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
	if large.tokenUpperBoundBytes <= small.tokenUpperBoundBytes {
		t.Fatalf("large token upper bound = %d, want greater than small token upper bound %d", large.tokenUpperBoundBytes, small.tokenUpperBoundBytes)
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
		{name: "AlphaNeighborhood512Q75", kind: benchmarkImageAlphaNeighborhood, width: 512, height: 512, quality: 75},
		{name: "ColorEdge128Q75", kind: benchmarkImageColorEdge, width: 128, height: 128, quality: 75},
	}
}

func losslessBenchmarkSmallCases() []losslessBenchmarkCase {
	return []losslessBenchmarkCase{
		{name: "Gradient128", kind: benchmarkImageGradient, width: 128, height: 128},
		{name: "PhotoLike256", kind: benchmarkImagePhotoLike, width: 256, height: 256},
		{name: "UI256", kind: benchmarkImageUI, width: 256, height: 256},
		{name: "Flat128", kind: benchmarkImageFlat, width: 128, height: 128},
		{name: "RGBA256", kind: benchmarkImageGradient, width: 256, height: 256, format: benchmarkFixtureRGBA},
		{name: "Gray256", kind: benchmarkImageGradient, width: 256, height: 256, format: benchmarkFixtureGray},
		{name: "Alpha128", kind: benchmarkImageAlpha, width: 128, height: 128},
		{name: "Palette256", width: 256, height: 256, format: benchmarkFixturePaletted},
	}
}

func losslessBenchmarkLargeCases() []losslessBenchmarkCase {
	return []losslessBenchmarkCase{
		{name: "Gradient1024", kind: benchmarkImageGradient, width: 1024, height: 1024},
		{name: "PhotoLike1024", kind: benchmarkImagePhotoLike, width: 1024, height: 1024},
		{name: "UI1024", kind: benchmarkImageUI, width: 1024, height: 1024},
		{name: "Flat1024", kind: benchmarkImageFlat, width: 1024, height: 1024},
		{name: "RGBA1024", kind: benchmarkImageGradient, width: 1024, height: 1024, format: benchmarkFixtureRGBA},
		{name: "Gray1024", kind: benchmarkImageGradient, width: 1024, height: 1024, format: benchmarkFixtureGray},
		{name: "AlphaBands512", kind: benchmarkImageAlphaBands, width: 512, height: 512},
		{name: "EntropyRegions512", kind: benchmarkImageEntropyRegions, width: 512, height: 512},
		{name: "Palette1024", width: 1024, height: 1024, format: benchmarkFixturePaletted},
	}
}

func losslessBenchmarkHugeCases() []losslessBenchmarkCase {
	return []losslessBenchmarkCase{
		{name: "Gradient4096", kind: benchmarkImageGradient, width: 4096, height: 4096},
		{name: "RGBA4096", kind: benchmarkImageGradient, width: 4096, height: 4096, format: benchmarkFixtureRGBA},
		{name: "Gray4096", kind: benchmarkImageGradient, width: 4096, height: 4096, format: benchmarkFixtureGray},
		{name: "UI4096", kind: benchmarkImageUI, width: 4096, height: 4096},
		{name: "Palette4096", width: 4096, height: 4096, format: benchmarkFixturePaletted},
	}
}

func losslessBenchmarkRoundTripCases() []losslessBenchmarkCase {
	cases := append([]losslessBenchmarkCase{}, losslessBenchmarkSmallCases()...)
	cases = append(cases,
		losslessBenchmarkCase{name: "Gradient512", kind: benchmarkImageGradient, width: 512, height: 512},
		losslessBenchmarkCase{name: "RGBA512", kind: benchmarkImageGradient, width: 512, height: 512, format: benchmarkFixtureRGBA},
		losslessBenchmarkCase{name: "Gray512", kind: benchmarkImageGradient, width: 512, height: 512, format: benchmarkFixtureGray},
		losslessBenchmarkCase{name: "AlphaBands512", kind: benchmarkImageAlphaBands, width: 512, height: 512},
		losslessBenchmarkCase{name: "EntropyRegions512", kind: benchmarkImageEntropyRegions, width: 512, height: 512},
		losslessBenchmarkCase{name: "Palette512", width: 512, height: 512, format: benchmarkFixturePaletted},
	)
	return cases
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
	b.ReportMetric(float64(workspace.tokenUpperBoundBytes), "token_upper_est_B")
	b.ReportMetric(float64(workspace.lz77HashBytes), "lz77_hash_est_B")
	b.ReportMetric(float64(workspace.metaPrefixBytes), "meta_prefix_est_B")
	b.ReportMetric(float64(workspace.colorCacheBytes), "color_cache_est_B")
	b.ReportMetric(float64(workspace.planCandidateBytes), "plan_candidate_est_B")
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

type nearLosslessErrorMetrics struct {
	rgbMAE     float64
	rgbMaxAbs  int
	alphaExact int
}

func estimateNearLosslessError(img image.Image, quality int) nearLosslessErrorMetrics {
	bounds := img.Bounds()
	readPixel := pixelReaderFor(img)
	quantized := newNearLosslessReader(newEncoderSource(img), quality)
	var totalAbs int64
	maxAbs := 0
	alphaExact := 1
	count := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			original := readPixel(x, y)
			got := quantized(x, y)
			for _, diff := range []int{
				absInt(int(original.R) - int(got.R)),
				absInt(int(original.G) - int(got.G)),
				absInt(int(original.B) - int(got.B)),
			} {
				totalAbs += int64(diff)
				if diff > maxAbs {
					maxAbs = diff
				}
				count++
			}
			if original.A != got.A {
				alphaExact = 0
			}
		}
	}
	if count == 0 {
		return nearLosslessErrorMetrics{alphaExact: alphaExact}
	}
	return nearLosslessErrorMetrics{
		rgbMAE:     float64(totalAbs) / float64(count),
		rgbMaxAbs:  maxAbs,
		alphaExact: alphaExact,
	}
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

func assertLosslessBenchmarkWebP(t *testing.T, data []byte, img image.Image) {
	t.Helper()
	got, width, height, alpha, err := decodeEncoderOutput(data)
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	bounds := img.Bounds()
	if width != bounds.Dx() || height != bounds.Dy() {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, bounds.Dx(), bounds.Dy())
	}
	wantAlpha := benchmarkImageHasAlpha(img)
	if alpha != wantAlpha {
		t.Fatalf("alpha hint = %t, want %t", alpha, wantAlpha)
	}
	readPixel := pixelReaderFor(img)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := readPixel(bounds.Min.X+x, bounds.Min.Y+y)
			if got[y*width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*width+x], want)
			}
		}
	}
}

func (tc lossyBenchmarkCase) hasAlpha() bool {
	return tc.kind == benchmarkImageAlpha || tc.kind == benchmarkImageAlphaBands || tc.kind == benchmarkImageAlphaNeighborhood
}

type lossyWorkspaceMetrics struct {
	recBytes       int
	modeBytes      int
	residualBytes  int
	partitionBytes int
	totalBytes     int
}

type losslessWorkspaceMetrics struct {
	tokenUpperBoundBytes int
	lz77HashBytes        int
	metaPrefixBytes      int
	colorCacheBytes      int
	planCandidateBytes   int
	totalBytes           int
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
	recBytes := yStride*mbh*16 + cStride*mbh*8*2
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
	width, height := bounds.Dx(), bounds.Dy()
	pixels := width * height
	prefixWidth, prefixHeight := vp8lMetaPrefixImageDimensions(width, height, vp8lMinMetaPrefixCandidateBits)
	prefixBlocks := minInt(prefixWidth*prefixHeight, vp8lMaxMetaPrefixBlocks)
	tokenUpperBoundBytes := pixels * int(unsafe.Sizeof(vp8lToken{}))
	lz77HashBytes := 2 * vp8lHashSize * vp8lMinHashCandidates * int(unsafe.Sizeof(int32(0)))
	metaPrefixBytes := prefixBlocks * (int(unsafe.Sizeof(uint16(0))) + int(unsafe.Sizeof(imageAnalysis{})))
	metaPrefixBytes += vp8lMaxMetaPrefixGroups * (int(unsafe.Sizeof(imageAnalysis{})) + int(unsafe.Sizeof(int(0))))
	colorCacheBytes := int(unsafe.Sizeof(vp8lColorCachePlan{})) + vp8lMaxColorCacheSize*int(unsafe.Sizeof(color.NRGBA{}))
	planCandidateBytes := vp8lMaxEncodingPlanCandidates * int(unsafe.Sizeof(vp8lEncodingPlan{}))

	return losslessWorkspaceMetrics{
		tokenUpperBoundBytes: tokenUpperBoundBytes,
		lz77HashBytes:        lz77HashBytes,
		metaPrefixBytes:      metaPrefixBytes,
		colorCacheBytes:      colorCacheBytes,
		planCandidateBytes:   planCandidateBytes,
		totalBytes:           tokenUpperBoundBytes + lz77HashBytes + metaPrefixBytes + colorCacheBytes + planCandidateBytes,
	}
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

func newLosslessBenchmarkFixtureImage(tc losslessBenchmarkCase) image.Image {
	if tc.format == benchmarkFixturePaletted {
		return newBenchmarkLimitedPalettedFixtureImage(tc.width, tc.height)
	}
	if tc.format == benchmarkFixtureGray {
		return newBenchmarkGrayFixtureImage(tc.kind, tc.width, tc.height)
	}
	if tc.format == benchmarkFixtureRGBA {
		return newBenchmarkRGBAFixtureImage(tc.kind, tc.width, tc.height)
	}
	return newBenchmarkFixtureImage(lossyBenchmarkCase{
		kind:   tc.kind,
		width:  tc.width,
		height: tc.height,
	})
}

func newBenchmarkRGBAFixtureImage(kind benchmarkImageKind, width int, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			p := benchmarkPixel(kind, x, y)
			img.SetRGBA(x, y, color.RGBA{R: p.R, G: p.G, B: p.B, A: 255})
		}
	}
	return img
}

func newBenchmarkGrayFixtureImage(kind benchmarkImageKind, width int, height int) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			p := benchmarkPixel(kind, x, y)
			img.Pix[img.PixOffset(x, y)] = rgbToLuma(p.R, p.G, p.B)
		}
	}
	return img
}

func newBenchmarkYCbCrFixtureImage(width int, height int) *image.YCbCr {
	img := image.NewYCbCr(image.Rect(0, 0, width, height), image.YCbCrSubsampleRatio420)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c := benchmarkPixel(benchmarkImagePhotoLike, x, y)
			yy, cb, cr := color.RGBToYCbCr(c.R, c.G, c.B)
			img.Y[img.YOffset(x, y)] = yy
			ci := img.COffset(x, y)
			img.Cb[ci] = cb
			img.Cr[ci] = cr
		}
	}
	return img
}

func newBenchmarkPalettedFixtureImage(width int, height int) *image.Paletted {
	palette := make(color.Palette, 256)
	for i := range palette {
		palette[i] = benchmarkPixel(benchmarkImagePhotoLike, i*3, i*5)
	}
	img := image.NewPaletted(image.Rect(0, 0, width, height), palette)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Pix[img.PixOffset(x, y)] = uint8(x*3 + y*5 + x*y/17)
		}
	}
	return img
}

func newBenchmarkLimitedPalettedFixtureImage(width int, height int) *image.Paletted {
	palette := color.Palette{
		color.NRGBA{R: 248, G: 250, B: 252, A: 255},
		color.NRGBA{R: 31, G: 41, B: 55, A: 255},
		color.NRGBA{R: 59, G: 130, B: 246, A: 255},
		color.NRGBA{R: 16, G: 185, B: 129, A: 255},
		color.NRGBA{R: 245, G: 158, B: 11, A: 255},
		color.NRGBA{R: 239, G: 68, B: 68, A: 255},
		color.NRGBA{R: 139, G: 92, B: 246, A: 255},
		color.NRGBA{R: 14, G: 165, B: 233, A: 255},
		color.NRGBA{R: 226, G: 232, B: 240, A: 255},
		color.NRGBA{R: 148, G: 163, B: 184, A: 255},
		color.NRGBA{R: 15, G: 23, B: 42, A: 255},
		color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		color.NRGBA{R: 220, G: 38, B: 38, A: 255},
		color.NRGBA{R: 22, G: 163, B: 74, A: 255},
		color.NRGBA{R: 2, G: 132, B: 199, A: 255},
		color.NRGBA{R: 202, G: 138, B: 4, A: 255},
	}
	img := image.NewPaletted(image.Rect(0, 0, width, height), palette)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := (x/17 + y/23 + x*y/4096) % len(palette)
			if x%64 < 3 || y%64 < 3 {
				index = 1
			} else if (x/32+y/32)%5 == 0 {
				index = 2 + (x/32+y/32)%6
			}
			img.Pix[img.PixOffset(x, y)] = uint8(index)
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
	case benchmarkImageUI:
		if y < 40 {
			return color.NRGBA{R: 31, G: 41, B: 55, A: 255}
		}
		if x < 72 {
			return color.NRGBA{R: 241, G: 245, B: 249, A: 255}
		}
		if x%96 < 2 || y%64 < 2 {
			return color.NRGBA{R: 203, G: 213, B: 225, A: 255}
		}
		if x%112 > 12 && x%112 < 64 && y%88 > 18 && y%88 < 30 {
			return color.NRGBA{R: 59, G: 130, B: 246, A: 255}
		}
		if x%112 > 12 && x%112 < 88 && y%88 > 44 && y%88 < 48 {
			return color.NRGBA{R: 100, G: 116, B: 139, A: 255}
		}
		return color.NRGBA{R: 248, G: 250, B: 252, A: 255}
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
	case benchmarkImageAlphaNeighborhood:
		index := positiveMod(x+alphaNeighborhoodShift(y), 512)
		alpha := uint8(32 + (index*37)%191)
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
	case benchmarkImageEntropyRegions:
		switch {
		case x < 256 && y < 256:
			return color.NRGBA{R: 80, G: 120, B: 180, A: 255}
		case x >= 256 && y < 256:
			if x%17 == 0 || y%19 == 0 || (x+y)%41 == 0 {
				return color.NRGBA{R: 18, G: 24, B: 32, A: 255}
			}
			return color.NRGBA{R: 244, G: 246, B: 248, A: 255}
		case x < 256:
			localX := x
			localY := y - 256
			return color.NRGBA{
				R: uint8(localX*3 + localY),
				G: uint8(localY*5 + localX/2),
				B: uint8((localX+localY)*2 + localX*localY/17),
				A: 255,
			}
		default:
			localX := x - 256
			localY := y - 256
			base := (localX*3 + localY*5 + localX*localY/29) & 0xff
			return color.NRGBA{
				R: uint8(base + (localX*localY)%23),
				G: uint8(base/2 + localY*2 + (localX^localY)&31),
				B: uint8(base/3 + localX*2 + (localX*localY)%19),
				A: 255,
			}
		}
	default:
		return color.NRGBA{
			R: uint8(x*3 + y),
			G: uint8(y*5 + x/2),
			B: uint8((x+y)*2 + x*y/17),
			A: 255,
		}
	}
}

func benchmarkImageInputBytes(img image.Image) int {
	switch img := img.(type) {
	case *image.NRGBA:
		return len(img.Pix)
	case *image.RGBA:
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

func benchmarkImageHasAlpha(img image.Image) bool {
	bounds := img.Bounds()
	readPixel := pixelReaderFor(img)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if readPixel(x, y).A != 255 {
				return true
			}
		}
	}
	return false
}

func alphaNeighborhoodShift(y int) int {
	if y%2 == 0 {
		return 0
	}
	return -1
}

func positiveMod(v int, n int) int {
	v %= n
	if v < 0 {
		return v + n
	}
	return v
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
			got := work.recY[y*yStride+x]
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
