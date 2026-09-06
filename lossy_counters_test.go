//go:build webp_lossy_counters

package webp

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func TestLossyCountersFastFixture(t *testing.T) {
	resetLossyCountersForTest()
	img := newLossyCounterFixture(16, 16, false)
	data := encodeLossyCounterFixture(t, img, &Options{
		Compression: CompressionLossy,
		Quality:     75,
		Mode:        ModeFast,
	})
	got := lossyCountersForTest()

	if got.Macroblocks != 1 {
		t.Fatalf("Macroblocks = %d, want 1", got.Macroblocks)
	}
	if got.SourcePixelReads == 0 || got.SourcePixelReads != got.RGBToYUVConversions {
		t.Fatalf("source/conversion counters = %d/%d, want equal non-zero values", got.SourcePixelReads, got.RGBToYUVConversions)
	}
	if got.PreparedSourceBytes != 0 || got.SharpChromaCandidates != 0 {
		t.Fatalf("prepared/sharp counters = %d/%d, want 0/0", got.PreparedSourceBytes, got.SharpChromaCandidates)
	}
	if got.Y16ModesScored == 0 || got.ChromaModesScored == 0 || got.ChromaFilterSamples == 0 {
		t.Fatalf("mode/chroma counters = Y16:%d chroma:%d samples:%d, want non-zero values", got.Y16ModesScored, got.ChromaModesScored, got.ChromaFilterSamples)
	}
	if got.Y4BlocksConsidered != 0 || got.Y4ModesScored != 0 || got.Y4MacroblocksSelected != 0 {
		t.Fatalf("Y4 counters = blocks:%d modes:%d selected:%d, want 0/0/0", got.Y4BlocksConsidered, got.Y4ModesScored, got.Y4MacroblocksSelected)
	}
	if got.ForwardDCTCount == 0 || got.InverseDCTCount == 0 || got.ResidualBlocks == 0 {
		t.Fatalf("transform/residual counters = forward:%d inverse:%d residual:%d, want non-zero values", got.ForwardDCTCount, got.InverseDCTCount, got.ResidualBlocks)
	}
	if got.RDPasses != 1 || got.ResidualCollectionPasses != 0 {
		t.Fatalf("pass counters = RD:%d residual:%d, want 1/0", got.RDPasses, got.ResidualCollectionPasses)
	}
	if got.SegmentsConsidered != 0 || got.SegmentsSelected != 0 || got.SegmentMapBits != 0 {
		t.Fatalf("segment counters = considered:%d selected:%d bits:%d, want 0/0/0", got.SegmentsConsidered, got.SegmentsSelected, got.SegmentMapBits)
	}
	if got.SkipCandidates != 0 || got.SkippedMacroblocks != 0 || got.TokenProbUpdatesTested != 0 || got.TokenProbUpdatesSelected != 0 {
		t.Fatalf("entropy counters = skip:%d/%d token:%d/%d, want zeros", got.SkipCandidates, got.SkippedMacroblocks, got.TokenProbUpdatesTested, got.TokenProbUpdatesSelected)
	}
	if got.TrellisBlocks != 0 || got.TrellisCandidateScores != 0 || got.TrellisPasses != 0 || got.TrellisCoefficientVisits != 0 || got.TrellisLevelChanges != 0 {
		t.Fatalf("trellis counters = blocks:%d scores:%d passes:%d coefficients:%d changes:%d, want zeros", got.TrellisBlocks, got.TrellisCandidateScores, got.TrellisPasses, got.TrellisCoefficientVisits, got.TrellisLevelChanges)
	}
	if got.FilterCandidates != 1 || got.SelectedFilterLevel != uint64(vp8LossyConfigForModeQuality(ModeFast, 75).filter.level) {
		t.Fatalf("filter counters = candidates:%d level:%d", got.FilterCandidates, got.SelectedFilterLevel)
	}
	if got.FirstPartitionBits != uint64(len(lossyCounterFirstPartition(t, data))*8) || got.FirstPartitionFallbacks != 0 {
		t.Fatalf("first partition counters = bits:%d fallbacks:%d", got.FirstPartitionBits, got.FirstPartitionFallbacks)
	}
	if got.AlphaFilters != 0 || got.AlphaLiterals != 0 || got.AlphaCopies != 0 || got.AlphaOptimalRows != 0 {
		t.Fatalf("alpha counters = filters:%d literals:%d copies:%d rows:%d, want zeros", got.AlphaFilters, got.AlphaLiterals, got.AlphaCopies, got.AlphaOptimalRows)
	}
	if got.YCbCrDirectConversions != 0 {
		t.Fatalf("YCbCrDirectConversions = %d, want 0 for an NRGBA source", got.YCbCrDirectConversions)
	}
}

