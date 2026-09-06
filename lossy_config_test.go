package webp

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"testing"
)

func TestEncodeLossyConfigMatchesPublicModes(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(7 * x), G: uint8(9 * y), B: uint8(3 * (x + y)), A: 255})
		}
	}
	for _, mode := range []Mode{ModeDefault, ModeBestCompression} {
		var public bytes.Buffer
		if err := Encode(&public, img, &Options{Compression: CompressionLossy, Quality: 75, Mode: mode}); err != nil {
			t.Fatalf("Encode mode %d: %v", mode, err)
		}
		var configured bytes.Buffer
		if err := encodeLossyConfig(
			&configured,
			newEncoderSource(img),
			vp8LossyConfigForModeQuality(mode, 75),
			lossyAlphaConfigForMode(mode),
		); err != nil {
			t.Fatalf("encodeLossyConfig mode %d: %v", mode, err)
		}
		if !bytes.Equal(configured.Bytes(), public.Bytes()) {
			t.Fatalf("configured mode %d output differed from public Encode", mode)
		}
	}
}

func TestEncodeLossyModeAutoCurrentRoutingMatchesDefault(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alpha uint8
	}{
		{name: "opaque", alpha: 255},
		{name: "alpha", alpha: 127},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewNRGBA(image.Rect(0, 0, 32, 24))
			for y := 0; y < img.Bounds().Dy(); y++ {
				for x := 0; x < img.Bounds().Dx(); x++ {
					img.SetNRGBA(x, y, color.NRGBA{
						R: uint8(7 * x),
						G: uint8(9 * y),
						B: uint8(3 * (x + y)),
						A: tc.alpha,
					})
				}
			}
			for _, quality := range []int{1, 75, 100} {
				t.Run(fmt.Sprintf("quality-%d", quality), func(t *testing.T) {
					defaultOutput := encodePublicLossyForTest(t, img, ModeDefault, quality)
					autoOutput := encodePublicLossyForTest(t, img, ModeAuto, quality)
					if !bytes.Equal(autoOutput, defaultOutput) {
						t.Fatalf("ModeAuto output = %d bytes, want current ModeDefault routing output %d bytes", len(autoOutput), len(defaultOutput))
					}
					if repeated := encodePublicLossyForTest(t, img, ModeAuto, quality); !bytes.Equal(repeated, autoOutput) {
						t.Fatal("ModeAuto output was not deterministic within the same encoder version")
					}
				})
			}
		})
	}
}

func TestVP8BestCompressionSharesDefaultQualityProfile(t *testing.T) {
	for quality := 1; quality <= 100; quality++ {
		defaultConfig := vp8LossyConfigForModeQuality(ModeDefault, quality)
		bestConfig := vp8LossyConfigForModeQuality(ModeBestCompression, quality)
		if bestConfig.qIndex != defaultConfig.qIndex ||
			bestConfig.quant != defaultConfig.quant ||
			bestConfig.quantDeltas != defaultConfig.quantDeltas ||
			bestConfig.quantBias != defaultConfig.quantBias ||
			bestConfig.filter != defaultConfig.filter ||
			bestConfig.rd != defaultConfig.rd ||
			bestConfig.rdYLambdaScale != defaultConfig.rdYLambdaScale ||
			bestConfig.rdUVLambdaScale != defaultConfig.rdUVLambdaScale ||
			bestConfig.textureStrength != defaultConfig.textureStrength {
			t.Fatalf("quality %d profile differs between Default and BestCompression", quality)
		}
	}
}

