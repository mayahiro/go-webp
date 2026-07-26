package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	webp "github.com/mayahiro/go-webp"
	"github.com/mayahiro/go-webp/internal/benchmarkbitstream"
)

// These unit tests do not invoke the optional external cwebp and dwebp binaries
func TestMakeComparisonConfig(t *testing.T) {
	best, err := makeComparisonConfig("best", 100, 6)
	if err != nil {
		t.Fatalf("best config failed: %v", err)
	}
	if best.goOptions.Mode != webp.ModeBestCompression || !best.exact {
		t.Fatalf("best config = %#v", best)
	}
	if !slices.Equal(best.cwebpArgs, []string{"-lossless", "-exact", "-q", "100", "-m", "6"}) {
		t.Fatalf("best cwebp args = %q", best.cwebpArgs)
	}
	if got := reportCWebPArguments(best); !slices.Equal(got, []string{"-quiet", "-lossless", "-exact", "-q", "100", "-m", "6", "<input.png>", "-o", "<output.webp>"}) {
		t.Fatalf("reported cwebp arguments = %q", got)
	}

	near, err := makeComparisonConfig("near-lossless", 75, 4)
	if err != nil {
		t.Fatalf("near-lossless config failed: %v", err)
	}
	if near.goOptions.Mode != webp.ModeNearLossless || near.goOptions.Quality != 75 || near.exact {
		t.Fatalf("near-lossless config = %#v", near)
	}
	if !slices.Equal(near.cwebpArgs, []string{"-lossless", "-exact", "-q", "75", "-m", "4", "-near_lossless", "75"}) {
		t.Fatalf("near-lossless cwebp args = %q", near.cwebpArgs)
	}
}

func TestSummarizeLosslessTimingReportsMedianAndRange(t *testing.T) {
	for _, tc := range []struct {
		name       string
		durations  []time.Duration
		wantMedian int64
		wantMin    int64
		wantMax    int64
	}{
		{name: "empty"},
		{name: "odd", durations: []time.Duration{9, 1, 5}, wantMedian: 5, wantMin: 1, wantMax: 9},
		{name: "even", durations: []time.Duration{9, 1, 7, 3}, wantMedian: 5, wantMin: 1, wantMax: 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeLosslessTiming(tc.durations)
			if got.Runs != len(tc.durations) || got.WarmupRuns != losslessComparisonWarmupRuns || got.MedianNS != tc.wantMedian || got.MinNS != tc.wantMin || got.MaxNS != tc.wantMax {
				t.Fatalf("timing = %#v", got)
			}
		})
	}
	if got := averageLosslessDuration([]time.Duration{3, 6, 9}); got != 6 {
		t.Fatalf("average duration = %d, want 6", got)
	}
}

func TestRunDeterministicLosslessEncodesRejectsChangedOutput(t *testing.T) {
	outputs := [][]byte{[]byte("stable"), []byte("stable"), []byte("changed")}
	call := 0
	_, _, err := runDeterministicLosslessEncodes("fixture/encoder", 2, func() ([]byte, time.Duration, error) {
		output := outputs[call]
		call++
		return output, time.Duration(call), nil
	})
	if err == nil || !strings.Contains(err.Error(), "changed after warm-up on run 2") {
		t.Fatalf("error = %v", err)
	}
}

func TestLosslessOutputSHA256IsStableAndContentDependent(t *testing.T) {
	first := losslessOutputSHA256([]byte("first"))
	if len(first) != 64 || first != losslessOutputSHA256([]byte("first")) {
		t.Fatalf("unstable SHA-256 = %q", first)
	}
	if first == losslessOutputSHA256([]byte("second")) {
		t.Fatal("different output has the same SHA-256")
	}
}

func TestMeasureImageReportsRGBAndAlphaError(t *testing.T) {
	want := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	want.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 40})
	got := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	got.SetNRGBA(0, 0, color.NRGBA{R: 12, G: 19, B: 30, A: 41})

	metrics, err := measureImage(got, want)
	if err != nil {
		t.Fatalf("measureImage failed: %v", err)
	}
	if metrics.rgbMAE != 1 || metrics.rgbMaxAbs != 2 || metrics.alphaExact || metrics.exact {
		t.Fatalf("metrics = %#v", metrics)
	}
}

