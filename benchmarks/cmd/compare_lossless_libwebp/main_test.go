package main

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	webp "github.com/mayahiro/go-webp"
)

// These unit tests do not invoke the optional external cwebp and dwebp binaries
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
	if corpus.name != "production" || len(corpus.sha256) != 64 || corpus.split != "all" {
		t.Fatalf("corpus configuration = %#v", corpus)
	}
}
