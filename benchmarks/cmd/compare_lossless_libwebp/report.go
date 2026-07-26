package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/mayahiro/go-webp/internal/benchmarkbitstream"
)

const losslessComparisonReportSchemaVersion = 2
const losslessComparisonWarmupRuns = 1

// losslessComparisonReport intentionally records anonymous fixture identities instead of source paths
type losslessComparisonReport struct {
	SchemaVersion int                         `json:"schema_version"`
	Configuration losslessReportConfiguration `json:"configuration"`
	Fixtures      []losslessFixtureReport     `json:"fixtures"`
	Aggregate     losslessAggregateReport     `json:"aggregate"`
}

type losslessReportConfiguration struct {
	Runs                int      `json:"runs"`
	WarmupRuns          int      `json:"warmup_runs"`
	TimingStatistic     string   `json:"timing_statistic"`
	OutputHashAlgorithm string   `json:"output_hash_algorithm"`
	CWebPVersion        string   `json:"cwebp_version"`
	CWebPMethod         int      `json:"cwebp_method"`
	CWebPQuality        int      `json:"cwebp_quality"`
	CWebPArguments      []string `json:"cwebp_arguments"`
	CWebPInputFormat    string   `json:"cwebp_input_format"`
	DWebPVersion        string   `json:"dwebp_version"`
	GoVersion           string   `json:"go_version"`
	GOOS                string   `json:"goos"`
	GOARCH              string   `json:"goarch"`
	GOMAXPROCS          int      `json:"gomaxprocs"`
	GoCommit            string   `json:"go_commit"`
	GoDirty             bool     `json:"go_dirty"`
	CPUModel            string   `json:"cpu_model"`
	OSVersion           string   `json:"os_version"`
	GoMode              string   `json:"go_mode"`
	GoTimingScope       string   `json:"go_timing_scope"`
	CWebPTimingScope    string   `json:"cwebp_timing_scope"`
	Corpus              string   `json:"corpus"`
	CorpusSHA256        string   `json:"corpus_sha256,omitempty"`
	CorpusSplit         string   `json:"corpus_split"`
	HoldoutPercent      int      `json:"holdout_percent,omitempty"`
	FixtureFilter       []string `json:"fixture_filter,omitempty"`
}

type losslessFixtureReport struct {
	Name               string         `json:"name"`
	SourceFormat       string         `json:"source_format,omitempty"`
	SourceOriginFormat string         `json:"source_origin_format,omitempty"`
	CWebPInputFormat   string         `json:"cwebp_input_format"`
	Split              string         `json:"split,omitempty"`
	HasAlpha           bool           `json:"has_alpha"`
	Width              int            `json:"width"`
	Height             int            `json:"height"`
	GoWebP             losslessSample `json:"go_webp"`
	CWebP              losslessSample `json:"cwebp"`
}

type losslessSample struct {
	EncodedBytes    int64                             `json:"encoded_bytes"`
	AverageEncodeNS int64                             `json:"average_encode_ns"`
	Timing          losslessTimingSummary             `json:"timing"`
	OutputSHA256    string                            `json:"output_sha256"`
	Layout          benchmarkbitstream.LosslessLayout `json:"layout"`
	RGBMAE          float64                           `json:"rgb_mae"`
	RGBMaxAbs       int                               `json:"rgb_max_abs"`
	AlphaExact      bool                              `json:"alpha_exact"`
	Exact           bool                              `json:"exact"`
}

type losslessTimingSummary struct {
	Runs       int   `json:"runs"`
	WarmupRuns int   `json:"warmup_runs"`
	MedianNS   int64 `json:"median_ns"`
	MinNS      int64 `json:"min_ns"`
	MaxNS      int64 `json:"max_ns"`
}

type losslessAggregateSummary struct {
	Files                int                               `json:"files"`
	GoWebPBytes          int64                             `json:"go_webp_bytes"`
	CWebPBytes           int64                             `json:"cwebp_bytes"`
	GoSizeDeltaBytes     int64                             `json:"go_size_delta_bytes"`
	GoSizeDeltaPct       float64                           `json:"go_size_delta_percent"`
	GoAverageEncodeNS    int64                             `json:"go_average_encode_ns"`
	CWebPAverageNS       int64                             `json:"cwebp_average_process_ns"`
	GoMedianTotalNS      int64                             `json:"go_encode_median_total_ns"`
	CWebPMedianTotalNS   int64                             `json:"cwebp_process_median_total_ns"`
	GoSmaller            int                               `json:"go_smaller"`
	CWebPSmaller         int                               `json:"cwebp_smaller"`
	EqualSize            int                               `json:"equal_size"`
	GoAlphaViolations    int                               `json:"go_alpha_exact_violations"`
	CWebPAlphaViolations int                               `json:"cwebp_alpha_exact_violations"`
	GoExactViolations    int                               `json:"go_exact_violations"`
	CWebPExactViolations int                               `json:"cwebp_exact_violations"`
	GoWebPLayout         benchmarkbitstream.LosslessLayout `json:"go_webp_layout"`
	CWebPLayout          benchmarkbitstream.LosslessLayout `json:"cwebp_layout"`
}

