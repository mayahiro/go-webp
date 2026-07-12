package webp

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVP8FirstPartitionFallbackSuppressesTokenUpdatesAtRealLimit(t *testing.T) {
	const mbw, mbh = 1021, 108
	segmentation, modes, tokenProbs, skipMap := makeVP8FirstPartitionStressPlan(mbw, mbh)
	cfg := vp8LossyConfigForQIndex(ModeDefault, 30)
	plan := vp8FramePlan{
		mbw:          mbw,
		mbh:          mbh,
		modes:        modes,
		tokenProbs:   tokenProbs,
		skipMap:      skipMap,
		skipProb:     128,
		segmentation: *segmentation,
	}
	initialSize := vp8FirstPartitionSize(mbw, mbh, cfg.qIndex, cfg.quantDeltas, cfg.filter, &plan.segmentation, plan.modes, plan.tokenProbs, plan.skipMap, plan.skipProb)
	if initialSize <= vp8FirstPartitionMax {
		t.Fatalf("initial size = %d, want greater than %d", initialSize, vp8FirstPartitionMax)
	}
	withoutUpdates := plan
	withoutUpdates.tokenProbs = vp8DefaultTokenProbs
	withoutUpdatesSize := vp8FirstPartitionSize(mbw, mbh, cfg.qIndex, cfg.quantDeltas, cfg.filter, &withoutUpdates.segmentation, withoutUpdates.modes, withoutUpdates.tokenProbs, withoutUpdates.skipMap, withoutUpdates.skipProb)
	if withoutUpdatesSize > vp8FirstPartitionMax {
		t.Fatalf("size without token updates = %d, want at most %d", withoutUpdatesSize, vp8FirstPartitionMax)
	}
	t.Logf("first partition = %d bytes, without token updates = %d bytes", initialSize, withoutUpdatesSize)

	firstPart, fallbackCfg, fallbackPlan, stage, err := vp8FirstPartitionWithFallback(vp8Source{}, cfg, nil, plan, vp8FirstPartitionMax)
	if err != nil {
		t.Fatalf("vp8FirstPartitionWithFallback failed: %v", err)
	}
	if stage != vp8FirstPartitionFallbackTokenProbs {
		t.Fatalf("fallback stage = %d, want token probability fallback %d", stage, vp8FirstPartitionFallbackTokenProbs)
	}
	if fallbackCfg.updateTokenProb || fallbackPlan.tokenProbs != vp8DefaultTokenProbs {
		t.Fatal("token probability updates remained enabled after fallback")
	}
	if len(firstPart) != withoutUpdatesSize {
		t.Fatalf("fallback first partition = %d bytes, want %d", len(firstPart), withoutUpdatesSize)
	}
}

func TestVP8FirstPartitionDCPredictionFitsMaximumDimensions(t *testing.T) {
	const macroblockDimension = (maxVP8Dimension + 15) >> 4
	modes := make([]vp8MBMode, macroblockDimension*macroblockDimension)
	for i := range modes {
		modes[i] = vp8MBMode{useY16: true, yMode: vp8PredDC, cMode: vp8PredDC}
	}
	cfg := vp8DCPredictionFallbackConfig(vp8LossyConfigForQIndex(ModeBestCompression, 30))
	size := vp8FirstPartitionSize(macroblockDimension, macroblockDimension, cfg.qIndex, cfg.quantDeltas, cfg.filter, nil, modes, vp8DefaultTokenProbs, nil, 0)
	if size > vp8FirstPartitionMax {
		t.Fatalf("maximum-dimension DC first partition = %d bytes, limit %d", size, vp8FirstPartitionMax)
	}
	t.Logf("maximum-dimension DC first partition = %d bytes", size)
}