func TestLossyCountersYCbCrDirectFixture(t *testing.T) {
	resetLossyCountersForTest()
	img := newBenchmarkYCbCrFixtureImage(32, 32)
	encodeLossyCounterFixture(t, img, &Options{Compression: CompressionLossy, Quality: 75})
	got := lossyCountersForTest()
	if got.YCbCrDirectConversions == 0 || got.YCbCrDirectConversions != got.SourcePixelReads {
		t.Fatalf("direct/source counters = %d/%d, want equal non-zero values", got.YCbCrDirectConversions, got.SourcePixelReads)
	}
	if got.RGBToYUVConversions != 0 {
		t.Fatalf("RGBToYUVConversions = %d, want 0 for direct YCbCr", got.RGBToYUVConversions)
	}
}

func TestLossyCountersBestCompressionFixture(t *testing.T) {
	resetLossyCountersForTest()
	const width, height = 64, 64
	img := newLossyCounterFixture(width, height, false)
	data := encodeLossyCounterFixture(t, img, &Options{
		Compression: CompressionLossy,
		Quality:     75,
		Mode:        ModeBestCompression,
	})
	got := lossyCountersForTest()

	const macroblocks = 16
	const plannedMacroblocks = macroblocks * 2
	if got.Macroblocks != plannedMacroblocks {
		t.Fatalf("Macroblocks = %d, want %d for Default and BestCompression candidates", got.Macroblocks, plannedMacroblocks)
	}
	if got.PreparedSourceBytes != width*height*3 {
		t.Fatalf("PreparedSourceBytes = %d, want %d", got.PreparedSourceBytes, width*height*3)
	}
	if got.RGBToYUVConversions <= width*height*2 {
		t.Fatalf("RGBToYUVConversions = %d, want more than the materialized BestCompression candidate %d", got.RGBToYUVConversions, width*height*2)
	}
	if got.SourcePixelReads <= width*height || got.SharpChromaCandidates == 0 {
		t.Fatalf("source/sharp counters = reads:%d candidates:%d", got.SourcePixelReads, got.SharpChromaCandidates)
	}
	if got.RDPasses != 3 || got.ResidualCollectionPasses != 0 {
		t.Fatalf("pass counters = RD:%d residual:%d, want 3/0 with the Default incumbent", got.RDPasses, got.ResidualCollectionPasses)
	}
	minimumY4Blocks := uint64(macroblocks * 16 * 2)
	maximumY4Blocks := uint64(macroblocks * 16 * 3)
	minimumY4Scores := got.Y4BlocksConsidered * uint64(vp8NumPredModes)
	maximumY4Scores := minimumY4Scores * vp8Y4MaxBeamWidth
	if got.Y4BlocksConsidered < minimumY4Blocks || got.Y4BlocksConsidered > maximumY4Blocks || got.Y4ModesScored < minimumY4Scores || got.Y4ModesScored > maximumY4Scores {
		t.Fatalf("Y4 counters = blocks:%d modes:%d, want blocks in [%d,%d] and modes in [%d,%d]", got.Y4BlocksConsidered, got.Y4ModesScored, minimumY4Blocks, maximumY4Blocks, minimumY4Scores, maximumY4Scores)
	}
	if got.Y4MacroblocksSelected > uint64(macroblocks)*got.RDPasses {
		t.Fatalf("Y4MacroblocksSelected = %d, want at most %d", got.Y4MacroblocksSelected, uint64(macroblocks)*got.RDPasses)
	}
	if got.SegmentsConsidered != 4 || got.SegmentsSelected != 4 || got.SegmentMapBits != macroblocks*4 {
		t.Fatalf("segment counters = considered:%d selected:%d bits:%d, want 4/4/%d", got.SegmentsConsidered, got.SegmentsSelected, got.SegmentMapBits, macroblocks*4)
	}
	if got.SkipCandidates != macroblocks*3 || got.SkippedMacroblocks > plannedMacroblocks {
		t.Fatalf("skip counters = candidates:%d selected:%d, want %d candidates and at most %d selected", got.SkipCandidates, got.SkippedMacroblocks, macroblocks*3, plannedMacroblocks)
	}
	if got.TokenProbUpdatesTested == 0 || got.TokenProbUpdatesSelected > got.TokenProbUpdatesTested {
		t.Fatalf("token probability counters = tested:%d selected:%d", got.TokenProbUpdatesTested, got.TokenProbUpdatesSelected)
	}
	if got.TrellisBlocks != 0 || got.TrellisCandidateScores != 0 || got.TrellisPasses != 0 || got.TrellisCoefficientVisits != 0 || got.TrellisLevelChanges != 0 {
		t.Fatalf("trellis counters = blocks:%d scores:%d passes:%d coefficients:%d changes:%d, want zero for BestCompression", got.TrellisBlocks, got.TrellisCandidateScores, got.TrellisPasses, got.TrellisCoefficientVisits, got.TrellisLevelChanges)
	}
	if got.FirstPartitionBits != uint64(len(lossyCounterFirstPartition(t, data))*8) || got.FirstPartitionFallbacks != 0 {
		t.Fatalf("first partition counters = bits:%d fallbacks:%d", got.FirstPartitionBits, got.FirstPartitionFallbacks)
	}
}

