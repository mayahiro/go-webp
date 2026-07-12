package main

import (
	"math"
	"sort"
)

const rateDistortionInterpolation = "piecewise cubic Hermite (PCHIP)"

type rateDistortionReport struct {
	Interpolation      string                     `json:"interpolation"`
	Extrapolation      string                     `json:"extrapolation"`
	RateUnit           string                     `json:"rate_unit"`
	QualityMatchMetric string                     `json:"quality_match_metric"`
	GoPoints           int                        `json:"go_points"`
	CWebPPoints        int                        `json:"cwebp_points"`
	MatchedSize        []rateDistortionPointMatch `json:"matched_size"`
	MatchedQuality     []rateDistortionPointMatch `json:"matched_quality"`
	Bjontegaard        bjontegaardReport          `json:"bjontegaard"`
}

type bjontegaardReport struct {
	BDRateRGBPSNRPercent *float64 `json:"bd_rate_rgb_psnr_percent"`
	BDRateYSSIMPercent   *float64 `json:"bd_rate_y_ssim_percent"`
	BDPSNRDB             *float64 `json:"bd_psnr_db"`
	BDSSIM               *float64 `json:"bd_ssim"`
}

type rateDistortionPointMatch struct {
	GoQuality                          int      `json:"go_quality"`
	CWebPQuality                       float64  `json:"cwebp_quality"`
	GoBytes                            float64  `json:"go_bytes"`
	CWebPBytes                         float64  `json:"cwebp_bytes"`
	GoMinusCWebPBytes                  float64  `json:"go_minus_cwebp_bytes"`
	GoMinusCWebPPercent                float64  `json:"go_minus_cwebp_percent"`
	GoMinusCWebPRGBPSNRDB              *float64 `json:"go_minus_cwebp_rgb_psnr_db"`
	GoMinusCWebPYPSNRDB                *float64 `json:"go_minus_cwebp_y_psnr_db"`
	GoMinusCWebPUVPSNRDB               *float64 `json:"go_minus_cwebp_uv_psnr_db"`
	GoMinusCWebPYSSIM                  *float64 `json:"go_minus_cwebp_y_ssim"`
	GoMinusCWebPCompositeBlackPSNRDB   *float64 `json:"go_minus_cwebp_composite_black_psnr_db"`
	GoMinusCWebPCompositeWhitePSNRDB   *float64 `json:"go_minus_cwebp_composite_white_psnr_db"`
	GoMinusCWebPCompositeCheckerPSNRDB *float64 `json:"go_minus_cwebp_composite_checker_psnr_db"`
}

type rdMetric struct {
	value float64
	valid bool
}

type rdMetrics struct {
	ySSIM                  rdMetric
	rgbPSNRDB              rdMetric
	yPSNRDB                rdMetric
	uvPSNRDB               rdMetric
	compositeBlackPSNRDB   rdMetric
	compositeWhitePSNRDB   rdMetric
	compositeCheckerPSNRDB rdMetric
}

type rdPoint struct {
	quality float64
	logRate float64
	pixels  float64
	metrics rdMetrics
}

func analyzeRateDistortion(points []aggregatePoint) rateDistortionReport {
	goPoints := rdPointsFromAggregate(points, true)
	cwebpPoints := rdPointsFromAggregate(points, false)
	result := rateDistortionReport{
		Interpolation:      rateDistortionInterpolation,
		Extrapolation:      "disabled",
		RateUnit:           "encoded_bytes_per_pixel",
		QualityMatchMetric: "pixel_weighted_y_ssim",
		GoPoints:           len(goPoints),
		CWebPPoints:        len(cwebpPoints),
	}
	for _, goPoint := range goPoints {
		if matched, ok := interpolateRDPoint(cwebpPoints, rdLogRate, goPoint.logRate); ok {
			matched.logRate = goPoint.logRate
			result.MatchedSize = append(result.MatchedSize, makeRateDistortionMatch(goPoint, matched))
		}
		if goPoint.metrics.ySSIM.valid {
			if matched, ok := interpolateRDPoint(cwebpPoints, rdYSSIM, goPoint.metrics.ySSIM.value); ok {
				matched.metrics.ySSIM = goPoint.metrics.ySSIM
				result.MatchedQuality = append(result.MatchedQuality, makeRateDistortionMatch(goPoint, matched))
			}
		}
	}
	result.Bjontegaard = bjontegaardReport{
		BDRateRGBPSNRPercent: bjontegaardRate(goPoints, cwebpPoints, func(point rdPoint) rdMetric { return point.metrics.rgbPSNRDB }),
		BDRateYSSIMPercent:   bjontegaardRate(goPoints, cwebpPoints, func(point rdPoint) rdMetric { return point.metrics.ySSIM }),
		BDPSNRDB:             bjontegaardQuality(goPoints, cwebpPoints, func(point rdPoint) rdMetric { return point.metrics.rgbPSNRDB }),
		BDSSIM:               bjontegaardQuality(goPoints, cwebpPoints, func(point rdPoint) rdMetric { return point.metrics.ySSIM }),
	}
	return result
}