func TestVP8FirstPartitionFallbackStream(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kind  benchmarkImageKind
		alpha bool
	}{
		{name: "opaque", kind: benchmarkImagePhotoLike},
		{name: "alpha", kind: benchmarkImageAlphaBands, alpha: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: tc.kind, width: 128, height: 128, quality: 75})
			limit := dcPredictionFirstPartitionSizeForTest(img, ModeBestCompression, 75)
			first, firstStage, err := encodeLossyWithFirstPartitionLimitForTest(img, ModeBestCompression, 75, limit)
			if err != nil {
				t.Fatalf("first fallback encode failed: %v", err)
			}
			second, secondStage, err := encodeLossyWithFirstPartitionLimitForTest(img, ModeBestCompression, 75, limit)
			if err != nil {
				t.Fatalf("second fallback encode failed: %v", err)
			}
			if firstStage != vp8FirstPartitionFallbackDCPrediction || secondStage != firstStage {
				t.Fatalf("fallback stages = %d/%d, want DC prediction %d", firstStage, secondStage, vp8FirstPartitionFallbackDCPrediction)
			}
			if !bytes.Equal(first, second) {
				t.Fatal("fallback output is not deterministic")
			}

			chunks := readWebPChunks(t, first)
			if tc.alpha {
				if len(chunks) != 3 || chunks[0].name != "VP8X" || chunks[1].name != "ALPH" || chunks[2].name != "VP8 " {
					t.Fatalf("fallback chunks = %#v, want VP8X, ALPH, VP8", chunks)
				}
				assertLossyVP8Frame(t, chunks[2].payload, 128, 128)
			} else {
				if len(chunks) != 1 || chunks[0].name != "VP8 " {
					t.Fatalf("fallback chunks = %#v, want VP8", chunks)
				}
				assertLossyVP8Frame(t, chunks[0].payload, 128, 128)
			}

			for _, decoder := range []struct {
				name    string
				command string
				args    func(string, string) []string
			}{
				{name: "dwebp", command: "dwebp", args: func(input string, output string) []string {
					return []string{"-quiet", input, "-o", output}
				}},
				{name: "sips", command: "sips", args: func(input string, output string) []string {
					return []string{"-s", "format", "png", input, "--out", output}
				}},
			} {
				t.Run(decoder.name, func(t *testing.T) {
					if _, err := exec.LookPath(decoder.command); err != nil {
						t.Skipf("%s is not available", decoder.command)
					}
					dir := t.TempDir()
					webpPath := filepath.Join(dir, "fallback.webp")
					pngPath := filepath.Join(dir, "fallback.png")
					if err := os.WriteFile(webpPath, first, 0o600); err != nil {
						t.Fatalf("write fallback WebP: %v", err)
					}
					if output, err := exec.Command(decoder.command, decoder.args(webpPath, pngPath)...).CombinedOutput(); err != nil {
						t.Fatalf("%s failed: %v: %s", decoder.name, err, output)
					}
					assertFallbackDecodedPNG(t, pngPath, img, tc.alpha)
				})
			}
		})
	}
}

func assertFallbackDecodedPNG(t *testing.T, path string, source *image.NRGBA, checkAlpha bool) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open decoded PNG: %v", err)
	}
	decoded, err := png.Decode(file)
	closeErr := file.Close()
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	if closeErr != nil {
		t.Fatalf("close decoded PNG: %v", closeErr)
	}
	if decoded.Bounds().Dx() != 128 || decoded.Bounds().Dy() != 128 {
		t.Fatalf("decoded dimensions = %dx%d, want 128x128", decoded.Bounds().Dx(), decoded.Bounds().Dy())
	}
	if !checkAlpha {
		return
	}
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			_, _, _, alpha := decoded.At(x, y).RGBA()
			if got, want := uint8(alpha>>8), source.NRGBAAt(x, y).A; got != want {
				t.Fatalf("decoded alpha (%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}

func dcPredictionFirstPartitionSizeForTest(img image.Image, mode Mode, quality int) int {
	source := newEncoderSource(img)
	cfg := vp8DCPredictionFallbackConfig(vp8LossyConfigForModeQuality(mode, quality))
	vp8Source := newVP8Source(source, cfg.materializeSource)
	if cfg.sharpYUV && vp8Source.materialized() {
		vp8Source.applySharpChroma(source.pixels())
	}
	work := newVP8EncodeBuffers((source.width+15)>>4, (source.height+15)>>4)
	plan := makeVP8FramePlan(vp8Source, cfg, work)
	return vp8FirstPartitionSize(plan.mbw, plan.mbh, cfg.qIndex, cfg.quantDeltas, cfg.filter, &plan.segmentation, plan.modes, plan.tokenProbs, plan.skipMap, plan.skipProb)
}

func encodeLossyWithFirstPartitionLimitForTest(img image.Image, mode Mode, quality int, limit int) ([]byte, vp8FirstPartitionFallbackStage, error) {
	source := newEncoderSource(img)
	cfg := vp8LossyConfigForModeQuality(mode, quality)
	alphaCfg := lossyAlphaConfigForMode(mode)
	vp8Source := newVP8Source(source, cfg.materializeSource)
	if cfg.sharpYUV && vp8Source.materialized() {
		vp8Source.applySharpChroma(source.pixels())
	}
	var alphaAnalysis lossyAlphaAnalysis
	var readPixel pixelReader
	if !lossyStandardImageOpaque(source.image) {
		readPixel = source.pixels()
		alphaAnalysis = analyzeLossyAlphaConfig(readPixel, source.bounds, source.width, source.height, alphaCfg)
	}
	work := newVP8EncodeBuffers((source.width+15)>>4, (source.height+15)>>4)
	plan := makeVP8FramePlan(vp8Source, cfg, work)
	firstPart, residualPart, stage, err := encodeVP8FramePartitionsLimit(vp8Source, cfg, work, plan, limit)
	if err != nil {
		return nil, stage, err
	}
	frame := assembleVP8KeyFrame(source.width, source.height, firstPart, residualPart)
	var output bytes.Buffer
	if alphaAnalysis.hasAlpha {
		err = writeLossyExtended(&output, readPixel, source.bounds, source.width, source.height, frame, alphaAnalysis, alphaCfg)
	} else {
		err = writeLossySimple(&output, frame)
	}
	return output.Bytes(), stage, err
}
