package webp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mayahiro/go-webp/internal/benchmarkbitstream"
	"github.com/mayahiro/go-webp/internal/benchmarkcorpus"
	"github.com/mayahiro/go-webp/internal/benchmarkmetric"
)

type lossyAblationVariant struct {
	name   string
	config vp8LossyConfig
}

type lossyAblationFeatures struct {
	Y4                bool                   `json:"y4"`
	Trellis           bool                   `json:"trellis"`
	RDPasses          int                    `json:"rd_passes"`
	MaxSegments       int                    `json:"max_segments"`
	SegmentStrength   int                    `json:"segment_strength"`
	TextureStrength   int                    `json:"texture_distortion_strength"`
	Skip              bool                   `json:"skip"`
	TokenProbUpdate   bool                   `json:"token_probability_update"`
	RDYLambdaScale    int                    `json:"rd_y_lambda_scale_256"`
	RDUVLambdaScale   int                    `json:"rd_uv_lambda_scale_256"`
	Y4FilterDelta     int                    `json:"y4_filter_delta"`
	SharpYUV          bool                   `json:"sharp_yuv"`
	MaterializeSource bool                   `json:"materialize_source"`
	QuantBias         lossyAblationQuantBias `json:"quant_bias"`
}

type lossyAblationQuantBias struct {
	Y1DC int `json:"y1_dc"`
	Y1AC int `json:"y1_ac"`
	Y2DC int `json:"y2_dc"`
	Y2AC int `json:"y2_ac"`
	UVDC int `json:"uv_dc"`
	UVAC int `json:"uv_ac"`
}

type lossyAblationReport struct {
	SchemaVersion int                      `json:"schema_version"`
	Corpus        string                   `json:"corpus"`
	CorpusSHA256  string                   `json:"corpus_sha256"`
	CorpusSplit   string                   `json:"corpus_split"`
	Holdout       int                      `json:"holdout_percent"`
	Quality       int                      `json:"quality"`
	Fixtures      []lossyAblationFixture   `json:"fixtures"`
	Aggregates    []lossyAblationAggregate `json:"aggregates"`
	TimingScope   string                   `json:"timing_scope"`
	QualityMetric string                   `json:"quality_metric"`
}

type lossyAblationFixture struct {
	ID      string                       `json:"id"`
	Split   string                       `json:"split"`
	Width   int                          `json:"width"`
	Height  int                          `json:"height"`
	Results []lossyAblationFixtureResult `json:"results"`
}

type lossyAblationFixtureResult struct {
	Variant      string                         `json:"variant"`
	EncodedBytes int                            `json:"encoded_bytes"`
	EncodeNS     int64                          `json:"encode_ns"`
	Layout       benchmarkbitstream.LossyLayout `json:"layout"`
	Distortion   benchmarkmetric.Metrics        `json:"distortion"`
}

type lossyAblationAggregate struct {
	Variant          string                         `json:"variant"`
	Features         lossyAblationFeatures          `json:"features"`
	EncodedBytes     int64                          `json:"encoded_bytes"`
	SizeDeltaPercent float64                        `json:"size_delta_percent_from_default"`
	EncodeNS         int64                          `json:"encode_ns"`
	TimeRatio        float64                        `json:"time_ratio_from_default"`
	WeightedYSSIM    float64                        `json:"weighted_y_ssim"`
	WeightedRGBMSE   float64                        `json:"weighted_rgb_mse"`
	Layout           benchmarkbitstream.LossyLayout `json:"layout"`
	ySSIMSum         float64
	rgbMSESum        float64
	pixels           int64
}

func TestLossyAblationVariants(t *testing.T) {
	variants := lossyAblationVariants(75)
	if len(variants) != 6 {
		t.Fatalf("variant count = %d, want 6", len(variants))
	}
	names := make(map[string]struct{}, len(variants))
	configs := make(map[vp8LossyConfig]string, len(variants))
	for _, variant := range variants {
		if _, ok := names[variant.name]; ok {
			t.Fatalf("duplicate variant name %q", variant.name)
		}
		names[variant.name] = struct{}{}
		if previous, ok := configs[variant.config]; ok {
			t.Fatalf("variants %q and %q use the same config", previous, variant.name)
		}
		configs[variant.config] = variant.name
	}
	for _, required := range []string{"default", "no-y4", "one-segment", "no-skip", "no-token-update", "best"} {
		if _, ok := names[required]; !ok {
			t.Fatalf("missing variant %q", required)
		}
	}
}