func rdPointsFromAggregate(points []aggregatePoint, useGo bool) []rdPoint {
	result := make([]rdPoint, 0, len(points))
	for _, point := range points {
		if point.Fixtures == 0 || point.Pixels <= 0 {
			continue
		}
		encodedBytes := point.CWebPBytes
		if useGo {
			encodedBytes = point.GoBytes
		}
		if encodedBytes <= 0 {
			continue
		}
		result = append(result, rdPoint{
			quality: float64(point.GoQuality),
			logRate: math.Log(float64(encodedBytes) / float64(point.Pixels)),
			pixels:  float64(point.Pixels),
			metrics: rdMetricsFromAggregate(point.PixelWeighted, useGo),
		})
	}
	return result
}

func rdMetricsFromAggregate(comparison aggregateQualityComparison, useGo bool) rdMetrics {
	return rdMetrics{
		ySSIM:                  rdMetricFromComparison(comparison.YSSIM, useGo),
		rgbPSNRDB:              rdMetricFromComparison(comparison.RGBPSNRDB, useGo),
		yPSNRDB:                rdMetricFromComparison(comparison.YPSNRDB, useGo),
		uvPSNRDB:               rdMetricFromComparison(comparison.UVPSNRDB, useGo),
		compositeBlackPSNRDB:   rdMetricFromComparison(comparison.CompositeBlackPSNRDB, useGo),
		compositeWhitePSNRDB:   rdMetricFromComparison(comparison.CompositeWhitePSNRDB, useGo),
		compositeCheckerPSNRDB: rdMetricFromComparison(comparison.CompositeCheckerPSNRDB, useGo),
	}
}

func rdMetricFromComparison(comparison aggregateMetricComparison, useGo bool) rdMetric {
	value := comparison.CWebP
	if useGo {
		value = comparison.Go
	}
	if value == nil || !finiteFloat(*value) {
		return rdMetric{}
	}
	return rdMetric{value: *value, valid: true}
}

type rdAxis func(rdPoint) (float64, bool)

func rdLogRate(point rdPoint) (float64, bool) {
	return point.logRate, finiteFloat(point.logRate)
}

func rdYSSIM(point rdPoint) (float64, bool) {
	return point.metrics.ySSIM.value, point.metrics.ySSIM.valid
}

type rdAxisPoint struct {
	x     float64
	point rdPoint
}

func interpolateRDPoint(points []rdPoint, axis rdAxis, target float64) (rdPoint, bool) {
	axisPoints := make([]rdAxisPoint, 0, len(points))
	for _, point := range points {
		x, ok := axis(point)
		if ok && finiteFloat(x) {
			axisPoints = append(axisPoints, rdAxisPoint{x: x, point: point})
		}
	}
	sort.SliceStable(axisPoints, func(i int, j int) bool { return axisPoints[i].x < axisPoints[j].x })
	axisPoints = uniqueRDAxisPoints(axisPoints)
	if len(axisPoints) < 2 || target < axisPoints[0].x || target > axisPoints[len(axisPoints)-1].x {
		return rdPoint{}, false
	}
	xs := make([]float64, len(axisPoints))
	for i, point := range axisPoints {
		xs[i] = point.x
	}
	quality := interpolateRDValue(axisPoints, xs, target, func(point rdPoint) rdMetric {
		return rdMetric{value: point.quality, valid: true}
	})
	logRate := interpolateRDValue(axisPoints, xs, target, func(point rdPoint) rdMetric {
		return rdMetric{value: point.logRate, valid: finiteFloat(point.logRate)}
	})
	if !quality.valid || !logRate.valid {
		return rdPoint{}, false
	}
	result := rdPoint{
		quality: quality.value,
		logRate: logRate.value,
		pixels:  axisPoints[0].point.pixels,
	}
	result.metrics = rdMetrics{
		ySSIM:                  interpolateRDValue(axisPoints, xs, target, func(point rdPoint) rdMetric { return point.metrics.ySSIM }),
		rgbPSNRDB:              interpolateRDValue(axisPoints, xs, target, func(point rdPoint) rdMetric { return point.metrics.rgbPSNRDB }),
		yPSNRDB:                interpolateRDValue(axisPoints, xs, target, func(point rdPoint) rdMetric { return point.metrics.yPSNRDB }),
		uvPSNRDB:               interpolateRDValue(axisPoints, xs, target, func(point rdPoint) rdMetric { return point.metrics.uvPSNRDB }),
		compositeBlackPSNRDB:   interpolateRDValue(axisPoints, xs, target, func(point rdPoint) rdMetric { return point.metrics.compositeBlackPSNRDB }),
		compositeWhitePSNRDB:   interpolateRDValue(axisPoints, xs, target, func(point rdPoint) rdMetric { return point.metrics.compositeWhitePSNRDB }),
		compositeCheckerPSNRDB: interpolateRDValue(axisPoints, xs, target, func(point rdPoint) rdMetric { return point.metrics.compositeCheckerPSNRDB }),
	}
	return result, finiteFloat(result.quality) && finiteFloat(result.logRate)
}

