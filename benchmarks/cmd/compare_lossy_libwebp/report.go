package main

import (
	"math"

	"github.com/mayahiro/go-webp/internal/benchmarkbitstream"
	"github.com/mayahiro/go-webp/internal/benchmarkmetric"
)

// comparisonReport intentionally records anonymous fixture identities instead of source paths
type comparisonReport struct {
	SchemaVersion int                 `json:"schema_version"`
	Configuration reportConfiguration `json:"configuration"`
	Fixtures      []fixtureReport     `json:"fixtures"`
}

type reportConfiguration struct {
	Runs               int    `json:"runs"`
	Qualities          []int  `json:"qualities"`
	CWebPVersion       string `json:"cwebp_version"`
	CWebPMethod        int    `json:"cwebp_method"`
	CWebPSharpYUV      bool   `json:"cwebp_sharp_yuv"`
	CWebPMT            bool   `json:"cwebp_mt"`
	GoVersion          string `json:"go_version"`
	GOOS               string `json:"goos"`
	GOARCH             string `json:"goarch"`
	GoMode             string `json:"go_mode"`
	GoTimingScope      string `json:"go_timing_scope"`
	CWebPTimingScope   string `json:"cwebp_timing_scope"`
	QualityMatchMetric string `json:"quality_match_metric"`
	MatchStrategy      string `json:"match_strategy"`
	ExactPSNRValue     string `json:"exact_psnr_value"`
	ExactSSIMValue     string `json:"exact_ssim_value"`
	Corpus             string `json:"corpus"`
	CorpusSHA256       string `json:"corpus_sha256,omitempty"`
	CorpusSplit        string `json:"corpus_split"`
	HoldoutPercent     int    `json:"holdout_percent,omitempty"`
}

type fixtureReport struct {
	Name           string       `json:"name"`
	SourceFormat   string       `json:"source_format,omitempty"`
	Split          string       `json:"split,omitempty"`
	Width          int          `json:"width"`
	Height         int          `json:"height"`
	GoWebP         []sample     `json:"go_webp"`
	CWebP          []sample     `json:"cwebp"`
	MatchedSize    []pointMatch `json:"matched_size"`
	MatchedQuality []pointMatch `json:"matched_quality"`
}

type sample struct {
	Quality         int                            `json:"quality"`
	EncodedBytes    int                            `json:"encoded_bytes"`
	AverageEncodeNS int64                          `json:"average_encode_ns"`
	Layout          benchmarkbitstream.LossyLayout `json:"layout"`
	Distortion      distortionMetrics              `json:"distortion"`
}

type distortionMetrics = benchmarkmetric.Metrics

type pointMatch struct {
	GoQuality      int      `json:"go_quality"`
	CWebPQuality   int      `json:"cwebp_quality"`
	GoBytes        int      `json:"go_bytes"`
	CWebPBytes     int      `json:"cwebp_bytes"`
	SizeDeltaBytes int      `json:"size_delta_bytes"`
	SizeDeltaPct   float64  `json:"size_delta_percent"`
	RGBPSNRDeltaDB *float64 `json:"rgb_psnr_delta_db"`
	YSSIMDeltaDB   *float64 `json:"y_ssim_delta_db"`
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
	deltaBytes := cwebpSample.EncodedBytes - goSample.EncodedBytes
	deltaPct := 0.0
	if goSample.EncodedBytes != 0 {
		deltaPct = 100 * float64(deltaBytes) / float64(goSample.EncodedBytes)
	}
	return pointMatch{
		GoQuality:      goSample.Quality,
		CWebPQuality:   cwebpSample.Quality,
		GoBytes:        goSample.EncodedBytes,
		CWebPBytes:     cwebpSample.EncodedBytes,
		SizeDeltaBytes: deltaBytes,
		SizeDeltaPct:   deltaPct,
		RGBPSNRDeltaDB: metricDelta(goSample.Distortion.RGBPSNRDB, cwebpSample.Distortion.RGBPSNRDB),
		YSSIMDeltaDB:   metricDelta(goSample.Distortion.YSSIMDB, cwebpSample.Distortion.YSSIMDB),
	}
}

func metricDelta(goValue *float64, cwebpValue *float64) *float64 {
	if goValue == nil && cwebpValue == nil {
		zero := 0.0
		return &zero
	}
	if goValue == nil || cwebpValue == nil {
		return nil
	}
	delta := *cwebpValue - *goValue
	return &delta
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
