package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	webp "github.com/mayahiro/go-webp"
)

// These unit tests do not invoke the optional external cwebp and dwebp binaries
func TestParseQualitiesSortsAndDeduplicates(t *testing.T) {
	got, err := parseQualities("90, 25,75,25")
	if err != nil {
		t.Fatalf("parseQualities failed: %v", err)
	}
	want := []int{25, 75, 90}
	if !slices.Equal(got, want) {
		t.Fatalf("qualities = %v, want %v", got, want)
	}
}

func TestParseGoMode(t *testing.T) {
	for _, tc := range []struct {
		value string
		mode  webp.Mode
		name  string
	}{
		{value: "default", mode: webp.ModeDefault, name: "default"},
		{value: "best", mode: webp.ModeBestCompression, name: "best"},
		{value: "best-compression", mode: webp.ModeBestCompression, name: "best"},
		{value: "low-memory", mode: webp.ModeLowMemory, name: "low-memory"},
	} {
		gotMode, gotName, err := parseGoMode(tc.value)
		if err != nil {
			t.Fatalf("parseGoMode(%q) failed: %v", tc.value, err)
		}
		if gotMode != tc.mode || gotName != tc.name {
			t.Fatalf("parseGoMode(%q) = (%d, %q), want (%d, %q)", tc.value, gotMode, gotName, tc.mode, tc.name)
		}
	}
	if _, _, err := parseGoMode("unknown"); err == nil {
		t.Fatal("parseGoMode accepted an unknown mode")
	}
}

func TestParseQualitiesRejectsOutOfRangeValues(t *testing.T) {
	for _, value := range []string{"", "0", "101", "25,,75", "quality"} {
		if _, err := parseQualities(value); err == nil {
			t.Fatalf("parseQualities(%q) succeeded", value)
		}
	}
}

func TestSummarizeTimingReportsMedianAndRange(t *testing.T) {
	for _, tc := range []struct {
		name       string
		durations  []time.Duration
		wantMedian int64
		wantMin    int64
		wantMax    int64
	}{
		{name: "odd", durations: []time.Duration{9, 1, 5}, wantMedian: 5, wantMin: 1, wantMax: 9},
		{name: "even", durations: []time.Duration{9, 1, 7, 3}, wantMedian: 5, wantMin: 1, wantMax: 9},
		{name: "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeTiming(tc.durations)
			if got.Runs != len(tc.durations) || got.WarmupRuns != comparisonWarmupRuns || got.MedianNS != tc.wantMedian || got.MinNS != tc.wantMin || got.MaxNS != tc.wantMax {
				t.Fatalf("timing = %#v, want runs/warmup/median/min/max = %d/%d/%d/%d/%d", got, len(tc.durations), comparisonWarmupRuns, tc.wantMedian, tc.wantMin, tc.wantMax)
			}
		})
	}
}

func TestOutputSHA256IsStableAndContentDependent(t *testing.T) {
	first := outputSHA256([]byte("first"))
	if len(first) != 64 || first != outputSHA256([]byte("first")) {
		t.Fatalf("unstable SHA-256 = %q", first)
	}
	if first == outputSHA256([]byte("second")) {
		t.Fatal("different outputs have the same SHA-256")
	}
}

func TestMeasureDistortionReportsExactAndChangedChannels(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 40})
	source.SetNRGBA(1, 0, color.NRGBA{R: 50, G: 60, B: 70, A: 80})

	exact, err := measureDistortion(source, source)
	if err != nil {
		t.Fatalf("measureDistortion exact failed: %v", err)
	}
	if !exact.RGBExact || !exact.AlphaExact || exact.RGBPSNRDB != nil || exact.YPSNRDB != nil || exact.UVPSNRDB != nil {
		t.Fatalf("exact metrics = %#v", exact)
	}
	if exact.YSSIM != 1 || exact.YSSIMDB != nil {
		t.Fatalf("exact SSIM metrics = %#v", exact)
	}

	changed := image.NewNRGBA(source.Bounds())
	copy(changed.Pix, source.Pix)
	pixel := changed.NRGBAAt(1, 0)
	pixel.R += 20
	pixel.A++
	changed.SetNRGBA(1, 0, pixel)
	metrics, err := measureDistortion(source, changed)
	if err != nil {
		t.Fatalf("measureDistortion changed failed: %v", err)
	}
	if metrics.RGBExact || metrics.AlphaExact {
		t.Fatalf("changed metrics reported exact: %#v", metrics)
	}
	if math.Abs(metrics.RGBMAE-20.0/6.0) > 1e-12 {
		t.Fatalf("rgb_mae = %v, want %v", metrics.RGBMAE, 20.0/6.0)
	}
	if metrics.AlphaMAE != 0.5 {
		t.Fatalf("alpha_mae = %v, want 0.5", metrics.AlphaMAE)
	}
	if metrics.RGBPSNRDB == nil {
		t.Fatal("changed RGB PSNR is exact")
	}
	if metrics.YSSIM >= 1 || metrics.YSSIMDB == nil {
		t.Fatalf("changed SSIM metrics = %#v", metrics)
	}
}

