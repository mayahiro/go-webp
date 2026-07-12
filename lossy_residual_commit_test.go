package webp

import (
	"bytes"
	"image"
	"testing"
)

func TestVP8WinningResidualCommitPreservesFrame(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind benchmarkImageKind
	}{
		{name: "Gradient", kind: benchmarkImageGradient},
		{name: "PhotoLike", kind: benchmarkImagePhotoLike},
		{name: "UI", kind: benchmarkImageUI},
	} {
		img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: tc.kind, width: 65, height: 49})
		for _, mode := range []Mode{ModeDefault, ModeBestCompression} {
			t.Run(tc.name+"/"+lossyModeNameForTest(mode), func(t *testing.T) {
				cfg := vp8LossyConfigForModeQuality(mode, 75)
				committed := encodeVP8FrameWithResidualCommitForTest(t, img, cfg)
				cfg.commitWinningResiduals = false
				separate := encodeVP8FrameWithResidualCommitForTest(t, img, cfg)
				if !bytes.Equal(committed, separate) {
					t.Fatalf("committed frame differs: got %d bytes, want %d", len(committed), len(separate))
				}
			})
		}
	}
}

func encodeVP8FrameWithResidualCommitForTest(t *testing.T, img image.Image, cfg vp8LossyConfig) []byte {
	t.Helper()
	encoderSource := newEncoderSource(img)
	source := newVP8Source(encoderSource, cfg.materializeSource)
	if cfg.sharpYUV && source.materialized() {
		source.applySharpChroma(encoderSource.pixels())
	}
	frame, err := encodeVP8KeyFrameSource(source, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func lossyModeNameForTest(mode Mode) string {
	if mode == ModeBestCompression {
		return "BestCompression"
	}
	return "Default"
}
