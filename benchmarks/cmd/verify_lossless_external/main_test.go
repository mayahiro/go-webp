package main

import (
	"image/color"
	"strings"
	"testing"
)

func TestExternalFixtureIncludesHiddenRGB(t *testing.T) {
	img := hiddenRGBFixture()
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
		t.Fatal("hidden RGB fixture has no transparent pixel with non-zero RGB")
	}
}

func TestExternalDecoderVersionReportsPinnedXImage(t *testing.T) {
	version, err := externalDecoderVersion(decoderXImage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(version, xImageDecoderVersion) {
		t.Fatalf("version = %q, want %q", version, xImageDecoderVersion)
	}
}
