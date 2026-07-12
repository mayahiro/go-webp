package main

import (
	"math"
	"testing"
)

func TestPCHIPInterpolatesAndIntegratesLinearCurve(t *testing.T) {
	curve, ok := newPCHIP(
		[]float64{0, 1, 2, 3},
		[]float64{1, 3, 5, 7},
	)
	if !ok {
		t.Fatal("newPCHIP rejected a strictly increasing axis")
	}
	value, ok := curve.value(1.5)
	if !ok || math.Abs(value-4) > 1e-12 {
		t.Fatalf("value(1.5) = %v, %t, want 4, true", value, ok)
	}
	integral, ok := curve.integral(0, 3)
	if !ok || math.Abs(integral-12) > 1e-12 {
		t.Fatalf("integral(0, 3) = %v, %t, want 12, true", integral, ok)
	}
	if _, ok := curve.value(-0.01); ok {
		t.Fatal("PCHIP extrapolated below its measured range")
	}
	if _, ok := curve.integral(0, 3.01); ok {
		t.Fatal("PCHIP integrated beyond its measured range")
	}
	if _, ok := newPCHIP([]float64{0, 1, 1}, []float64{0, 1, 2}); ok {
		t.Fatal("PCHIP accepted a non-increasing axis")
	}
}

func TestBjontegaardMetricsUseGoMinusCWebPDirection(t *testing.T) {
	goPoints := makeLinearBDPoints(1)
	cwebpPoints := makeLinearBDPoints(2)
	metric := func(point rdPoint) rdMetric { return point.metrics.rgbPSNRDB }

	bdRate := bjontegaardRate(goPoints, cwebpPoints, metric)
	if bdRate == nil || math.Abs(*bdRate-(-50)) > 1e-10 {
		t.Fatalf("BD-rate = %v, want -50 percent", bdRate)
	}
	bdPSNR := bjontegaardQuality(goPoints, cwebpPoints, metric)
	if bdPSNR == nil || math.Abs(*bdPSNR-1) > 1e-10 {
		t.Fatalf("BD-PSNR = %v, want +1 dB", bdPSNR)
	}

	identicalRate := bjontegaardRate(goPoints, goPoints, metric)
	identicalQuality := bjontegaardQuality(goPoints, goPoints, metric)
	if identicalRate == nil || identicalQuality == nil || math.Abs(*identicalRate) > 1e-12 || math.Abs(*identicalQuality) > 1e-12 {
		t.Fatalf("identical curve deltas = %v/%v, want 0/0", identicalRate, identicalQuality)
	}
	if got := bjontegaardRate(goPoints[:3], cwebpPoints[:3], metric); got != nil {
		t.Fatalf("three-point BD-rate = %v, want nil", got)
	}
}

func TestRateDistortionReportAddsTargetSizeAndCurveMetrics(t *testing.T) {
	points := make([]aggregatePoint, 0, 4)
	for i, quality := range []int{25, 50, 75, 90} {
		goBytes := 100 << i
		cwebpBytes := 200 << i
		goPSNR := 30 + float64(i)
		cwebpPSNR := 30 + float64(i)
		goSSIM := 0.90 + 0.02*float64(i)
		cwebpSSIM := goSSIM
		points = append(points, aggregatePoint{
			GoQuality:  quality,
			Fixtures:   1,
			Pixels:     100,
			GoBytes:    goBytes,
			CWebPBytes: cwebpBytes,
			PixelWeighted: aggregateQualityComparison{
				YSSIM:                  aggregateValueComparison(goSSIM, cwebpSSIM),
				RGBPSNRDB:              aggregateValueComparison(goPSNR, cwebpPSNR),
				YPSNRDB:                aggregateValueComparison(goPSNR+1, cwebpPSNR+1),
				UVPSNRDB:               aggregateValueComparison(goPSNR+2, cwebpPSNR+2),
				CompositeBlackPSNRDB:   aggregateValueComparison(goPSNR+3, cwebpPSNR+3),
				CompositeWhitePSNRDB:   aggregateValueComparison(goPSNR+4, cwebpPSNR+4),
				CompositeCheckerPSNRDB: aggregateValueComparison(goPSNR+5, cwebpPSNR+5),
			},
		})
	}

	got := analyzeRateDistortion(points)
	if got.Interpolation != rateDistortionInterpolation || got.Extrapolation != "disabled" || got.GoPoints != 4 || got.CWebPPoints != 4 {
		t.Fatalf("rate-distortion metadata = %#v", got)
	}
	if len(got.MatchedSize) != 3 || len(got.MatchedQuality) != 4 {
		t.Fatalf("match counts = %d/%d, want 3/4", len(got.MatchedSize), len(got.MatchedQuality))
	}
	for _, match := range got.MatchedSize {
		if math.Abs(match.GoMinusCWebPBytes) > 1e-7 || math.Abs(match.GoMinusCWebPPercent) > 1e-10 {
			t.Errorf("target-size match = %#v", match)
		}
	}
	if got.Bjontegaard.BDRateRGBPSNRPercent == nil || math.Abs(*got.Bjontegaard.BDRateRGBPSNRPercent-(-50)) > 1e-10 {
		t.Fatalf("RGB BD-rate = %v, want -50 percent", got.Bjontegaard.BDRateRGBPSNRPercent)
	}
	if got.Bjontegaard.BDRateYSSIMPercent == nil || math.Abs(*got.Bjontegaard.BDRateYSSIMPercent-(-50)) > 1e-10 {
		t.Fatalf("Y SSIM BD-rate = %v, want -50 percent", got.Bjontegaard.BDRateYSSIMPercent)
	}
	if got.Bjontegaard.BDPSNRDB == nil || math.Abs(*got.Bjontegaard.BDPSNRDB-1) > 1e-10 {
		t.Fatalf("BD-PSNR = %v, want +1 dB", got.Bjontegaard.BDPSNRDB)
	}
	if got.Bjontegaard.BDSSIM == nil || math.Abs(*got.Bjontegaard.BDSSIM-0.02) > 1e-10 {
		t.Fatalf("BD-SSIM = %v, want +0.02", got.Bjontegaard.BDSSIM)
	}
}

func makeLinearBDPoints(rateScale float64) []rdPoint {
	points := make([]rdPoint, 4)
	for i := range points {
		points[i] = rdPoint{
			quality: float64(i),
			logRate: math.Log(rateScale * math.Pow(2, float64(i))),
			pixels:  1,
			metrics: rdMetrics{
				rgbPSNRDB: rdMetric{value: 30 + float64(i), valid: true},
			},
		}
	}
	return points
}