func TestFilterLosslessFixtures(t *testing.T) {
	fixtures := []fixture{{name: "a"}, {name: "b"}, {name: "c"}}
	got, filter, err := filterLosslessFixtures(fixtures, "c, a,c")
	if err != nil {
		t.Fatal(err)
	}
	if names := []string{got[0].name, got[1].name}; !slices.Equal(names, []string{"a", "c"}) {
		t.Fatalf("filtered fixtures = %v", names)
	}
	if !slices.Equal(filter, []string{"c", "a"}) {
		t.Fatalf("recorded filter = %v", filter)
	}
	if _, _, err := filterLosslessFixtures(fixtures, "missing"); err == nil {
		t.Fatal("missing fixture filter succeeded")
	}
}

func TestNormalizeLosslessCorpusSplit(t *testing.T) {
	for _, tc := range []struct {
		input       string
		wantStorage string
		wantReport  string
	}{
		{input: "development", wantStorage: "train", wantReport: "development"},
		{input: "train", wantStorage: "train", wantReport: "development"},
		{input: "validation", wantStorage: "holdout", wantReport: "validation"},
		{input: "holdout", wantStorage: "holdout", wantReport: "validation"},
		{input: "all", wantStorage: "all", wantReport: "all"},
	} {
		storage, report, err := normalizeLosslessCorpusSplit(tc.input)
		if err != nil || storage != tc.wantStorage || report != tc.wantReport {
			t.Fatalf("normalize %q = %q/%q/%v", tc.input, storage, report, err)
		}
	}
	if _, _, err := normalizeLosslessCorpusSplit("unknown"); err == nil {
		t.Fatal("invalid split succeeded")
	}
}

func TestHiddenRGBAlphaFixtureContainsTransparentNonZeroRGB(t *testing.T) {
	img := hiddenRGBAlphaFixture()
	found := false
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			pixel := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if pixel.A == 0 && (pixel.R != 0 || pixel.G != 0 || pixel.B != 0) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("fixture has no transparent pixel with non-zero hidden RGB")
	}
}

func TestLoadLosslessComparisonFixturesUsesAnonymousCorpusIdentity(t *testing.T) {
	root := t.TempDir()
	const privateName = "private-customer-name.jpg"
	file, err := os.Create(filepath.Join(root, privateName))
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 23), G: uint8(y * 31), B: uint8(x*7 + y*11), A: 255})
		}
	}
	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	fixtures, corpus, err := loadLosslessComparisonFixtures(root, "production", "all", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 1 {
		t.Fatalf("fixtures = %d, want 1", len(fixtures))
	}
	if strings.Contains(fixtures[0].name, "private") || len(fixtures[0].name) != 16 {
		t.Fatalf("fixture name is not anonymous: %q", fixtures[0].name)
	}
	if corpus.name != "production" || len(corpus.sha256) != 64 || corpus.split != "all" || !corpus.private {
		t.Fatalf("corpus configuration = %#v", corpus)
	}
	if fixtures[0].format != "jpeg" || fixtures[0].split == "" {
		t.Fatalf("fixture metadata = %#v", fixtures[0])
	}
}