func TestLossyCountersBestCompressionReusesHighQualitySource(t *testing.T) {
	const width, height = 64, 64
	img := newLossyCounterFixture(width, height, false)
	resetLossyCountersForTest()
	encodeLossyCounterFixture(t, img, &Options{Compression: CompressionLossy, Quality: 100, Mode: ModeDefault})
	defaultCounts := lossyCountersForTest()
	resetLossyCountersForTest()
	encodeLossyCounterFixture(t, img, &Options{Compression: CompressionLossy, Quality: 100, Mode: ModeBestCompression})
	bestCounts := lossyCountersForTest()
	if bestCounts.PreparedSourceBytes != width*height*3 {
		t.Fatalf("prepared source bytes = %d, want one plane (%d)", bestCounts.PreparedSourceBytes, width*height*3)
	}
	if defaultCounts.SharpChromaCandidates == 0 || bestCounts.SharpChromaCandidates != defaultCounts.SharpChromaCandidates {
		t.Fatalf("sharp candidates = %d, want one preparation (%d)", bestCounts.SharpChromaCandidates, defaultCounts.SharpChromaCandidates)
	}
	if bestCounts.Macroblocks != 2*defaultCounts.Macroblocks {
		t.Fatalf("macroblocks = %d, want both frame searches (%d)", bestCounts.Macroblocks, 2*defaultCounts.Macroblocks)
	}
}

func TestLossyCountersOpaque16BitImagesSkipAlpha(t *testing.T) {
	const size = 64
	bounds := image.Rect(-3, 5, size-3, size+5)
	rgba := image.NewRGBA64(bounds.Inset(-1)).SubImage(bounds).(*image.RGBA64)
	nrgba := image.NewNRGBA64(bounds.Inset(-1)).SubImage(bounds).(*image.NRGBA64)
	gray := image.NewGray16(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			value := uint16(uint8(x*37+y*13)) * 257
			rgba.SetRGBA64(x, y, color.RGBA64{R: value, G: value, B: value, A: 65535})
			nrgba.SetNRGBA64(x, y, color.NRGBA64{R: value, G: value, B: value, A: 65535})
			gray.SetGray16(x, y, color.Gray16{Y: value})
		}
	}
	for _, img := range []image.Image{rgba, nrgba, gray} {
		resetLossyCountersForTest()
		opts := &Options{Compression: CompressionLossy, Mode: ModeBestCompression, Quality: 100}
		got := encodeLossyCounterFixture(t, img, opts)
		counts := lossyCountersForTest()
		if counts.AlphaFilters != 0 || counts.AlphaOptimalRows != 0 {
			t.Errorf("%T alpha filters/optimal rows = %d/%d, want 0/0", img, counts.AlphaFilters, counts.AlphaOptimalRows)
		}
		want := encodeLossyCounterFixture(t, benchmarkImageWrapper{Image: img}, opts)
		if !bytes.Equal(got, want) {
			t.Errorf("%T output differs from generic image path", img)
		}
	}
}

