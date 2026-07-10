package main

import (
	"image"
	"image/color"
	"math"
	"slices"
	"testing"

	webp "github.com/mayahiro/go-webp"
)

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

	changed := image.NewNRGBA(source.Bounds())
	copy(changed.Pix, source.Pix)
	pixel := changed.NRGBAAt(1, 0)
	pixel.R++
	pixel.A++
	changed.SetNRGBA(1, 0, pixel)
	metrics, err := measureDistortion(source, changed)
	if err != nil {
		t.Fatalf("measureDistortion changed failed: %v", err)
	}
	if metrics.RGBExact || metrics.AlphaExact {
		t.Fatalf("changed metrics reported exact: %#v", metrics)
	}
	if math.Abs(metrics.RGBMAE-1.0/6.0) > 1e-12 {
		t.Fatalf("rgb_mae = %v, want %v", metrics.RGBMAE, 1.0/6.0)
	}
	if metrics.AlphaMAE != 0.5 {
		t.Fatalf("alpha_mae = %v, want 0.5", metrics.AlphaMAE)
	}
	if metrics.RGBPSNRDB == nil {
		t.Fatal("changed RGB PSNR is exact")
	}
}

func TestBuildMatchesUsesNearestSizeAndQuality(t *testing.T) {
	goSamples := []sample{{
		Quality:      75,
		EncodedBytes: 1000,
		Distortion:   distortionMetrics{RGBPSNRDB: float64Pointer(30)},
	}}
	cwebpSamples := []sample{
		{Quality: 70, EncodedBytes: 990, Distortion: distortionMetrics{RGBPSNRDB: float64Pointer(28)}},
		{Quality: 80, EncodedBytes: 1010, Distortion: distortionMetrics{RGBPSNRDB: float64Pointer(31)}},
		{Quality: 90, EncodedBytes: 1200, Distortion: distortionMetrics{RGBPSNRDB: float64Pointer(30.5)}},
	}
	matchedSize, matchedQuality := buildMatches(goSamples, cwebpSamples)
	if len(matchedSize) != 1 || matchedSize[0].CWebPQuality != 80 {
		t.Fatalf("matched size = %#v, want cwebp quality 80", matchedSize)
	}
	if len(matchedQuality) != 1 || matchedQuality[0].CWebPQuality != 90 {
		t.Fatalf("matched quality = %#v, want cwebp quality 90", matchedQuality)
	}
}

func float64Pointer(value float64) *float64 {
	return &value
}