func TestBuildMatchesUsesNearestSizeAndQuality(t *testing.T) {
	goSamples := []sample{{
		Quality:      75,
		EncodedBytes: 1000,
		Distortion:   distortionMetrics{RGBPSNRDB: float64Pointer(30), YSSIMDB: float64Pointer(30)},
	}}
	cwebpSamples := []sample{
		{Quality: 70, EncodedBytes: 990, Distortion: distortionMetrics{RGBPSNRDB: float64Pointer(28), YSSIMDB: float64Pointer(28)}},
		{Quality: 80, EncodedBytes: 1010, Distortion: distortionMetrics{RGBPSNRDB: float64Pointer(31), YSSIMDB: float64Pointer(31)}},
		{Quality: 90, EncodedBytes: 1200, Distortion: distortionMetrics{RGBPSNRDB: float64Pointer(30.5), YSSIMDB: float64Pointer(30.5)}},
	}
	matchedSize, matchedQuality := buildMatches(goSamples, cwebpSamples)
	if len(matchedSize) != 1 || matchedSize[0].CWebPQuality != 80 {
		t.Fatalf("matched size = %#v, want cwebp quality 80", matchedSize)
	}
	if len(matchedQuality) != 1 || matchedQuality[0].CWebPQuality != 90 {
		t.Fatalf("matched quality = %#v, want cwebp quality 90", matchedQuality)
	}
}

func TestMakePointMatchUsesGoMinusCWebPDirection(t *testing.T) {
	for _, tc := range []struct {
		name        string
		goBytes     int
		cwebpBytes  int
		wantBytes   int
		wantPercent float64
	}{
		{name: "go larger", goBytes: 1000, cwebpBytes: 800, wantBytes: 200, wantPercent: 25},
		{name: "go smaller", goBytes: 800, cwebpBytes: 1000, wantBytes: -200, wantPercent: -20},
		{name: "zero reference", goBytes: 800, cwebpBytes: 0, wantBytes: 800, wantPercent: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := makePointMatch(
				sample{EncodedBytes: tc.goBytes},
				sample{EncodedBytes: tc.cwebpBytes},
			)
			if got.GoMinusCWebPBytes != tc.wantBytes || math.Abs(got.GoMinusCWebPPercent-tc.wantPercent) > 1e-12 {
				t.Fatalf("delta = %d/%v, want %d/%v", got.GoMinusCWebPBytes, got.GoMinusCWebPPercent, tc.wantBytes, tc.wantPercent)
			}
		})
	}
}

