package benchmarkmetric

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestMeasureReportsExactImages(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for index := range img.Pix {
		img.Pix[index] = uint8(index * 7)
	}
	metrics, err := Measure(img, img)
	if err != nil {
		t.Fatal(err)
	}
	if !metrics.RGBExact || !metrics.AlphaExact || metrics.RGBPSNRDB != nil || metrics.YSSIM != 1 || metrics.YSSIMDB != nil {
		t.Fatalf("exact metrics = %#v", metrics)
	}
	if metrics.CompositeOverBlackPSNRDB != nil || metrics.CompositeOverWhitePSNRDB != nil || metrics.CompositeOverCheckerPSNRDB != nil {
		t.Fatalf("exact composite metrics = %#v", metrics)
	}
}

func TestMeasureCompositeMetricsIgnoreHiddenTransparentRGB(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	decoded := image.NewNRGBA(source.Bounds())
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 64, B: 32, A: 0})
	decoded.SetNRGBA(0, 0, color.NRGBA{R: 0, G: 0, B: 0, A: 0})

	metrics, err := Measure(source, decoded)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.RGBMSE == 0 {
		t.Fatal("hidden RGB change was not measured")
	}
	if metrics.CompositeOverBlackMSE != 0 || metrics.CompositeOverWhiteMSE != 0 || metrics.CompositeOverCheckerMSE != 0 {
		t.Fatalf("hidden RGB affected composite metrics: %#v", metrics)
	}
}

func TestMeasureCompositeMetricsIncludeAlphaChanges(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	decoded := image.NewNRGBA(source.Bounds())
	source.SetNRGBA(0, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 128})
	decoded.SetNRGBA(0, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 255})

	metrics, err := Measure(source, decoded)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.RGBMSE != 0 || metrics.AlphaExact {
		t.Fatalf("source metrics = %#v", metrics)
	}
	if metrics.CompositeOverBlackMSE == 0 || metrics.CompositeOverWhiteMSE == 0 || metrics.CompositeOverCheckerMSE == 0 {
		t.Fatalf("alpha change was not measured after compositing: %#v", metrics)
	}
}

func TestMeasureReportsChangedImages(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	decoded := image.NewNRGBA(source.Bounds())
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 100, G: 100, B: 100, A: 255})
			decoded.SetNRGBA(x, y, color.NRGBA{R: 110, G: 110, B: 110, A: 254})
		}
	}
	metrics, err := Measure(source, decoded)
	if err != nil {
		t.Fatal(err)
	}
	wantSSIM := float64(2*100*110+20) / float64(100*100+110*110+20)
	if metrics.RGBExact || metrics.AlphaExact || math.Abs(metrics.YSSIM-wantSSIM) > 1e-12 {
		t.Fatalf("changed metrics = %#v, want Y SSIM %.15f", metrics, wantSSIM)
	}
	if metrics.RGBMSE != 100 || metrics.YMSE != 100 || metrics.UVMSE != 0 {
		t.Fatalf("changed MSE metrics = %#v, want RGB/Y/UV = 100/100/0", metrics)
	}
}

func TestMeasureRejectsDimensionMismatch(t *testing.T) {
	if _, err := Measure(image.NewNRGBA(image.Rect(0, 0, 2, 1)), image.NewNRGBA(image.Rect(0, 0, 1, 1))); err == nil {
		t.Fatal("Measure accepted mismatched dimensions")
	}
}

func TestMeasurePlaneSSIMRejectsInvalidDimensions(t *testing.T) {
	if _, err := measurePlaneSSIM([]byte{1}, []byte{1}, 2, 1); err == nil {
		t.Fatal("measurePlaneSSIM accepted invalid dimensions")
	}
}