func TestLossyAblationCorpus(t *testing.T) {
	root := os.Getenv("GO_WEBP_ABLATION_CORPUS")
	if root == "" {
		t.Skip("GO_WEBP_ABLATION_CORPUS is not set")
	}
	output := os.Getenv("GO_WEBP_ABLATION_OUTPUT")
	if output == "" {
		t.Fatal("GO_WEBP_ABLATION_OUTPUT must be set with GO_WEBP_ABLATION_CORPUS")
	}
	if _, err := exec.LookPath("dwebp"); err != nil {
		t.Fatalf("dwebp not found in PATH: %v", err)
	}
	split := os.Getenv("GO_WEBP_ABLATION_SPLIT")
	if split == "" {
		split = "train"
	}
	quality := 75
	if value := os.Getenv("GO_WEBP_ABLATION_QUALITY"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			t.Fatalf("invalid GO_WEBP_ABLATION_QUALITY %q", value)
		}
		quality = parsed
	}

	corpus, samples, err := benchmarkcorpus.LoadSplit(root, "production", 20, split)
	if err != nil {
		t.Fatal(err)
	}
	work, err := os.MkdirTemp("", "go-webp-lossy-ablation-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(work)

	variants := lossyAblationVariants(quality)
	report := lossyAblationReport{
		SchemaVersion: 2,
		Corpus:        corpus.Corpus,
		CorpusSHA256:  corpus.CorpusSHA256,
		CorpusSplit:   split,
		Holdout:       corpus.HoldoutPercent,
		Quality:       quality,
		TimingScope:   "in-process encodeLossyConfig call",
		QualityMetric: "weighted_y_ssim_and_rgb_mse",
	}
	aggregates := make(map[string]*lossyAblationAggregate, len(variants))
	for _, variant := range variants {
		aggregates[variant.name] = &lossyAblationAggregate{
			Variant:  variant.name,
			Features: lossyAblationFeaturesFor(variant.config),
		}
	}
	for _, sample := range samples {
		bounds := sample.Pixels.Bounds()
		pixels := int64(bounds.Dx()) * int64(bounds.Dy())
		fixture := lossyAblationFixture{
			ID:     sample.Metadata.ID,
			Split:  sample.Metadata.Split,
			Width:  bounds.Dx(),
			Height: bounds.Dy(),
		}
		for _, variant := range variants {
			t.Logf("fixture=%s variant=%s", sample.Metadata.ID, variant.name)
			var encoded bytes.Buffer
			start := time.Now()
			err := encodeLossyConfig(&encoded, newEncoderSource(sample.Pixels), variant.config, lossyAlphaConfigForMode(ModeDefault))
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("fixture %s variant %s: %v", sample.Metadata.ID, variant.name, err)
			}
			layout, err := benchmarkbitstream.ParseLossy(encoded.Bytes())
			if err != nil {
				t.Fatalf("fixture %s variant %s layout: %v", sample.Metadata.ID, variant.name, err)
			}
			metrics, err := decodeLossyAblation(work, sample.Metadata.ID+"-"+variant.name, encoded.Bytes(), sample.Pixels)
			if err != nil {
				t.Fatalf("fixture %s variant %s: %v", sample.Metadata.ID, variant.name, err)
			}
			fixture.Results = append(fixture.Results, lossyAblationFixtureResult{
				Variant:      variant.name,
				EncodedBytes: encoded.Len(),
				EncodeNS:     elapsed.Nanoseconds(),
				Layout:       layout,
				Distortion:   metrics,
			})
			aggregate := aggregates[variant.name]
			aggregate.EncodedBytes += int64(encoded.Len())
			aggregate.EncodeNS += elapsed.Nanoseconds()
			aggregate.Layout.Add(layout)
			aggregate.ySSIMSum += metrics.YSSIM * float64(pixels)
			aggregate.rgbMSESum += metrics.RGBMSE * float64(pixels)
			aggregate.pixels += pixels
		}
		report.Fixtures = append(report.Fixtures, fixture)
	}
	defaultAggregate := aggregates["default"]
	for _, variant := range variants {
		aggregate := aggregates[variant.name]
		aggregate.WeightedYSSIM = aggregate.ySSIMSum / float64(aggregate.pixels)
		aggregate.WeightedRGBMSE = aggregate.rgbMSESum / float64(aggregate.pixels)
		aggregate.SizeDeltaPercent = 100 * float64(aggregate.EncodedBytes-defaultAggregate.EncodedBytes) / float64(defaultAggregate.EncodedBytes)
		aggregate.TimeRatio = float64(aggregate.EncodeNS) / float64(defaultAggregate.EncodeNS)
		report.Aggregates = append(report.Aggregates, *aggregate)
	}
	if err := writeLossyAblationReport(output, report); err != nil {
		t.Fatal(err)
	}
}