func TestMakePointMatchReportsDirectionalMetricDeltas(t *testing.T) {
	goSample := sample{Distortion: distortionMetrics{
		RGBPSNRDB:                  float64Pointer(31),
		YPSNRDB:                    float64Pointer(32),
		UVPSNRDB:                   float64Pointer(33),
		YSSIMDB:                    float64Pointer(34),
		CompositeOverBlackPSNRDB:   float64Pointer(35),
		CompositeOverWhitePSNRDB:   float64Pointer(36),
		CompositeOverCheckerPSNRDB: float64Pointer(37),
		AlphaExact:                 true,
	}}
	cwebpSample := sample{Distortion: distortionMetrics{
		RGBPSNRDB:                  float64Pointer(30),
		YPSNRDB:                    float64Pointer(30),
		UVPSNRDB:                   float64Pointer(30),
		YSSIMDB:                    float64Pointer(30),
		CompositeOverBlackPSNRDB:   float64Pointer(30),
		CompositeOverWhitePSNRDB:   float64Pointer(30),
		CompositeOverCheckerPSNRDB: float64Pointer(30),
		AlphaExact:                 false,
	}}

	got := makePointMatch(goSample, cwebpSample)
	for name, tc := range map[string]struct {
		value *float64
		want  float64
	}{
		"RGB PSNR":          {value: got.GoMinusCWebPRGBPSNRDB, want: 1},
		"Y PSNR":            {value: got.GoMinusCWebPYPSNRDB, want: 2},
		"UV PSNR":           {value: got.GoMinusCWebPUVPSNRDB, want: 3},
		"Y SSIM":            {value: got.GoMinusCWebPYSSIMDB, want: 4},
		"composite black":   {value: got.GoMinusCWebPCompositeBlackPSNRDB, want: 5},
		"composite white":   {value: got.GoMinusCWebPCompositeWhitePSNRDB, want: 6},
		"composite checker": {value: got.GoMinusCWebPCompositeCheckerPSNRDB, want: 7},
	} {
		if tc.value == nil || math.Abs(*tc.value-tc.want) > 1e-12 {
			t.Errorf("%s delta = %v, want %v", name, tc.value, tc.want)
		}
	}
	if !got.GoAlphaExact || got.CWebPAlphaExact {
		t.Fatalf("alpha exact = %t/%t, want true/false", got.GoAlphaExact, got.CWebPAlphaExact)
	}
}

func TestGoMinusCWebPMetricHandlesExactValues(t *testing.T) {
	bothExact := goMinusCWebPMetric(nil, nil)
	if bothExact == nil || *bothExact != 0 {
		t.Fatalf("both exact delta = %v, want 0", bothExact)
	}
	finite := float64Pointer(30)
	if got := goMinusCWebPMetric(nil, finite); got != nil {
		t.Fatalf("exact minus finite delta = %v, want nil", got)
	}
	if got := goMinusCWebPMetric(finite, nil); got != nil {
		t.Fatalf("finite minus exact delta = %v, want nil", got)
	}
}

func TestComparisonReportSchemaUsesDirectionalDeltaFields(t *testing.T) {
	if comparisonReportSchemaVersion != 8 {
		t.Fatalf("schema version = %d, want 8", comparisonReportSchemaVersion)
	}
	data, err := json.Marshal(struct {
		Configuration reportConfiguration `json:"configuration"`
		Sample        sample              `json:"sample"`
		Point         pointMatch          `json:"point"`
		Aggregate     aggregateReport     `json:"aggregate"`
	}{Aggregate: aggregateReport{NominalQuality: []aggregatePoint{{}}}})
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)
	for _, field := range []string{
		"go_minus_cwebp_bytes",
		"go_minus_cwebp_percent",
		"go_minus_cwebp_rgb_psnr_db",
		"go_minus_cwebp_y_psnr_db",
		"go_minus_cwebp_uv_psnr_db",
		"go_minus_cwebp_y_ssim_db",
		"go_minus_cwebp_composite_black_psnr_db",
		"go_minus_cwebp_composite_white_psnr_db",
		"go_minus_cwebp_composite_checker_psnr_db",
		"fixture_mean",
		"pixel_weighted",
		"byte_weighted",
		"rate_distortion",
		"matched_size",
		"bjontegaard",
		"bd_rate_rgb_psnr_percent",
		"bd_rate_y_ssim_percent",
		"bd_psnr_db",
		"bd_ssim",
		"by_source_format",
		"by_alpha",
		"gomaxprocs",
		"timing_statistic",
		"output_hash_algorithm",
		"timing",
		"output_sha256",
		"median_ns",
		"min_ns",
		"max_ns",
		"go_encode_median_total_ns",
		"cwebp_process_median_total_ns",
		"curve_interpolation",
		"curve_extrapolation",
		"bjontegaard_min_points",
	} {
		if !strings.Contains(encoded, `"`+field+`"`) {
			t.Errorf("schema is missing %q: %s", field, encoded)
		}
	}
	for _, oldField := range []string{
		"size_delta_bytes",
		"size_delta_percent",
		"rgb_psnr_delta_db",
		"y_ssim_delta_db",
		"mean_y_ssim_delta",
		"mean_go_y_ssim",
		"mean_cwebp_y_ssim",
		"mean_go_minus_cwebp_y_ssim",
		"average_encode_ns",
		"go_encode_total_ns",
		"cwebp_process_total_ns",
	} {
		if strings.Contains(encoded, `"`+oldField+`"`) {
			t.Errorf("schema still contains %q: %s", oldField, encoded)
		}
	}
}

