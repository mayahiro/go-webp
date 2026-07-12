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
	name              string
	config            vp8LossyConfig
	alphaConfig       lossyAlphaConfig
	customAlphaConfig bool
}

type lossyAblationFeatures struct {
	Y4                  bool                   `json:"y4"`
	Y4FlatnessLimit     int                    `json:"y4_flatness_limit"`
	Y4BeamWidth         int                    `json:"y4_refinement_beam_width"`
	CommitResiduals     bool                   `json:"commit_winning_residuals"`
	Trellis             bool                   `json:"trellis"`
	TrellisPasses       int                    `json:"trellis_passes"`
	RDPasses            int                    `json:"rd_passes"`
	MaxSegments         int                    `json:"max_segments"`
	SegmentStrength     int                    `json:"segment_strength"`
	TextureStrength     int                    `json:"texture_distortion_strength"`
	Skip                bool                   `json:"skip"`
	TokenProbUpdate     bool                   `json:"token_probability_update"`
	RDYLambdaScale      int                    `json:"rd_y_lambda_scale_256"`
	RDUVLambdaScale     int                    `json:"rd_uv_lambda_scale_256"`
	FilterLevel         int                    `json:"filter_level"`
	FilterLevelDelta    int                    `json:"filter_level_delta"`
	LoopFilterDisabled  bool                   `json:"loop_filter_disabled"`
	Y4FilterDelta       int                    `json:"y4_filter_delta"`
	SharpYUV            bool                   `json:"sharp_yuv"`
	MaterializeSource   bool                   `json:"materialize_source"`
	ParallelAlpha       bool                   `json:"parallel_alpha"`
	AlphaOptimalPasses  int                    `json:"alpha_optimal_passes"`
	AlphaOptimalFilters int                    `json:"alpha_optimal_filters"`
	QuantBias           lossyAblationQuantBias `json:"quant_bias"`
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
	CorpusGroup   string                   `json:"corpus_group"`
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
	best := variants[len(variants)-1]
	if best.name != "best" || !best.customAlphaConfig || best.alphaConfig != lossyAlphaConfigForMode(ModeBestCompression) {
		t.Fatalf("best alpha config = %#v", best)
	}

	final := lossyAblationVariantsForExperiment(75, "default-best-final")
	if len(final) != 2 || final[0].customAlphaConfig || !final[1].customAlphaConfig || final[1].alphaConfig != lossyAlphaConfigForMode(ModeBestCompression) {
		t.Fatalf("default-best-final variants = %#v", final)
	}
	parallelAlpha := lossyAblationVariantsForExperiment(75, "parallel-alpha")
	if len(parallelAlpha) != 2 || !parallelAlpha[0].config.parallelAlpha || parallelAlpha[1].config.parallelAlpha {
		t.Fatalf("parallel-alpha variants = %#v", parallelAlpha)
	}
}

