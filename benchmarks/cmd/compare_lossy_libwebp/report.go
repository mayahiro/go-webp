package main

import (
	"math"

	"github.com/mayahiro/go-webp/internal/benchmarkbitstream"
	"github.com/mayahiro/go-webp/internal/benchmarkmetric"
)

const comparisonReportSchemaVersion = 8
const comparisonWarmupRuns = 1

// comparisonReport intentionally records anonymous fixture identities instead of source paths
type comparisonReport struct {
	SchemaVersion int                 `json:"schema_version"`
	Configuration reportConfiguration `json:"configuration"`
	Fixtures      []fixtureReport     `json:"fixtures"`
	Aggregate     aggregateReport     `json:"aggregate"`
}

type reportConfiguration struct {
	Runs                 int    `json:"runs"`
	Qualities            []int  `json:"qualities"`
	CWebPVersion         string `json:"cwebp_version"`
	CWebPMethod          int    `json:"cwebp_method"`
	CWebPSharpYUV        bool   `json:"cwebp_sharp_yuv"`
	CWebPMT              bool   `json:"cwebp_mt"`
	GoVersion            string `json:"go_version"`
	GOOS                 string `json:"goos"`
	GOARCH               string `json:"goarch"`
	GOMAXPROCS           int    `json:"gomaxprocs"`
	GoMode               string `json:"go_mode"`
	WarmupRuns           int    `json:"warmup_runs"`
	TimingStatistic      string `json:"timing_statistic"`
	OutputHashAlgorithm  string `json:"output_hash_algorithm"`
	GoTimingScope        string `json:"go_timing_scope"`
	CWebPTimingScope     string `json:"cwebp_timing_scope"`
	QualityMatchMetric   string `json:"quality_match_metric"`
	MatchStrategy        string `json:"match_strategy"`
	MatchLimitation      string `json:"match_limitation"`
	CurveInterpolation   string `json:"curve_interpolation"`
	CurveExtrapolation   string `json:"curve_extrapolation"`
	BjontegaardMinPoints int    `json:"bjontegaard_min_points"`
	ExactPSNRValue       string `json:"exact_psnr_value"`
	ExactSSIMValue       string `json:"exact_ssim_value"`
	Corpus               string `json:"corpus"`
	CorpusSHA256         string `json:"corpus_sha256,omitempty"`
	CorpusSplit          string `json:"corpus_split"`
	HoldoutPercent       int    `json:"holdout_percent,omitempty"`
}

type fixtureReport struct {
	Name           string       `json:"name"`
	SourceFormat   string       `json:"source_format,omitempty"`
	Split          string       `json:"split,omitempty"`
	HasAlpha       bool         `json:"has_alpha"`
	Width          int          `json:"width"`
	Height         int          `json:"height"`
	GoWebP         []sample     `json:"go_webp"`
	CWebP          []sample     `json:"cwebp"`
	MatchedSize    []pointMatch `json:"matched_size"`
	MatchedQuality []pointMatch `json:"matched_quality"`
}

type sample struct {
	Quality      int                            `json:"quality"`
	EncodedBytes int                            `json:"encoded_bytes"`
	Timing       timingSummary                  `json:"timing"`
	OutputSHA256 string                         `json:"output_sha256"`
	Layout       benchmarkbitstream.LossyLayout `json:"layout"`
	Distortion   distortionMetrics              `json:"distortion"`
}

type timingSummary struct {
	Runs       int   `json:"runs"`
	WarmupRuns int   `json:"warmup_runs"`
	MedianNS   int64 `json:"median_ns"`
	MinNS      int64 `json:"min_ns"`
	MaxNS      int64 `json:"max_ns"`
}

type distortionMetrics = benchmarkmetric.Metrics

type pointMatch struct {
	GoQuality                          int      `json:"go_quality"`
	CWebPQuality                       int      `json:"cwebp_quality"`
	GoBytes                            int      `json:"go_bytes"`
	CWebPBytes                         int      `json:"cwebp_bytes"`
	GoMinusCWebPBytes                  int      `json:"go_minus_cwebp_bytes"`
	GoMinusCWebPPercent                float64  `json:"go_minus_cwebp_percent"`
	GoMinusCWebPRGBPSNRDB              *float64 `json:"go_minus_cwebp_rgb_psnr_db"`
	GoMinusCWebPYPSNRDB                *float64 `json:"go_minus_cwebp_y_psnr_db"`
	GoMinusCWebPUVPSNRDB               *float64 `json:"go_minus_cwebp_uv_psnr_db"`
	GoMinusCWebPYSSIMDB                *float64 `json:"go_minus_cwebp_y_ssim_db"`
	GoMinusCWebPCompositeBlackPSNRDB   *float64 `json:"go_minus_cwebp_composite_black_psnr_db"`
	GoMinusCWebPCompositeWhitePSNRDB   *float64 `json:"go_minus_cwebp_composite_white_psnr_db"`
	GoMinusCWebPCompositeCheckerPSNRDB *float64 `json:"go_minus_cwebp_composite_checker_psnr_db"`
	GoAlphaExact                       bool     `json:"go_alpha_exact"`
	CWebPAlphaExact                    bool     `json:"cwebp_alpha_exact"`
}

