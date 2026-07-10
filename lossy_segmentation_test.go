package webp

import "testing"

func TestVP8SegmentationUsesConfiguredStrength(t *testing.T) {
	cfg := vp8LossyConfigForModeQuality(ModeDefault, 75)
	cfg.segmentStrength = 5
	segmentation := vp8Segmentation{count: 4}
	segmentation.configureSegments(cfg)
	want := [vp8SegmentCount]int{cfg.qIndex - 10, cfg.qIndex - 5, cfg.qIndex + 5, cfg.qIndex + 10}
	for index := range segmentation.segments {
		if got := segmentation.segments[index].quant.qIndex; got != want[index] {
			t.Fatalf("segment %d qIndex = %d, want %d", index, got, want[index])
		}
	}
}

func TestVP8BiasedQuantizationSuppressesBorderlineCoefficients(t *testing.T) {
	if got := quantizeTransformCoeff(12, 24); got != 1 {
		t.Fatalf("neutral coefficient = %d, want 1", got)
	}
	if got := quantizeTransformCoeffBiased(12, 24, 96); got != 0 {
		t.Fatalf("biased coefficient = %d, want 0", got)
	}
	if got := quantizeTransformCoeffBiased(-12, 24, 96); got != 0 {
		t.Fatalf("negative biased coefficient = %d, want 0", got)
	}
}

func TestVP8DefaultLossyConfigUsesTunedQuantBias(t *testing.T) {
	cfg := vp8LossyConfigForModeQuality(ModeDefault, 75)
	want := vp8QuantBias{y1DC: 114, y1AC: 114, y2DC: 114, y2AC: 114, uvDC: 124, uvAC: 124}
	if cfg.quantBias != want || cfg.quant.bias != want {
		t.Fatalf("default quant bias = %#v/%#v, want %#v", cfg.quantBias, cfg.quant.bias, want)
	}
	if cfg.rdYLambdaScale != 64 || cfg.rdUVLambdaScale != 96 {
		t.Fatalf("default RD scales = %d/%d, want 64/96", cfg.rdYLambdaScale, cfg.rdUVLambdaScale)
	}
	if cfg.textureStrength != 200 || cfg.rd.textureLambda <= 0 {
		t.Fatalf("default texture profile = %d/%d, want enabled", cfg.textureStrength, cfg.rd.textureLambda)
	}
}

func TestVP8LossyQualityProfileTracksQuantizer(t *testing.T) {
	for _, tc := range []struct {
		quality      int
		lumaBias     int
		uvBias       int
		yLambdaScale int
		texture      int
		sharpYUV     bool
	}{
		{quality: 25, lumaBias: 116, uvBias: 96, yLambdaScale: 64},
		{quality: 50, lumaBias: 116, uvBias: 112, yLambdaScale: 64},
		{quality: 75, lumaBias: 114, uvBias: 124, yLambdaScale: 64, texture: 200},
		{quality: 90, lumaBias: 116, uvBias: 128, yLambdaScale: 32, sharpYUV: true},
	} {
		cfg := vp8LossyConfigForModeQuality(ModeDefault, tc.quality)
		if cfg.quantBias.y1DC != tc.lumaBias || cfg.quantBias.y1AC != tc.lumaBias || cfg.quantBias.y2DC != tc.lumaBias || cfg.quantBias.y2AC != tc.lumaBias {
			t.Fatalf("quality %d luma bias = %#v, want %d", tc.quality, cfg.quantBias, tc.lumaBias)
		}
		if cfg.quantBias.uvDC != tc.uvBias || cfg.quantBias.uvAC != tc.uvBias {
			t.Fatalf("quality %d UV bias = %d/%d, want %d", tc.quality, cfg.quantBias.uvDC, cfg.quantBias.uvAC, tc.uvBias)
		}
		if cfg.rdYLambdaScale != tc.yLambdaScale {
			t.Fatalf("quality %d Y lambda scale = %d, want %d", tc.quality, cfg.rdYLambdaScale, tc.yLambdaScale)
		}
		if cfg.textureStrength != tc.texture {
			t.Fatalf("quality %d texture strength = %d, want %d", tc.quality, cfg.textureStrength, tc.texture)
		}
		if cfg.sharpYUV != tc.sharpYUV || cfg.materializeSource != tc.sharpYUV {
			t.Fatalf("quality %d sharp/materialized = %t/%t, want %t", tc.quality, cfg.sharpYUV, cfg.materializeSource, tc.sharpYUV)
		}
	}
}

func TestVP8TextureProfileUsesBoundedQuantizerRange(t *testing.T) {
	for _, tc := range []struct {
		qIndex  int
		enabled bool
	}{
		{qIndex: 14},
		{qIndex: 15, enabled: true},
		{qIndex: 30, enabled: true},
		{qIndex: 31},
	} {
		cfg := vp8LossyConfigForQIndex(ModeDefault, tc.qIndex)
		if got := cfg.textureStrength > 0; got != tc.enabled {
			t.Fatalf("qIndex %d texture enabled = %t, want %t", tc.qIndex, got, tc.enabled)
		}
		wantBias := 116
		if tc.enabled {
			wantBias = 114
		}
		if cfg.quantBias.y1AC != wantBias {
			t.Fatalf("qIndex %d luma bias = %d, want %d", tc.qIndex, cfg.quantBias.y1AC, wantBias)
		}
	}
	for _, mode := range []Mode{ModeFast, ModeLowMemory, ModeBestCompression} {
		if got := vp8LossyConfigForQIndex(mode, 20).textureStrength; got != 0 {
			t.Fatalf("mode %d texture strength = %d, want 0", mode, got)
		}
	}
}

func TestVP8AdaptiveSegmentStrengthTracksQuality(t *testing.T) {
	for _, tc := range []struct {
		quality int
		want    int
	}{
		{quality: 50, want: 4},
		{quality: 75, want: 4},
		{quality: 90, want: 2},
	} {
		cfg := vp8LossyConfigForModeQuality(ModeDefault, tc.quality)
		segmentation := vp8Segmentation{count: 4}
		segmentation.configureSegments(cfg)
		base := cfg.qIndex
		got := (segmentation.segments[3].quant.qIndex - base) / 2
		if got != tc.want {
			t.Fatalf("quality %d segment strength = %d, want %d", tc.quality, got, tc.want)
		}
	}
}