func TestAggregateComparisonReportsNominalAndMatchedQuality(t *testing.T) {
	fixtures := []fixtureReport{
		{
			SourceFormat: "jpeg",
			Width:        1,
			Height:       1,
			GoWebP: []sample{{
				Quality:      50,
				EncodedBytes: 100,
				Timing:       timingSummary{MedianNS: 10},
				Distortion:   aggregateTestMetrics(0.9, 10, 100, true),
			}},
			CWebP: []sample{
				{Quality: 50, EncodedBytes: 90, Timing: timingSummary{MedianNS: 4}, Distortion: aggregateTestMetrics(0.88, 9, 121, true)},
				{Quality: 60, EncodedBytes: 110, Timing: timingSummary{MedianNS: 5}, Distortion: aggregateTestMetrics(0.901, 10.01, 81, true)},
			},
		},
		{
			SourceFormat: "png",
			HasAlpha:     true,
			Width:        3,
			Height:       1,
			GoWebP: []sample{{
				Quality:      50,
				EncodedBytes: 200,
				Timing:       timingSummary{MedianNS: 20},
				Distortion:   aggregateTestMetrics(0.8, 7, 25, false),
			}},
			CWebP: []sample{
				{Quality: 40, EncodedBytes: 180, Timing: timingSummary{MedianNS: 6}, Distortion: aggregateTestMetrics(0.799, 6.99, 36, true)},
				{Quality: 50, EncodedBytes: 210, Timing: timingSummary{MedianNS: 7}, Distortion: aggregateTestMetrics(0.81, 7.2, 16, true)},
			},
		},
	}

	got := aggregateComparison(fixtures, []int{50, 75})
	if len(got.NominalQuality) != 2 || len(got.MatchedQuality) != 2 {
		t.Fatalf("aggregate lengths = %d/%d", len(got.NominalQuality), len(got.MatchedQuality))
	}
	nominal := got.NominalQuality[0]
	if nominal.Fixtures != 2 || nominal.Pixels != 4 || nominal.AlphaFixtures != 1 || nominal.GoBytes != 300 || nominal.CWebPBytes != 300 || nominal.GoMinusCWebPBytes != 0 {
		t.Fatalf("nominal aggregate = %#v", nominal)
	}
	if nominal.GoSmaller != 1 || nominal.CWebPSmaller != 1 || nominal.GoEncodeMedianTotalNS != 30 || nominal.CWebPProcessMedianTotalNS != 11 {
		t.Fatalf("nominal counters = %#v", nominal)
	}
	if nominal.ByteWeighted.GoWebP != 300 || nominal.ByteWeighted.CWebP != 300 || nominal.ByteWeighted.GoMinusCWebP != 0 || nominal.ByteWeighted.GoMinusCWebPPct != 0 {
		t.Fatalf("byte-weighted aggregate = %#v", nominal.ByteWeighted)
	}
	assertAggregateMetric(t, "fixture mean Y SSIM", nominal.FixtureMean.YSSIM, 0.85, 0.845)
	assertAggregateMetric(t, "fixture mean RGB PSNR", nominal.FixtureMean.RGBPSNRDB, *aggregatePSNRDB(62.5), *aggregatePSNRDB(68.5))
	assertAggregateMetric(t, "pixel-weighted Y SSIM", nominal.PixelWeighted.YSSIM, 0.825, 0.8275)
	assertAggregateMetric(t, "pixel-weighted RGB PSNR", nominal.PixelWeighted.RGBPSNRDB, *aggregatePSNRDB(43.75), *aggregatePSNRDB(42.25))
	if nominal.GoAlphaExactViolations != 1 || nominal.CWebPAlphaExactViolations != 0 {
		t.Fatalf("alpha exact violations = %d/%d, want 1/0", nominal.GoAlphaExactViolations, nominal.CWebPAlphaExactViolations)
	}
	for name, series := range map[string]aggregateSeries{
		"jpeg":   got.BySourceFormat["jpeg"],
		"png":    got.BySourceFormat["png"],
		"opaque": got.ByAlpha["opaque"],
		"alpha":  got.ByAlpha["alpha"],
	} {
		if len(series.NominalQuality) != 2 || series.NominalQuality[0].Fixtures != 1 {
			t.Errorf("%s aggregate = %#v", name, series)
		}
	}

	matched := got.MatchedQuality[0]
	if matched.CWebPBytes != 290 || matched.GoMinusCWebPBytes != 10 {
		t.Fatalf("matched sizes = %#v", matched)
	}
	if math.Abs(matched.GoMinusCWebPPercent-1000.0/290.0) > 1e-12 {
		t.Fatalf("matched size delta = %v", matched.GoMinusCWebPPercent)
	}
	if matched.MinimumCWebPQuality != 40 || matched.MaximumCWebPQuality != 60 || matched.MeanCWebPQuality != 50 {
		t.Fatalf("matched qualities = %#v", matched)
	}
	assertAggregateMetric(t, "matched fixture mean Y SSIM", matched.FixtureMean.YSSIM, 0.85, 0.85)
	assertAggregateMetric(t, "matched pixel-weighted Y SSIM", matched.PixelWeighted.YSSIM, 0.825, 0.8245)

	empty := got.MatchedQuality[1]
	if empty.Fixtures != 0 || empty.MinimumCWebPQuality != 0 || empty.GoMinusCWebPPercent != 0 {
		t.Fatalf("empty aggregate = %#v", empty)
	}
}