func TestAggregateLosslessFixtures(t *testing.T) {
	fixtures := []losslessFixtureReport{
		{
			SourceOriginFormat: "jpeg",
			GoWebP:             losslessSample{EncodedBytes: 90, AverageEncodeNS: 10, Timing: losslessTimingSummary{MedianNS: 9}, Layout: benchmarkbitstream.LosslessLayout{LiteralBits: 11}, AlphaExact: true, Exact: true},
			CWebP:              losslessSample{EncodedBytes: 100, AverageEncodeNS: 4, Timing: losslessTimingSummary{MedianNS: 3}, Layout: benchmarkbitstream.LosslessLayout{LiteralBits: 7}, AlphaExact: true, Exact: true},
		},
		{
			SourceOriginFormat: "png",
			HasAlpha:           true,
			GoWebP:             losslessSample{EncodedBytes: 120, AverageEncodeNS: 20, Timing: losslessTimingSummary{MedianNS: 19}, Layout: benchmarkbitstream.LosslessLayout{CopyBits: 13}, AlphaExact: true, Exact: true},
			CWebP:              losslessSample{EncodedBytes: 100, AverageEncodeNS: 6, Timing: losslessTimingSummary{MedianNS: 5}, Layout: benchmarkbitstream.LosslessLayout{CopyBits: 5}, AlphaExact: false, Exact: false},
		},
		{
			SourceOriginFormat: "png",
			GoWebP:             losslessSample{EncodedBytes: 50, AverageEncodeNS: 30, Timing: losslessTimingSummary{MedianNS: 29}, AlphaExact: true, Exact: true},
			CWebP:              losslessSample{EncodedBytes: 50, AverageEncodeNS: 8, Timing: losslessTimingSummary{MedianNS: 7}, AlphaExact: true, Exact: true},
		},
	}
	got := aggregateLosslessFixtures(fixtures)
	if got.Files != 3 || got.GoWebPBytes != 260 || got.CWebPBytes != 250 {
		t.Fatalf("aggregate sizes = %#v", got)
	}
	if got.GoSizeDeltaBytes != 10 || got.GoSizeDeltaPct != 4 {
		t.Fatalf("aggregate delta = %#v", got)
	}
	if got.GoAverageEncodeNS != 60 || got.CWebPAverageNS != 18 {
		t.Fatalf("aggregate time = %#v", got)
	}
	if got.GoMedianTotalNS != 57 || got.CWebPMedianTotalNS != 15 {
		t.Fatalf("aggregate median time = %#v", got)
	}
	if got.GoSmaller != 1 || got.CWebPSmaller != 1 || got.EqualSize != 1 {
		t.Fatalf("aggregate wins = %#v", got)
	}
	if got.GoWebPLayout.LiteralBits != 11 || got.GoWebPLayout.CopyBits != 13 || got.CWebPLayout.LiteralBits != 7 || got.CWebPLayout.CopyBits != 5 {
		t.Fatalf("aggregate layouts = %#v/%#v", got.GoWebPLayout, got.CWebPLayout)
	}
	if got.CWebPAlphaViolations != 1 || got.CWebPExactViolations != 1 || got.GoAlphaViolations != 0 || got.GoExactViolations != 0 {
		t.Fatalf("aggregate exact violations = %#v", got)
	}
	if got.BySourceOriginFormat["jpeg"].Files != 1 || got.BySourceOriginFormat["png"].Files != 2 || got.ByAlpha["alpha"].Files != 1 || got.ByAlpha["opaque"].Files != 2 {
		t.Fatalf("aggregate groups = formats:%#v alpha:%#v", got.BySourceOriginFormat, got.ByAlpha)
	}

	zero := aggregateLosslessFixtures([]losslessFixtureReport{{}})
	if zero.GoSizeDeltaPct != 0 {
		t.Fatalf("zero cwebp delta = %v", zero.GoSizeDeltaPct)
	}
}

func TestWriteLosslessReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "report.json")
	want := losslessComparisonReport{
		SchemaVersion: losslessComparisonReportSchemaVersion,
		Configuration: losslessReportConfiguration{
			Corpus:         "generated-standard",
			CWebPArguments: []string{"-lossless", "-exact", "-q", "75", "-m", "4"},
			CWebPQuality:   75,
			GOMAXPROCS:     4,
			GoCommit:       strings.Repeat("a", 40),
			CPUModel:       "test CPU",
			OSVersion:      "test OS",
		},
		Fixtures: []losslessFixtureReport{{
			Name:               "fixture",
			SourceFormat:       "png",
			SourceOriginFormat: "png",
			CWebPInputFormat:   "png",
			HasAlpha:           true,
			GoWebP:             losslessSample{OutputSHA256: strings.Repeat("b", 64)},
		}},
		Aggregate: losslessAggregateReport{losslessAggregateSummary: losslessAggregateSummary{Files: 1}},
	}
	if err := writeLosslessReport(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("report mode = %o, want 600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got losslessComparisonReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != want.SchemaVersion || got.Configuration.Corpus != want.Configuration.Corpus || got.Aggregate.Files != 1 {
		t.Fatalf("decoded report = %#v", got)
	}
	if got.Configuration.CWebPQuality != 75 || got.Configuration.GOMAXPROCS != 4 || got.Configuration.GoCommit != strings.Repeat("a", 40) || got.Configuration.CPUModel != "test CPU" || got.Configuration.OSVersion != "test OS" {
		t.Fatalf("decoded configuration = %#v", got.Configuration)
	}
	if len(got.Fixtures) != 1 || got.Fixtures[0].SourceFormat != "png" || got.Fixtures[0].SourceOriginFormat != "png" || !got.Fixtures[0].HasAlpha || len(got.Fixtures[0].GoWebP.OutputSHA256) != 64 {
		t.Fatalf("decoded fixture = %#v", got.Fixtures)
	}
}

func TestCPUModelFromProcCPUInfo(t *testing.T) {
	data := "processor : 0\nmodel name : Example CPU 123\n"
	if got := cpuModelFromProcCPUInfo(data); got != "Example CPU 123" {
		t.Fatalf("CPU model = %q", got)
	}
}

func TestCPUModelFromSystemProfiler(t *testing.T) {
	data := "Hardware:\n\n    Hardware Overview:\n\n      Chip: Apple Example\n"
	if got := cpuModelFromSystemProfiler(data); got != "Apple Example" {
		t.Fatalf("CPU model = %q", got)
	}
}