func TestLossyCountersExplicitTrellisFixture(t *testing.T) {
	resetLossyCountersForTest()
	img := newLossyCounterFixture(64, 64, false)
	cfg := vp8LossyConfigForModeQuality(ModeBestCompression, 75)
	cfg.defaultFrameIncumbent = false
	cfg.trellis = true
	cfg.trellisPasses = 1
	var output bytes.Buffer
	if err := encodeLossyConfig(&output, newEncoderSource(img), cfg, lossyAlphaConfigForMode(ModeBestCompression)); err != nil {
		t.Fatal(err)
	}
	got := lossyCountersForTest()
	if got.TrellisBlocks == 0 || got.TrellisCandidateScores == 0 || got.TrellisPasses == 0 || got.TrellisPasses > got.TrellisBlocks || got.TrellisCoefficientVisits < got.TrellisLevelChanges {
		t.Fatalf("trellis counters = blocks:%d scores:%d passes:%d coefficients:%d changes:%d", got.TrellisBlocks, got.TrellisCandidateScores, got.TrellisPasses, got.TrellisCoefficientVisits, got.TrellisLevelChanges)
	}
}

func TestLossyCountersAlphaFixture(t *testing.T) {
	resetLossyCountersForTest()
	const width, height = 64, 64
	img := newLossyCounterFixture(width, height, true)
	data := encodeLossyCounterFixture(t, img, &Options{
		Compression: CompressionLossy,
		Quality:     75,
		Mode:        ModeBestCompression,
	})
	got := lossyCountersForTest()

	if got.AlphaFilters != 4 {
		t.Fatalf("AlphaFilters = %d, want 4", got.AlphaFilters)
	}
	if got.AlphaOptimalRows != height {
		t.Fatalf("AlphaOptimalRows = %d, want %d", got.AlphaOptimalRows, height)
	}
	if got.AlphaLiterals == 0 || got.AlphaCopies == 0 {
		t.Fatalf("alpha token counters = literals:%d copies:%d, want non-zero values", got.AlphaLiterals, got.AlphaCopies)
	}
	chunks := readWebPChunks(t, data)
	if len(chunks) != 3 || chunks[1].name != "ALPH" || chunks[1].payload[0]&0x03 != alphCompressionVP8L {
		t.Fatalf("chunks = %#v, want compressed ALPH followed by VP8", chunks)
	}
}

func TestLossyCountersReset(t *testing.T) {
	resetLossyCountersForTest()
	countLossyCounter(lossyCounterMacroblocks, 3)
	resetLossyCountersForTest()
	if got := lossyCountersForTest(); got != (lossyCounterSnapshot{}) {
		t.Fatalf("snapshot after reset = %#v, want zero", got)
	}
}

func TestLossyCountersFirstPartitionFallback(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImagePhotoLike, width: 128, height: 128, quality: 75})
	limit := dcPredictionFirstPartitionSizeForTest(img, ModeBestCompression, 75)
	resetLossyCountersForTest()
	data, stage, err := encodeLossyWithFirstPartitionLimitForTest(img, ModeBestCompression, 75, limit)
	if err != nil {
		t.Fatalf("fallback encode failed: %v", err)
	}
	if stage != vp8FirstPartitionFallbackDCPrediction {
		t.Fatalf("fallback stage = %d, want %d", stage, vp8FirstPartitionFallbackDCPrediction)
	}
	got := lossyCountersForTest()
	if got.FirstPartitionFallbacks != 1 {
		t.Fatalf("FirstPartitionFallbacks = %d, want 1", got.FirstPartitionFallbacks)
	}
	if got.FirstPartitionBits != uint64(len(lossyCounterFirstPartition(t, data))*8) {
		t.Fatalf("FirstPartitionBits = %d, want accepted partition size", got.FirstPartitionBits)
	}
}

func BenchmarkLossyCounterProfiles(b *testing.B) {
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
				options := &Options{Compression: CompressionLossy, Quality: 75, Mode: mode.mode}
				var output bytes.Buffer
				resetLossyCountersForTest()
				b.ResetTimer()
				for b.Loop() {
					output.Reset()
					if err := Encode(&output, fixture.img, options); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				reportLossyCounterMetrics(b, lossyCountersForTest(), b.N)
			})
		}
	}
}