func aggregateTestMetrics(ySSIM float64, ySSIMDB float64, mse float64, alphaExact bool) distortionMetrics {
	return distortionMetrics{
		RGBMSE:                  mse,
		YMSE:                    mse / 2,
		UVMSE:                   mse / 4,
		YSSIM:                   ySSIM,
		YSSIMDB:                 float64Pointer(ySSIMDB),
		AlphaExact:              alphaExact,
		CompositeOverBlackMSE:   mse / 5,
		CompositeOverWhiteMSE:   mse / 6,
		CompositeOverCheckerMSE: mse / 7,
	}
}

func assertAggregateMetric(t *testing.T, name string, got aggregateMetricComparison, wantGo float64, wantCWebP float64) {
	t.Helper()
	if got.Go == nil || got.CWebP == nil || got.GoMinusCWebP == nil {
		t.Fatalf("%s = %#v, want finite values", name, got)
	}
	if math.Abs(*got.Go-wantGo) > 1e-12 || math.Abs(*got.CWebP-wantCWebP) > 1e-12 || math.Abs(*got.GoMinusCWebP-(wantGo-wantCWebP)) > 1e-12 {
		t.Fatalf("%s = %#v, want %v/%v/%v", name, got, wantGo, wantCWebP, wantGo-wantCWebP)
	}
}

func TestLoadComparisonFixturesUsesAnonymousCorpusIdentity(t *testing.T) {
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

	fixtures, corpus, err := loadComparisonFixtures(root, "production", "all", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 1 {
		t.Fatalf("fixtures = %d, want 1", len(fixtures))
	}
	if strings.Contains(fixtures[0].name, "private") || len(fixtures[0].name) != 16 {
		t.Fatalf("fixture name is not anonymous: %q", fixtures[0].name)
	}
	if fixtures[0].format != "jpeg" || fixtures[0].hasAlpha {
		t.Fatalf("fixture metadata = %#v", fixtures[0])
	}
	if corpus.name != "production" || len(corpus.sha256) != 64 || corpus.split != "all" {
		t.Fatalf("corpus configuration = %#v", corpus)
	}
}

func TestLoadGeneratedComparisonFixturesRecordsFormatAndAlpha(t *testing.T) {
	fixtures, corpus, err := loadComparisonFixtures("", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if corpus.name != "generated-standard" || corpus.split != "all" {
		t.Fatalf("corpus configuration = %#v", corpus)
	}
	alphaFixtures := 0
	for _, fixture := range fixtures {
		if fixture.format != "generated" || fixture.split != "all" {
			t.Errorf("fixture metadata = %#v", fixture)
		}
		if fixture.hasAlpha {
			alphaFixtures++
		}
	}
	if alphaFixtures != 1 {
		t.Fatalf("alpha fixtures = %d, want 1", alphaFixtures)
	}
}

func float64Pointer(value float64) *float64 {
	return &value
}
