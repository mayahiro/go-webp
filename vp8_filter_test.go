package webp

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVP8LoopFilterSimulationMatchesDWebP(t *testing.T) {
	if _, err := exec.LookPath("dwebp"); err != nil {
		t.Skip("dwebp is not available")
	}
	const width = 64
	const height = 48
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{
		kind:   benchmarkImagePhotoLike,
		width:  width,
		height: height,
	})
	tests := []struct {
		name   string
		adjust func(*vp8LossyConfig)
	}{
		{name: "normal"},
		{name: "normal-y16", adjust: func(cfg *vp8LossyConfig) { cfg.tryY4 = false }},
		{name: "normal-minus-4", adjust: func(cfg *vp8LossyConfig) { cfg.filterLevelDelta = -4 }},
		{name: "simple", adjust: func(cfg *vp8LossyConfig) { cfg.filter.simple = true }},
		{name: "disabled", adjust: func(cfg *vp8LossyConfig) { cfg.disableLoopFilter = true }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := vp8LossyConfigForModeQuality(ModeDefault, 75)
			if tc.adjust != nil {
				tc.adjust(&cfg)
			}
			cfg = cfg.withAdjustedLoopFilter()
			source := newVP8Source(newEncoderSource(img), cfg.materializeSource)
			mbw := (width + 15) >> 4
			mbh := (height + 15) >> 4
			work := newVP8EncodeBuffers(mbw, mbh)
			plan := makeVP8FramePlan(source, cfg, work)
			if !plan.segmentation.enabled() {
				t.Fatal("segmentation is disabled for the regression fixture")
			}
			if tc.name == "normal" || tc.name == "normal-y16" {
				y4 := 0
				for _, mode := range plan.modes {
					if !mode.useY16 {
						y4++
					}
				}
				if tc.name == "normal" && y4 == 0 {
					t.Fatal("normal fixture did not cover Y4 filtering")
				}
				if tc.name == "normal-y16" && y4 != 0 {
					t.Fatalf("normal-y16 Y4 macroblocks = %d, want 0", y4)
				}
			}
			reconstruction := reconstructVP8Frame(source, cfg, plan)
			applyVP8LoopFilter(&reconstruction, cfg.filter, &plan.segmentation, plan.modes, plan.skipMap)

			firstPart, residualPart, err := encodeVP8FramePartitions(source, cfg, work, plan)
			if err != nil {
				t.Fatalf("encode partitions: %v", err)
			}
			frame := assembleVP8KeyFrame(width, height, firstPart, residualPart)
			var encoded bytes.Buffer
			if err := writeLossySimple(&encoded, frame); err != nil {
				t.Fatalf("write WebP: %v", err)
			}
			decoded := decodeVP8RawYUV(t, encoded.Bytes())
			assertVP8PlaneEqual(t, "Y", decoded[:width*height], width, reconstruction.y, reconstruction.yStride, width, height)
			chromaWidth := width / 2
			chromaHeight := height / 2
			chromaSize := chromaWidth * chromaHeight
			assertVP8PlaneEqual(t, "Cb", decoded[width*height:width*height+chromaSize], chromaWidth, reconstruction.cb, reconstruction.cStride, chromaWidth, chromaHeight)
			assertVP8PlaneEqual(t, "Cr", decoded[width*height+chromaSize:width*height+2*chromaSize], chromaWidth, reconstruction.cr, reconstruction.cStride, chromaWidth, chromaHeight)
		})
	}
}

func TestVP8LoopFilterCandidateKeepsResidualPartition(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImagePhotoLike, width: 65, height: 49})
	defaultConfig := vp8LossyConfigForModeQuality(ModeDefault, 75)
	minus4 := defaultConfig
	minus4.filterLevelDelta = -4
	plus4 := defaultConfig
	plus4.filterLevelDelta = 4
	var want []byte
	for i, cfg := range []vp8LossyConfig{defaultConfig, minus4, plus4} {
		source := newVP8Source(newEncoderSource(img), cfg.materializeSource)
		frame, err := encodeVP8KeyFrameSource(source, cfg)
		if err != nil {
			t.Fatalf("candidate %d: %v", i, err)
		}
		firstPartitionBytes := vp8FrameFirstPartitionBytes(frame)
		residual := frame[10+firstPartitionBytes:]
		if i == 0 {
			want = append([]byte(nil), residual...)
			continue
		}
		if !bytes.Equal(residual, want) {
			t.Fatalf("candidate %d changed the residual partition", i)
		}
	}
}

func BenchmarkVP8LoopFilterSimulation(b *testing.B) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImagePhotoLike, width: 512, height: 512})
	cfg := vp8LossyConfigForModeQuality(ModeDefault, 75).withAdjustedLoopFilter()
	source := newVP8Source(newEncoderSource(img), cfg.materializeSource)
	mbw := (source.width + 15) >> 4
	mbh := (source.height + 15) >> 4
	plan := makeVP8FramePlan(source, cfg, newVP8EncodeBuffers(mbw, mbh))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		reconstruction := reconstructVP8Frame(source, cfg, plan)
		applyVP8LoopFilter(&reconstruction, cfg.filter, &plan.segmentation, plan.modes, plan.skipMap)
	}
}

func decodeVP8RawYUV(t *testing.T, encoded []byte) []byte {
	t.Helper()
	dir := t.TempDir()
	webpPath := filepath.Join(dir, "input.webp")
	yuvPath := filepath.Join(dir, "output.yuv")
	if err := os.WriteFile(webpPath, encoded, 0o600); err != nil {
		t.Fatalf("write WebP: %v", err)
	}
	if output, err := exec.Command("dwebp", "-quiet", "-yuv", webpPath, "-o", yuvPath).CombinedOutput(); err != nil {
		t.Fatalf("dwebp: %v: %s", err, output)
	}
	decoded, err := os.ReadFile(yuvPath)
	if err != nil {
		t.Fatalf("read YUV: %v", err)
	}
	return decoded
}

func assertVP8PlaneEqual(t *testing.T, name string, decoded []uint8, decodedStride int, simulated []uint8, simulatedStride int, width int, height int) {
	t.Helper()
	if len(decoded) < decodedStride*height {
		t.Fatalf("%s decoded length = %d, want at least %d", name, len(decoded), decodedStride*height)
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			got := simulated[y*simulatedStride+x]
			want := decoded[y*decodedStride+x]
			if got != want {
				t.Fatalf("%s simulation (%d,%d) = %d, dwebp %d", name, x, y, got, want)
			}
		}
	}
}