func reportLossyCounterMetrics(b *testing.B, counters lossyCounterSnapshot, iterations int) {
	b.Helper()
	perEncode := func(value uint64) float64 {
		return float64(value) / float64(iterations)
	}
	b.ReportMetric(perEncode(counters.SourcePixelReads), "source_reads/op")
	b.ReportMetric(perEncode(counters.RGBToYUVConversions), "rgb_to_yuv/op")
	b.ReportMetric(perEncode(counters.YCbCrDirectConversions), "ycbcr_direct/op")
	b.ReportMetric(perEncode(counters.PreparedSourceBytes), "prepared_B/op")
	b.ReportMetric(perEncode(counters.ChromaFilterSamples), "chroma_samples/op")
	b.ReportMetric(perEncode(counters.SharpChromaCandidates), "sharp_candidates/op")
	b.ReportMetric(perEncode(counters.Macroblocks), "macroblocks/op")
	b.ReportMetric(perEncode(counters.SegmentsConsidered), "segments_considered/op")
	b.ReportMetric(perEncode(counters.SegmentsSelected), "segments_selected/op")
	b.ReportMetric(perEncode(counters.SegmentMapBits), "segment_map_bits/op")
	b.ReportMetric(perEncode(counters.Y16ModesScored), "y16_modes/op")
	b.ReportMetric(perEncode(counters.Y4BlocksConsidered), "y4_blocks/op")
	b.ReportMetric(perEncode(counters.Y4ModesScored), "y4_modes/op")
	b.ReportMetric(perEncode(counters.Y4MacroblocksSelected), "y4_selected/op")
	b.ReportMetric(perEncode(counters.ChromaModesScored), "chroma_modes/op")
	b.ReportMetric(perEncode(counters.ForwardDCTCount), "forward_dct/op")
	b.ReportMetric(perEncode(counters.InverseDCTCount), "inverse_dct/op")
	b.ReportMetric(perEncode(counters.RDPasses), "rd_passes/op")
	b.ReportMetric(perEncode(counters.ResidualCollectionPasses), "residual_passes/op")
	b.ReportMetric(perEncode(counters.ResidualBlocks), "residual_blocks/op")
	b.ReportMetric(perEncode(counters.TrellisBlocks), "trellis_blocks/op")
	b.ReportMetric(perEncode(counters.TrellisCandidateScores), "trellis_scores/op")
	b.ReportMetric(perEncode(counters.TrellisPasses), "trellis_passes/op")
	b.ReportMetric(perEncode(counters.TrellisCoefficientVisits), "trellis_coefficients/op")
	b.ReportMetric(perEncode(counters.TrellisLevelChanges), "trellis_changes/op")
	b.ReportMetric(perEncode(counters.SkipCandidates), "skip_candidates/op")
	b.ReportMetric(perEncode(counters.SkippedMacroblocks), "skipped_macroblocks/op")
	b.ReportMetric(perEncode(counters.TokenProbUpdatesTested), "token_updates_tested/op")
	b.ReportMetric(perEncode(counters.TokenProbUpdatesSelected), "token_updates_selected/op")
	b.ReportMetric(perEncode(counters.FilterCandidates), "filter_candidates/op")
	b.ReportMetric(float64(counters.SelectedFilterLevel), "selected_filter_level")
	b.ReportMetric(perEncode(counters.AlphaFilters), "alpha_filters/op")
	b.ReportMetric(perEncode(counters.AlphaLiterals), "alpha_literals/op")
	b.ReportMetric(perEncode(counters.AlphaCopies), "alpha_copies/op")
	b.ReportMetric(perEncode(counters.AlphaOptimalRows), "alpha_optimal_rows/op")
	b.ReportMetric(perEncode(counters.FirstPartitionBits), "first_partition_bits/op")
	b.ReportMetric(perEncode(counters.FirstPartitionFallbacks), "first_partition_fallbacks/op")
}

func newLossyCounterFixture(width int, height int, alpha bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	mbw := (width + 15) >> 4
	macroblocks := mbw * ((height + 15) >> 4)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			macroblock := (y>>4)*mbw + (x >> 4)
			value := uint8(32 + macroblock*11)
			if macroblock >= macroblocks/2 && (x/2+y/2)&1 != 0 {
				value = 255 - value
			}
			a := uint8(255)
			if alpha {
				a = 32
				if x/8&1 != 0 {
					a = 220
				}
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: value,
				G: value / 2,
				B: 255 - value,
				A: a,
			})
		}
	}
	return img
}

func encodeLossyCounterFixture(t *testing.T, img image.Image, options *Options) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := Encode(&output, img, options); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	return output.Bytes()
}

func lossyCounterFirstPartition(t *testing.T, data []byte) []byte {
	t.Helper()
	for _, chunk := range readWebPChunks(t, data) {
		if chunk.name == "VP8 " {
			return readVP8FirstPartition(t, chunk.payload)
		}
	}
	t.Fatal("VP8 chunk not found")
	return nil
}