func sampleAtQuality(samples []sample, quality int) (sample, bool) {
	for _, value := range samples {
		if value.Quality == quality {
			return value, true
		}
	}
	return sample{}, false
}

func buildMatches(goSamples []sample, cwebpSamples []sample) ([]pointMatch, []pointMatch) {
	matchedSize := make([]pointMatch, 0, len(goSamples))
	matchedQuality := make([]pointMatch, 0, len(goSamples))
	for _, goSample := range goSamples {
		if cwebpSample, ok := nearestSizeSample(goSample, cwebpSamples); ok {
			matchedSize = append(matchedSize, makePointMatch(goSample, cwebpSample))
		}
		if cwebpSample, ok := nearestQualitySample(goSample, cwebpSamples); ok {
			matchedQuality = append(matchedQuality, makePointMatch(goSample, cwebpSample))
		}
	}
	return matchedSize, matchedQuality
}

func nearestSizeSample(target sample, candidates []sample) (sample, bool) {
	best := sample{}
	bestDelta := 0
	found := false
	for _, candidate := range candidates {
		delta := absInt(candidate.EncodedBytes - target.EncodedBytes)
		if !found || delta < bestDelta || delta == bestDelta && qualityDistance(target, candidate) < qualityDistance(target, best) {
			best = candidate
			bestDelta = delta
			found = true
		}
	}
	return best, found
}

func nearestQualitySample(target sample, candidates []sample) (sample, bool) {
	best := sample{}
	bestDistance := 0.0
	found := false
	for _, candidate := range candidates {
		distance := qualityDistance(target, candidate)
		if !found || distance < bestDistance || distance == bestDistance && candidate.EncodedBytes < best.EncodedBytes {
			best = candidate
			bestDistance = distance
			found = true
		}
	}
	return best, found
}

func qualityDistance(a sample, b sample) float64 {
	switch {
	case a.Distortion.YSSIMDB == nil && b.Distortion.YSSIMDB == nil:
		return 0
	case a.Distortion.YSSIMDB == nil:
		return 1e6 - *b.Distortion.YSSIMDB
	case b.Distortion.YSSIMDB == nil:
		return 1e6 - *a.Distortion.YSSIMDB
	default:
		return math.Abs(*a.Distortion.YSSIMDB - *b.Distortion.YSSIMDB)
	}
}

func makePointMatch(goSample sample, cwebpSample sample) pointMatch {
	deltaBytes := goSample.EncodedBytes - cwebpSample.EncodedBytes
	deltaPct := 0.0
	if cwebpSample.EncodedBytes != 0 {
		deltaPct = 100 * float64(deltaBytes) / float64(cwebpSample.EncodedBytes)
	}
	return pointMatch{
		GoQuality:             goSample.Quality,
		CWebPQuality:          cwebpSample.Quality,
		GoBytes:               goSample.EncodedBytes,
		CWebPBytes:            cwebpSample.EncodedBytes,
		GoMinusCWebPBytes:     deltaBytes,
		GoMinusCWebPPercent:   deltaPct,
		GoMinusCWebPRGBPSNRDB: goMinusCWebPMetric(goSample.Distortion.RGBPSNRDB, cwebpSample.Distortion.RGBPSNRDB),
		GoMinusCWebPYPSNRDB:   goMinusCWebPMetric(goSample.Distortion.YPSNRDB, cwebpSample.Distortion.YPSNRDB),
		GoMinusCWebPUVPSNRDB:  goMinusCWebPMetric(goSample.Distortion.UVPSNRDB, cwebpSample.Distortion.UVPSNRDB),
		GoMinusCWebPYSSIMDB:   goMinusCWebPMetric(goSample.Distortion.YSSIMDB, cwebpSample.Distortion.YSSIMDB),
		GoMinusCWebPCompositeBlackPSNRDB: goMinusCWebPMetric(
			goSample.Distortion.CompositeOverBlackPSNRDB,
			cwebpSample.Distortion.CompositeOverBlackPSNRDB,
		),
		GoMinusCWebPCompositeWhitePSNRDB: goMinusCWebPMetric(
			goSample.Distortion.CompositeOverWhitePSNRDB,
			cwebpSample.Distortion.CompositeOverWhitePSNRDB,
		),
		GoMinusCWebPCompositeCheckerPSNRDB: goMinusCWebPMetric(
			goSample.Distortion.CompositeOverCheckerPSNRDB,
			cwebpSample.Distortion.CompositeOverCheckerPSNRDB,
		),
		GoAlphaExact:    goSample.Distortion.AlphaExact,
		CWebPAlphaExact: cwebpSample.Distortion.AlphaExact,
	}
}

func goMinusCWebPMetric(goValue *float64, cwebpValue *float64) *float64 {
	if goValue == nil && cwebpValue == nil {
		zero := 0.0
		return &zero
	}
	if goValue == nil || cwebpValue == nil {
		return nil
	}
	delta := *goValue - *cwebpValue
	return &delta
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