func lossyAblationVariants(quality int) []lossyAblationVariant {
	defaultConfig := vp8LossyConfigForModeQuality(ModeDefault, quality)
	bestConfig := vp8LossyConfigForModeQuality(ModeBestCompression, quality)
	withoutY4 := defaultConfig
	withoutY4.tryY4 = false
	oneSegment := defaultConfig
	oneSegment.maxSegments = 1
	withoutSkip := defaultConfig
	withoutSkip.trySkip = false
	withoutTokenUpdate := defaultConfig
	withoutTokenUpdate.updateTokenProb = false
	return []lossyAblationVariant{
		{name: "default", config: defaultConfig},
		{name: "no-y4", config: withoutY4},
		{name: "one-segment", config: oneSegment},
		{name: "no-skip", config: withoutSkip},
		{name: "no-token-update", config: withoutTokenUpdate},
		{name: "best", config: bestConfig},
	}
}

func lossyAblationFeaturesFor(config vp8LossyConfig) lossyAblationFeatures {
	return lossyAblationFeatures{
		Y4:                config.tryY4,
		Trellis:           config.trellis,
		RDPasses:          config.rdPasses,
		MaxSegments:       config.maxSegments,
		SegmentStrength:   config.segmentStrength,
		TextureStrength:   config.textureStrength,
		Skip:              config.trySkip,
		TokenProbUpdate:   config.updateTokenProb,
		RDYLambdaScale:    config.rdYLambdaScale,
		RDUVLambdaScale:   config.rdUVLambdaScale,
		Y4FilterDelta:     config.filter.modeDeltas[0],
		SharpYUV:          config.sharpYUV,
		MaterializeSource: config.materializeSource,
		QuantBias: lossyAblationQuantBias{
			Y1DC: config.quantBias.y1DC,
			Y1AC: config.quantBias.y1AC,
			Y2DC: config.quantBias.y2DC,
			Y2AC: config.quantBias.y2AC,
			UVDC: config.quantBias.uvDC,
			UVAC: config.quantBias.uvAC,
		},
	}
}

func decodeLossyAblation(work string, name string, encoded []byte, source image.Image) (benchmarkmetric.Metrics, error) {
	webpPath := filepath.Join(work, name+".webp")
	pngPath := filepath.Join(work, name+".png")
	if err := os.WriteFile(webpPath, encoded, 0o600); err != nil {
		return benchmarkmetric.Metrics{}, err
	}
	if output, err := exec.Command("dwebp", "-quiet", webpPath, "-o", pngPath).CombinedOutput(); err != nil {
		return benchmarkmetric.Metrics{}, fmt.Errorf("dwebp: %w: %s", err, output)
	}
	file, err := os.Open(pngPath)
	if err != nil {
		return benchmarkmetric.Metrics{}, err
	}
	decoded, decodeErr := png.Decode(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return benchmarkmetric.Metrics{}, decodeErr
	}
	if closeErr != nil {
		return benchmarkmetric.Metrics{}, closeErr
	}
	return benchmarkmetric.Measure(source, decoded)
}

func writeLossyAblationReport(path string, report lossyAblationReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