func TestFilterLossyAblationSamples(t *testing.T) {
	samples := []benchmarkcorpus.Sample{
		{Metadata: benchmarkcorpus.Image{ID: "jpeg", Format: "jpeg"}},
		{Metadata: benchmarkcorpus.Image{ID: "png", Format: "png"}},
		{Metadata: benchmarkcorpus.Image{ID: "alpha", Format: "png", HasAlpha: true}},
	}
	alpha := filterLossyAblationSamples(t, samples, "alpha")
	if len(alpha) != 1 || alpha[0].Metadata.ID != "alpha" {
		t.Fatalf("alpha samples = %#v", alpha)
	}
	png := filterLossyAblationSamples(t, samples, "png")
	if len(png) != 2 || png[0].Metadata.ID != "png" || png[1].Metadata.ID != "alpha" {
		t.Fatalf("PNG samples = %#v", png)
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
	group := os.Getenv("GO_WEBP_ABLATION_GROUP")
	if group == "" {
		group = "all"
	}
	samples = filterLossyAblationSamples(t, samples, group)
	work, err := os.MkdirTemp("", "go-webp-lossy-ablation-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(work)

	variants := lossyAblationVariantsForExperiment(quality, os.Getenv("GO_WEBP_ABLATION_EXPERIMENT"))
	report := lossyAblationReport{
		SchemaVersion: 3,
		Corpus:        corpus.Corpus,
		CorpusSHA256:  corpus.CorpusSHA256,
		CorpusSplit:   split,
		CorpusGroup:   group,
		Holdout:       corpus.HoldoutPercent,
		Quality:       quality,
		TimingScope:   "in-process encodeLossyConfig call",
		QualityMetric: "weighted_y_ssim_and_rgb_mse",
	}
	aggregates := make(map[string]*lossyAblationAggregate, len(variants))
	for _, variant := range variants {
		aggregates[variant.name] = &lossyAblationAggregate{
			Variant:  variant.name,
			Features: lossyAblationFeaturesForVariant(variant),
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
			alphaConfig := lossyAlphaConfigForMode(ModeDefault)
			if variant.customAlphaConfig {
				alphaConfig = variant.alphaConfig
			}
			err := encodeLossyConfig(&encoded, newEncoderSource(sample.Pixels), variant.config, alphaConfig)
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

func filterLossyAblationSamples(t *testing.T, samples []benchmarkcorpus.Sample, group string) []benchmarkcorpus.Sample {
	t.Helper()
	if group == "all" {
		return samples
	}
	filtered := make([]benchmarkcorpus.Sample, 0, len(samples))
	for _, sample := range samples {
		include := false
		switch group {
		case "alpha":
			include = sample.Metadata.HasAlpha
		case "opaque":
			include = !sample.Metadata.HasAlpha
		case "jpeg", "png":
			include = sample.Metadata.Format == group
		default:
			t.Fatalf("invalid GO_WEBP_ABLATION_GROUP %q", group)
		}
		if include {
			filtered = append(filtered, sample)
		}
	}
	if len(filtered) == 0 {
		t.Fatalf("GO_WEBP_ABLATION_GROUP %q selected no fixtures", group)
	}
	return filtered
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
		{
			name:              "best",
			config:            bestConfig,
			alphaConfig:       lossyAlphaConfigForMode(ModeBestCompression),
			customAlphaConfig: true,
		},
	}
}

func lossyAblationVariantsForExperiment(quality int, experiment string) []lossyAblationVariant {
	if experiment == "segmentation-incumbent" {
		defaultConfig := vp8LossyConfigForModeQuality(ModeDefault, quality)
		oneSegment := defaultConfig
		oneSegment.maxSegments = 1
		return []lossyAblationVariant{
			{name: "default", config: defaultConfig},
			{name: "one-segment", config: oneSegment},
		}
	}
	if experiment == "segmentation-candidates" {
		defaultConfig := vp8LossyConfigForModeQuality(ModeDefault, quality)
		max2 := defaultConfig
		max2.maxSegments = 2
		max3 := defaultConfig
		max3.maxSegments = 3
		strength2 := defaultConfig
		strength2.segmentStrength = 2
		strength6 := defaultConfig
		strength6.segmentStrength = 6
		return []lossyAblationVariant{
			{name: "default", config: defaultConfig},
			{name: "segments-2", config: max2},
			{name: "segments-3", config: max3},
			{name: "strength-2", config: strength2},
			{name: "strength-6", config: strength6},
		}
	}
	if experiment == "loop-filter-candidates" {
		defaultConfig := vp8LossyConfigForModeQuality(ModeDefault, quality)
		noFilter := defaultConfig
		noFilter.disableLoopFilter = true
		minus4 := defaultConfig
		minus4.filterLevelDelta = -4
		plus4 := defaultConfig
		plus4.filterLevelDelta = 4
		return []lossyAblationVariant{
			{name: "default", config: defaultConfig},
			{name: "no-filter", config: noFilter},
			{name: "filter-minus-4", config: minus4},
			{name: "filter-plus-4", config: plus4},
		}
	}
	if experiment == "sharp-chroma-candidates" {
		sharpConfig := vp8LossyConfigForModeQuality(ModeBestCompression, quality)
		sharpConfig.defaultFrameIncumbent = false
		standardConfig := sharpConfig
		standardConfig.sharpYUV = false
		return []lossyAblationVariant{
			{name: "default", config: standardConfig},
			{name: "sharp-chroma", config: sharpConfig},
		}
	}
	if experiment == "trellis-passes" {
		defaultConfig := vp8LossyConfigForModeQuality(ModeBestCompression, quality)
		defaultConfig.defaultFrameIncumbent = false
		defaultConfig.trellis = true
		defaultConfig.trellisPasses = 1
		twoPass := defaultConfig
		twoPass.trellisPasses = 2
		withoutTrellis := defaultConfig
		withoutTrellis.trellis = false
		return []lossyAblationVariant{
			{name: "default", config: defaultConfig},
			{name: "trellis-two-pass", config: twoPass},
			{name: "trellis-off", config: withoutTrellis},
		}
	}
	if experiment == "y4-refinement-beam" {
		beamConfig := vp8LossyConfigForModeQuality(ModeBestCompression, quality)
		beamConfig.defaultFrameIncumbent = false
		defaultConfig := beamConfig
		defaultConfig.y4RefinementBeamWidth = 0
		return []lossyAblationVariant{
			{name: "default", config: defaultConfig},
			{name: "y4-beam-2", config: beamConfig},
		}
	}
	if experiment == "rd-passes" {
		defaultConfig := vp8LossyConfigForModeQuality(ModeBestCompression, quality)
		defaultConfig.defaultFrameIncumbent = false
		threePass := defaultConfig
		threePass.rdPasses = 3
		return []lossyAblationVariant{
			{name: "default", config: defaultConfig},
			{name: "rd-three-pass", config: threePass},
		}
	}
	if experiment == "default-best-final" {
		return []lossyAblationVariant{
			{name: "default", config: vp8LossyConfigForModeQuality(ModeDefault, quality)},
			{
				name:              "best",
				config:            vp8LossyConfigForModeQuality(ModeBestCompression, quality),
				alphaConfig:       lossyAlphaConfigForMode(ModeBestCompression),
				customAlphaConfig: true,
			},
		}
	}
	if experiment == "parallel-alpha" {
		defaultConfig := vp8LossyConfigForModeQuality(ModeDefault, quality)
		sequentialConfig := defaultConfig
		sequentialConfig.parallelAlpha = false
		return []lossyAblationVariant{
			{name: "default", config: defaultConfig},
			{name: "alpha-sequential", config: sequentialConfig},
		}
	}
	if experiment == "alpha-optimal-filters" {
		config := vp8LossyConfigForModeQuality(ModeBestCompression, quality)
		variants := make([]lossyAblationVariant, 0, 2)
		for _, filters := range []int{1, 2, 4} {
			alphaConfig := lossyAlphaConfigForMode(ModeBestCompression)
			alphaConfig.optimalFilters = filters
			variants = append(variants, lossyAblationVariant{
				name:              fmt.Sprintf("optimal-filters-%d", filters),
				config:            config,
				alphaConfig:       alphaConfig,
				customAlphaConfig: true,
			})
		}
		variants[0].name = "default"
		return variants
	}
	if experiment == "best-shared-quality" {
		defaultModeConfig := vp8LossyConfigForModeQuality(ModeDefault, quality)
		qIndex := qualityToVP8QIndex(quality)
		bestEffort := vp8EffortProfileForModeQIndex(ModeBestCompression, qIndex)
		bestEffort.defaultFrameIncumbent = false
		bestConfig := makeVP8LossyConfig(vp8ConservativeQualityProfileForQIndex(qIndex), bestEffort)
		sharedConfig := makeVP8LossyConfig(vp8QualityProfileForQIndex(qIndex), bestEffort)
		return []lossyAblationVariant{
			{name: "default", config: bestConfig},
			{name: "shared-quality", config: sharedConfig},
			{name: "mode-default", config: defaultModeConfig},
		}
	}
	if experiment == "winning-residual-commit" {
		commitConfig := vp8LossyConfigForModeQuality(ModeDefault, quality)
		defaultConfig := commitConfig
		defaultConfig.commitWinningResiduals = false
		return []lossyAblationVariant{
			{name: "default", config: defaultConfig},
			{name: "winning-residual-commit", config: commitConfig},
		}
	}
	if experiment == "y4-flatness-full" {
		flatConfig := vp8LossyConfigForModeQuality(ModeDefault, quality)
		defaultConfig := flatConfig
		defaultConfig.y4FlatnessLimit = 0
		return []lossyAblationVariant{
			{name: "default", config: defaultConfig},
			{name: "y4-flat-1", config: flatConfig},
		}
	}
	return lossyAblationVariants(quality)
}

func lossyAblationFeaturesFor(config vp8LossyConfig) lossyAblationFeatures {
	return lossyAblationFeatures{
		Y4:                 config.tryY4,
		Y4FlatnessLimit:    config.y4FlatnessLimit,
		Y4BeamWidth:        config.y4RefinementBeamWidth,
		CommitResiduals:    config.commitWinningResiduals,
		Trellis:            config.trellis,
		TrellisPasses:      config.trellisPasses,
		RDPasses:           config.rdPasses,
		MaxSegments:        config.maxSegments,
		SegmentStrength:    config.segmentStrength,
		TextureStrength:    config.textureStrength,
		Skip:               config.trySkip,
		TokenProbUpdate:    config.updateTokenProb,
		RDYLambdaScale:     config.rdYLambdaScale,
		RDUVLambdaScale:    config.rdUVLambdaScale,
		FilterLevel:        config.filter.level,
		FilterLevelDelta:   config.filterLevelDelta,
		LoopFilterDisabled: config.disableLoopFilter,
		Y4FilterDelta:      config.filter.modeDeltas[0],
		SharpYUV:           config.sharpYUV,
		MaterializeSource:  config.materializeSource,
		ParallelAlpha:      config.parallelAlpha,
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

func lossyAblationFeaturesForVariant(variant lossyAblationVariant) lossyAblationFeatures {
	features := lossyAblationFeaturesFor(variant.config)
	alphaConfig := lossyAlphaConfigForMode(ModeDefault)
	if variant.customAlphaConfig {
		alphaConfig = variant.alphaConfig
	}
	features.AlphaOptimalPasses = alphaConfig.optimalPasses
	features.AlphaOptimalFilters = alphaConfig.optimalFilters
	return features
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
