package webp

import (
	"slices"
	"testing"
)

func TestCollectVP8SkipAndTokenStatsMatchesSeparatePasses(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind benchmarkImageKind
	}{
		{name: "Flat", kind: benchmarkImageFlat},
		{name: "PhotoLike", kind: benchmarkImagePhotoLike},
		{name: "UI", kind: benchmarkImageUI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: tc.kind, width: 64, height: 64})
			source := newVP8Source(newEncoderSource(img), false)
			cfg := vp8LossyConfigForModeQuality(ModeLowMemory, 75)
			mbw := (source.width + 15) >> 4
			mbh := (source.height + 15) >> 4
			segmentation := makeVP8Segmentation(source.readLuma, source.bounds, mbw, mbh, cfg)
			work := newVP8EncodeBuffers(mbw, mbh)
			pass := runVP8ModePass(source, cfg, work, mbw, mbh, &segmentation, nil, false)

			clearVP8Reconstruction(work)
			wantSkip := analyzeVP8MacroblockSkips(source.readLuma, source.readChroma, source.bounds, mbw, mbh, cfg.quant, &segmentation, pass.modes, work)
			clearVP8Reconstruction(work)
			wantStats := collectVP8TokenStatsConfig(source.readLuma, source.readChroma, source.bounds, mbw, mbh, cfg.quant, &segmentation, pass.modes, work, wantSkip)

			clearVP8Reconstruction(work)
			gotStats, gotSkip := collectVP8SkipAndTokenStats(source.readLuma, source.readChroma, source.bounds, mbw, mbh, cfg.quant, &segmentation, pass.modes, work)
			if !slices.Equal(gotSkip, wantSkip) {
				t.Fatalf("skip map differs: got %v, want %v", gotSkip, wantSkip)
			}
			if gotStats != wantStats {
				t.Fatal("token stats differ")
			}
		})
	}
}