func uniqueRDAxisPoints(points []rdAxisPoint) []rdAxisPoint {
	if len(points) == 0 {
		return nil
	}
	result := points[:1]
	for _, point := range points[1:] {
		if point.x == result[len(result)-1].x {
			continue
		}
		result = append(result, point)
	}
	return result
}

func interpolateRDValue(points []rdAxisPoint, xs []float64, target float64, value func(rdPoint) rdMetric) rdMetric {
	metricX := make([]float64, 0, len(points))
	metricY := make([]float64, 0, len(points))
	for i, point := range points {
		metric := value(point.point)
		if !metric.valid || !finiteFloat(metric.value) {
			continue
		}
		metricX = append(metricX, xs[i])
		metricY = append(metricY, metric.value)
	}
	curve, ok := newPCHIP(metricX, metricY)
	if !ok {
		return rdMetric{}
	}
	interpolated, ok := curve.value(target)
	if !ok {
		return rdMetric{}
	}
	return rdMetric{value: interpolated, valid: true}
}

func makeRateDistortionMatch(goPoint rdPoint, cwebpPoint rdPoint) rateDistortionPointMatch {
	goBytes := math.Exp(goPoint.logRate) * goPoint.pixels
	cwebpBytes := math.Exp(cwebpPoint.logRate) * cwebpPoint.pixels
	deltaBytes := goBytes - cwebpBytes
	if math.Abs(deltaBytes) < 1e-7 {
		deltaBytes = 0
	}
	deltaPercent := 0.0
	if cwebpBytes != 0 {
		deltaPercent = 100 * deltaBytes / cwebpBytes
	}
	return rateDistortionPointMatch{
		GoQuality:                          int(math.Round(goPoint.quality)),
		CWebPQuality:                       cwebpPoint.quality,
		GoBytes:                            goBytes,
		CWebPBytes:                         cwebpBytes,
		GoMinusCWebPBytes:                  deltaBytes,
		GoMinusCWebPPercent:                deltaPercent,
		GoMinusCWebPRGBPSNRDB:              rdMetricDifference(goPoint.metrics.rgbPSNRDB, cwebpPoint.metrics.rgbPSNRDB),
		GoMinusCWebPYPSNRDB:                rdMetricDifference(goPoint.metrics.yPSNRDB, cwebpPoint.metrics.yPSNRDB),
		GoMinusCWebPUVPSNRDB:               rdMetricDifference(goPoint.metrics.uvPSNRDB, cwebpPoint.metrics.uvPSNRDB),
		GoMinusCWebPYSSIM:                  rdMetricDifference(goPoint.metrics.ySSIM, cwebpPoint.metrics.ySSIM),
		GoMinusCWebPCompositeBlackPSNRDB:   rdMetricDifference(goPoint.metrics.compositeBlackPSNRDB, cwebpPoint.metrics.compositeBlackPSNRDB),
		GoMinusCWebPCompositeWhitePSNRDB:   rdMetricDifference(goPoint.metrics.compositeWhitePSNRDB, cwebpPoint.metrics.compositeWhitePSNRDB),
		GoMinusCWebPCompositeCheckerPSNRDB: rdMetricDifference(goPoint.metrics.compositeCheckerPSNRDB, cwebpPoint.metrics.compositeCheckerPSNRDB),
	}
}

func rdMetricDifference(goMetric rdMetric, cwebpMetric rdMetric) *float64 {
	if !goMetric.valid || !cwebpMetric.valid {
		return nil
	}
	delta := goMetric.value - cwebpMetric.value
	if math.Abs(delta) < 1e-15 {
		delta = 0
	}
	return &delta
}
