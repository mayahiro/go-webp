package main

import (
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

func TestAggregateComparisonReportsNominalAndMatchedQuality(t *testing.T) {
	fixtures := []fixtureReport{
		{
			GoWebP: []sample{{
				Quality:         50,
				EncodedBytes:    100,
				AverageEncodeNS: 10,
				Distortion:      distortionMetrics{YSSIM: 0.9, YSSIMDB: float64Pointer(10)},
			}},
			CWebP: []sample{
				{Quality: 50, EncodedBytes: 90, AverageEncodeNS: 4, Distortion: distortionMetrics{YSSIM: 0.88, YSSIMDB: float64Pointer(9)}},
				{Quality: 60, EncodedBytes: 110, AverageEncodeNS: 5, Distortion: distortionMetrics{YSSIM: 0.901, YSSIMDB: float64Pointer(10.01)}},
			},
		},
		{
			GoWebP: []sample{{
				Quality:         50,
				EncodedBytes:    200,
				AverageEncodeNS: 20,
				Distortion:      distortionMetrics{YSSIM: 0.8, YSSIMDB: float64Pointer(7)},
			}},
			CWebP: []sample{
				{Quality: 40, EncodedBytes: 180, AverageEncodeNS: 6, Distortion: distortionMetrics{YSSIM: 0.799, YSSIMDB: float64Pointer(6.99)}},
				{Quality: 50, EncodedBytes: 210, AverageEncodeNS: 7, Distortion: distortionMetrics{YSSIM: 0.81, YSSIMDB: float64Pointer(7.2)}},
			},
		},
	}

	got := aggregateComparison(fixtures, []int{50, 75})
	if len(got.NominalQuality) != 2 || len(got.MatchedQuality) != 2 {
		t.Fatalf("aggregate lengths = %d/%d", len(got.NominalQuality), len(got.MatchedQuality))
	}
	nominal := got.NominalQuality[0]
	if nominal.Fixtures != 2 || nominal.GoBytes != 300 || nominal.CWebPBytes != 300 || nominal.GoSizeDeltaBytes != 0 {
		t.Fatalf("nominal aggregate = %#v", nominal)
	}
	if nominal.GoSmaller != 1 || nominal.CWebPSmaller != 1 || nominal.GoEncodeTotalNS != 30 || nominal.CWebPProcessTotalNS != 11 {
		t.Fatalf("nominal counters = %#v", nominal)
	}

	matched := got.MatchedQuality[0]
	if matched.CWebPBytes != 290 || matched.GoSizeDeltaBytes != 10 {
		t.Fatalf("matched sizes = %#v", matched)
	}
	if math.Abs(matched.GoSizeDeltaPct-1000.0/290.0) > 1e-12 {
		t.Fatalf("matched size delta = %v", matched.GoSizeDeltaPct)
	}
	if matched.MinimumCWebPQuality != 40 || matched.MaximumCWebPQuality != 60 || matched.MeanCWebPQuality != 50 {
		t.Fatalf("matched qualities = %#v", matched)
	}
	if math.Abs(matched.MeanYSSIMDelta) > 1e-12 {
		t.Fatalf("matched mean Y SSIM delta = %v", matched.MeanYSSIMDelta)
	}

	empty := got.MatchedQuality[1]
	if empty.Fixtures != 0 || empty.MinimumCWebPQuality != 0 || empty.GoSizeDeltaPct != 0 {
		t.Fatalf("empty aggregate = %#v", empty)
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
	if corpus.name != "production" || len(corpus.sha256) != 64 || corpus.split != "all" {
		t.Fatalf("corpus configuration = %#v", corpus)
	}
}

func float64Pointer(value float64) *float64 {
	return &value
}
