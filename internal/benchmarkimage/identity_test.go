package benchmarkimage

import (
	"image"
	"image/color"
	"testing"
)

func TestIdentifyPixelsNormalizesImageRepresentation(t *testing.T) {
	nrgba := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	nrgba.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	nrgba.SetNRGBA(1, 0, color.NRGBA{R: 40, G: 50, B: 60, A: 127})
	paletted := image.NewPaletted(image.Rect(4, 7, 6, 8), color.Palette{
		color.NRGBA{R: 10, G: 20, B: 30, A: 255},
		color.NRGBA{R: 40, G: 50, B: 60, A: 127},
	})
	paletted.SetColorIndex(4, 7, 0)
	paletted.SetColorIndex(5, 7, 1)

	nrgbaIdentity := IdentifyPixels(nrgba)
	palettedIdentity := IdentifyPixels(paletted)
	if nrgbaIdentity.SHA256 != palettedIdentity.SHA256 {
		t.Fatal("equivalent pixels have different identities")
	}
	if !nrgbaIdentity.HasAlpha || !palettedIdentity.HasAlpha {
		t.Fatal("alpha presence was not detected")
	}
}

func TestIdentifyPixelsIncludesDimensions(t *testing.T) {
	wide := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	tall := image.NewNRGBA(image.Rect(0, 0, 1, 2))
	if IdentifyPixels(wide).SHA256 == IdentifyPixels(tall).SHA256 {
		t.Fatal("different dimensions have the same identity")
	}
}