type losslessAggregateReport struct {
	losslessAggregateSummary
	BySourceOriginFormat map[string]losslessAggregateSummary `json:"by_source_origin_format"`
	ByAlpha              map[string]losslessAggregateSummary `json:"by_alpha"`
}

func losslessSampleFromResult(value result) losslessSample {
	return losslessSample{
		EncodedBytes:    value.size,
		AverageEncodeNS: value.avg.Nanoseconds(),
		Timing:          value.timing,
		OutputSHA256:    value.outputSHA256,
		Layout:          value.layout,
		RGBMAE:          value.distortion.rgbMAE,
		RGBMaxAbs:       value.distortion.rgbMaxAbs,
		AlphaExact:      value.distortion.alphaExact,
		Exact:           value.distortion.exact,
	}
}

func aggregateLosslessFixtures(fixtures []losslessFixtureReport) losslessAggregateReport {
	result := losslessAggregateReport{
		losslessAggregateSummary: aggregateLosslessSummary(fixtures),
		BySourceOriginFormat:     make(map[string]losslessAggregateSummary),
		ByAlpha:                  make(map[string]losslessAggregateSummary),
	}
	byFormat := make(map[string][]losslessFixtureReport)
	byAlpha := make(map[string][]losslessFixtureReport)
	for _, fixture := range fixtures {
		format := fixture.SourceOriginFormat
		if format == "" {
			format = fixture.SourceFormat
		}
		if format == "" {
			format = "unknown"
		}
		byFormat[format] = append(byFormat[format], fixture)
		alpha := "opaque"
		if fixture.HasAlpha {
			alpha = "alpha"
		}
		byAlpha[alpha] = append(byAlpha[alpha], fixture)
	}
	for name, grouped := range byFormat {
		result.BySourceOriginFormat[name] = aggregateLosslessSummary(grouped)
	}
	for name, grouped := range byAlpha {
		result.ByAlpha[name] = aggregateLosslessSummary(grouped)
	}
	return result
}

func aggregateLosslessSummary(fixtures []losslessFixtureReport) losslessAggregateSummary {
	result := losslessAggregateSummary{Files: len(fixtures)}
	for _, fixture := range fixtures {
		result.GoWebPBytes += fixture.GoWebP.EncodedBytes
		result.CWebPBytes += fixture.CWebP.EncodedBytes
		result.GoAverageEncodeNS += fixture.GoWebP.AverageEncodeNS
		result.CWebPAverageNS += fixture.CWebP.AverageEncodeNS
		result.GoMedianTotalNS += fixture.GoWebP.Timing.MedianNS
		result.CWebPMedianTotalNS += fixture.CWebP.Timing.MedianNS
		result.GoWebPLayout.Add(fixture.GoWebP.Layout)
		result.CWebPLayout.Add(fixture.CWebP.Layout)
		if !fixture.GoWebP.AlphaExact {
			result.GoAlphaViolations++
		}
		if !fixture.CWebP.AlphaExact {
			result.CWebPAlphaViolations++
		}
		if !fixture.GoWebP.Exact {
			result.GoExactViolations++
		}
		if !fixture.CWebP.Exact {
			result.CWebPExactViolations++
		}
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

func summarizeLosslessTiming(durations []time.Duration) losslessTimingSummary {
	result := losslessTimingSummary{
		Runs:       len(durations),
		WarmupRuns: losslessComparisonWarmupRuns,
	}
	if len(durations) == 0 {
		return result
	}
	sorted := slices.Clone(durations)
	slices.Sort(sorted)
	result.MinNS = sorted[0].Nanoseconds()
	result.MaxNS = sorted[len(sorted)-1].Nanoseconds()
	middle := len(sorted) / 2
	if len(sorted)&1 != 0 {
		result.MedianNS = sorted[middle].Nanoseconds()
	} else {
		lower := sorted[middle-1].Nanoseconds()
		upper := sorted[middle].Nanoseconds()
		result.MedianNS = lower + (upper-lower)/2
	}
	return result
}

func losslessOutputSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest)
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
