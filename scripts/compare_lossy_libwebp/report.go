package main

import "math"

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
}

type fixtureReport struct {
	Name           string       `json:"name"`
	Width          int          `json:"width"`
	Height         int          `json:"height"`
	GoWebP         []sample     `json:"go_webp"`
	CWebP          []sample     `json:"cwebp"`
	MatchedSize    []pointMatch `json:"matched_size"`
	MatchedQuality []pointMatch `json:"matched_quality"`
}

type sample struct {
	Quality         int               `json:"quality"`
	EncodedBytes    int               `json:"encoded_bytes"`
	AverageEncodeNS int64             `json:"average_encode_ns"`
	Distortion      distortionMetrics `json:"distortion"`
}

type distortionMetrics struct {
	RGBMAE     float64  `json:"rgb_mae"`
	RGBMSE     float64  `json:"rgb_mse"`
	RGBPSNRDB  *float64 `json:"rgb_psnr_db"`
	YPSNRDB    *float64 `json:"y_psnr_db"`
	UVPSNRDB   *float64 `json:"uv_psnr_db"`
	AlphaMAE   float64  `json:"alpha_mae"`
	RGBExact   bool     `json:"rgb_exact"`
	AlphaExact bool     `json:"alpha_exact"`
}

type pointMatch struct {
	GoQuality      int      `json:"go_quality"`
	CWebPQuality   int      `json:"cwebp_quality"`
	GoBytes        int      `json:"go_bytes"`
	CWebPBytes     int      `json:"cwebp_bytes"`
	SizeDeltaBytes int      `json:"size_delta_bytes"`
	SizeDeltaPct   float64  `json:"size_delta_percent"`
	RGBPSNRDeltaDB *float64 `json:"rgb_psnr_delta_db"`
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
	case a.Distortion.RGBPSNRDB == nil && b.Distortion.RGBPSNRDB == nil:
		return 0
	case a.Distortion.RGBPSNRDB == nil:
		return 1e6 - *b.Distortion.RGBPSNRDB
	case b.Distortion.RGBPSNRDB == nil:
		return 1e6 - *a.Distortion.RGBPSNRDB
	default:
		return math.Abs(*a.Distortion.RGBPSNRDB - *b.Distortion.RGBPSNRDB)
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
		RGBPSNRDeltaDB: psnrDelta(goSample.Distortion.RGBPSNRDB, cwebpSample.Distortion.RGBPSNRDB),
	}
}

func psnrDelta(goPSNR *float64, cwebpPSNR *float64) *float64 {
	if goPSNR == nil && cwebpPSNR == nil {
		zero := 0.0
		return &zero
	}
	if goPSNR == nil || cwebpPSNR == nil {
		return nil
	}
	delta := *cwebpPSNR - *goPSNR
	return &delta
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
