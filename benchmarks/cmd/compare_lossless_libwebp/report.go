package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mayahiro/go-webp/internal/benchmarkbitstream"
)

// losslessComparisonReport intentionally records anonymous fixture identities instead of source paths
type losslessComparisonReport struct {
	SchemaVersion int                         `json:"schema_version"`
	Configuration losslessReportConfiguration `json:"configuration"`
	Fixtures      []losslessFixtureReport     `json:"fixtures"`
	Aggregate     losslessAggregateReport     `json:"aggregate"`
}

type losslessReportConfiguration struct {
	Runs             int      `json:"runs"`
	CWebPVersion     string   `json:"cwebp_version"`
	CWebPMethod      int      `json:"cwebp_method"`
	GoVersion        string   `json:"go_version"`
	GOOS             string   `json:"goos"`
	GOARCH           string   `json:"goarch"`
	GoMode           string   `json:"go_mode"`
	GoTimingScope    string   `json:"go_timing_scope"`
	CWebPTimingScope string   `json:"cwebp_timing_scope"`
	Corpus           string   `json:"corpus"`
	CorpusSHA256     string   `json:"corpus_sha256,omitempty"`
	CorpusSplit      string   `json:"corpus_split"`
	HoldoutPercent   int      `json:"holdout_percent,omitempty"`
	FixtureFilter    []string `json:"fixture_filter,omitempty"`
}

type losslessFixtureReport struct {
	Name         string         `json:"name"`
	SourceFormat string         `json:"source_format,omitempty"`
	Split        string         `json:"split,omitempty"`
	Width        int            `json:"width"`
	Height       int            `json:"height"`
	GoWebP       losslessSample `json:"go_webp"`
	CWebP        losslessSample `json:"cwebp"`
}

type losslessSample struct {
	EncodedBytes    int64                             `json:"encoded_bytes"`
	AverageEncodeNS int64                             `json:"average_encode_ns"`
	Layout          benchmarkbitstream.LosslessLayout `json:"layout"`
	RGBMAE          float64                           `json:"rgb_mae"`
	RGBMaxAbs       int                               `json:"rgb_max_abs"`
	AlphaExact      bool                              `json:"alpha_exact"`
	Exact           bool                              `json:"exact"`
}

type losslessAggregateReport struct {
	Files             int                               `json:"files"`
	GoWebPBytes       int64                             `json:"go_webp_bytes"`
	CWebPBytes        int64                             `json:"cwebp_bytes"`
	GoSizeDeltaBytes  int64                             `json:"go_size_delta_bytes"`
	GoSizeDeltaPct    float64                           `json:"go_size_delta_percent"`
	GoAverageEncodeNS int64                             `json:"go_average_encode_ns"`
	CWebPAverageNS    int64                             `json:"cwebp_average_process_ns"`
	GoSmaller         int                               `json:"go_smaller"`
	CWebPSmaller      int                               `json:"cwebp_smaller"`
	EqualSize         int                               `json:"equal_size"`
	GoWebPLayout      benchmarkbitstream.LosslessLayout `json:"go_webp_layout"`
	CWebPLayout       benchmarkbitstream.LosslessLayout `json:"cwebp_layout"`
}

func losslessSampleFromResult(value result) losslessSample {
	return losslessSample{
		EncodedBytes:    value.size,
		AverageEncodeNS: value.avg.Nanoseconds(),
		Layout:          value.layout,
		RGBMAE:          value.distortion.rgbMAE,
		RGBMaxAbs:       value.distortion.rgbMaxAbs,
		AlphaExact:      value.distortion.alphaExact,
		Exact:           value.distortion.exact,
	}
}

func aggregateLosslessFixtures(fixtures []losslessFixtureReport) losslessAggregateReport {
	result := losslessAggregateReport{Files: len(fixtures)}
	for _, fixture := range fixtures {
		result.GoWebPBytes += fixture.GoWebP.EncodedBytes
		result.CWebPBytes += fixture.CWebP.EncodedBytes
		result.GoAverageEncodeNS += fixture.GoWebP.AverageEncodeNS
		result.CWebPAverageNS += fixture.CWebP.AverageEncodeNS
		result.GoWebPLayout.Add(fixture.GoWebP.Layout)
		result.CWebPLayout.Add(fixture.CWebP.Layout)
		switch {
		case fixture.GoWebP.EncodedBytes < fixture.CWebP.EncodedBytes:
			result.GoSmaller++
		case fixture.GoWebP.EncodedBytes > fixture.CWebP.EncodedBytes:
			result.CWebPSmaller++
		default:
			result.EqualSize++
		}
	}
	result.GoSizeDeltaBytes = result.GoWebPBytes - result.CWebPBytes
	if result.CWebPBytes != 0 {
		result.GoSizeDeltaPct = 100 * float64(result.GoSizeDeltaBytes) / float64(result.CWebPBytes)
	}
	return result
}

func writeLosslessReport(path string, report losslessComparisonReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