func TestVP8BestCompressionEffortIsDefaultSuperset(t *testing.T) {
	defaultConfig := vp8LossyConfigForModeQuality(ModeDefault, 75)
	bestConfig := vp8LossyConfigForModeQuality(ModeBestCompression, 75)
	if !bestConfig.tryY4 || !bestConfig.trySkip || !bestConfig.updateTokenProb || !bestConfig.bufferResiduals || !bestConfig.commitWinningResiduals {
		t.Fatal("BestCompression disabled a Default search stage")
	}
	if bestConfig.maxSegments < defaultConfig.maxSegments || bestConfig.rdPasses < defaultConfig.rdPasses {
		t.Fatalf("BestCompression effort = segments:%d passes:%d, Default = %d/%d", bestConfig.maxSegments, bestConfig.rdPasses, defaultConfig.maxSegments, defaultConfig.rdPasses)
	}
	if !bestConfig.materializeSource || bestConfig.trellis || !bestConfig.sharpYUV || !bestConfig.parallelAlpha || bestConfig.y4RefinementBeamWidth != 2 {
		t.Fatal("BestCompression did not enable its extended search stages")
	}
	if !bestConfig.defaultFrameIncumbent {
		t.Fatal("BestCompression disabled the Default frame incumbent")
	}
	if bestConfig.y4FlatnessLimit > defaultConfig.y4FlatnessLimit {
		t.Fatalf("BestCompression flatness gate = %d, want no more restrictive than Default %d", bestConfig.y4FlatnessLimit, defaultConfig.y4FlatnessLimit)
	}
}

func TestVP8BestCompressionOneTrellisPassMatchesTwoPassFixtures(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind benchmarkImageKind
	}{
		{name: "Gradient", kind: benchmarkImageGradient},
		{name: "PhotoLike", kind: benchmarkImagePhotoLike},
		{name: "UI", kind: benchmarkImageUI},
	} {
		img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: tc.kind, width: 65, height: 49})
		for _, quality := range []int{25, 75, 90} {
			t.Run(fmt.Sprintf("%s/Q%d", tc.name, quality), func(t *testing.T) {
				onePass := vp8LossyConfigForModeQuality(ModeBestCompression, quality)
				onePass.defaultFrameIncumbent = false
				onePass.trellis = true
				onePass.trellisPasses = 1
				twoPass := onePass
				twoPass.trellisPasses = 2
				onePassOutput := encodeLossyConfigForTest(t, img, onePass, ModeBestCompression)
				twoPassOutput := encodeLossyConfigForTest(t, img, twoPass, ModeBestCompression)
				if !bytes.Equal(onePassOutput, twoPassOutput) {
					t.Fatalf("one-pass output = %d bytes, two-pass %d", len(onePassOutput), len(twoPassOutput))
				}
			})
		}
	}
}

func TestVP8BestCompressionSelectsSmallerDefaultIncumbent(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind benchmarkImageKind
	}{
		{name: "Gradient", kind: benchmarkImageGradient},
		{name: "PhotoLike", kind: benchmarkImagePhotoLike},
		{name: "UI", kind: benchmarkImageUI},
	} {
		img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: tc.kind, width: 65, height: 49})
		for _, quality := range []int{25, 75, 90, 100} {
			t.Run(fmt.Sprintf("%s/Q%d", tc.name, quality), func(t *testing.T) {
				defaultConfig := vp8LossyConfigForModeQuality(ModeDefault, quality)
				bestConfig := vp8LossyConfigForModeQuality(ModeBestCompression, quality)
				rawBestConfig := bestConfig
				rawBestConfig.defaultFrameIncumbent = false

				defaultOutput := encodeLossyConfigForTest(t, img, defaultConfig, ModeDefault)
				rawBestOutput := encodeLossyConfigForTest(t, img, rawBestConfig, ModeBestCompression)
				guardedOutput := encodeLossyConfigForTest(t, img, bestConfig, ModeBestCompression)
				defaultChunks := readWebPChunks(t, defaultOutput)
				rawBestChunks := readWebPChunks(t, rawBestOutput)
				// RIFF padding can hide a one-byte VP8 frame difference.
				defaultFrame := defaultChunks[len(defaultChunks)-1].payload
				rawBestFrame := rawBestChunks[len(rawBestChunks)-1].payload
				want := rawBestOutput
				if len(defaultFrame) <= len(rawBestFrame) {
					want = defaultOutput
				}
				if !bytes.Equal(guardedOutput, want) {
					t.Fatalf("guarded output = %d bytes, want selected %d bytes", len(guardedOutput), len(want))
				}
			})
		}
	}
}

func encodeLossyConfigForTest(t *testing.T, img image.Image, cfg vp8LossyConfig, alphaMode Mode) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := encodeLossyConfig(&output, newEncoderSource(img), cfg, lossyAlphaConfigForMode(alphaMode)); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func encodePublicLossyForTest(t *testing.T, img image.Image, mode Mode, quality int) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := Encode(&output, img, &Options{Compression: CompressionLossy, Mode: mode, Quality: quality}); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
