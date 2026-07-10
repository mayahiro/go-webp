package main

import (
	"image"
	"image/color"
	"slices"
	"testing"

	webp "github.com/mayahiro/go-webp"
)

func TestMakeComparisonConfig(t *testing.T) {
	best, err := makeComparisonConfig("best", 75, 6)
	if err != nil {
		t.Fatalf("best config failed: %v", err)
	}
	if best.goOptions.Mode != webp.ModeBestCompression || !best.exact {
		t.Fatalf("best config = %#v", best)
	}
	if !slices.Equal(best.cwebpArgs, []string{"-lossless", "-m", "6"}) {
		t.Fatalf("best cwebp args = %q", best.cwebpArgs)
	}

	near, err := makeComparisonConfig("near-lossless", 75, 4)
	if err != nil {
		t.Fatalf("near-lossless config failed: %v", err)
	}
	if near.goOptions.Mode != webp.ModeNearLossless || near.goOptions.Quality != 75 || near.exact {
		t.Fatalf("near-lossless config = %#v", near)
	}
	if !slices.Equal(near.cwebpArgs, []string{"-lossless", "-m", "4", "-near_lossless", "75"}) {
		t.Fatalf("near-lossless cwebp args = %q", near.cwebpArgs)
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
