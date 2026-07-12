package main

import (
	"math"
	"sort"
)

type bdCurve struct {
	quality []float64
	logRate []float64
}

func bjontegaardRate(goPoints []rdPoint, cwebpPoints []rdPoint, metric func(rdPoint) rdMetric) *float64 {
	goCurve, ok := makeBDCurve(goPoints, metric)
	if !ok {
		return nil
	}
	cwebpCurve, ok := makeBDCurve(cwebpPoints, metric)
	if !ok {
		return nil
	}
	low := math.Max(goCurve.quality[0], cwebpCurve.quality[0])
	high := math.Min(goCurve.quality[len(goCurve.quality)-1], cwebpCurve.quality[len(cwebpCurve.quality)-1])
	if !(high > low) {
		return nil
	}
	goPCHIP, _ := newPCHIP(goCurve.quality, goCurve.logRate)
	cwebpPCHIP, _ := newPCHIP(cwebpCurve.quality, cwebpCurve.logRate)
	goIntegral, ok := goPCHIP.integral(low, high)
	if !ok {
		return nil
	}
	cwebpIntegral, ok := cwebpPCHIP.integral(low, high)
	if !ok {
		return nil
	}
	value := 100 * math.Expm1((goIntegral-cwebpIntegral)/(high-low))
	return &value
}

func bjontegaardQuality(goPoints []rdPoint, cwebpPoints []rdPoint, metric func(rdPoint) rdMetric) *float64 {
	goCurve, ok := makeBDCurve(goPoints, metric)
	if !ok {
		return nil
	}
	cwebpCurve, ok := makeBDCurve(cwebpPoints, metric)
	if !ok {
		return nil
	}
	low := math.Max(goCurve.logRate[0], cwebpCurve.logRate[0])
	high := math.Min(goCurve.logRate[len(goCurve.logRate)-1], cwebpCurve.logRate[len(cwebpCurve.logRate)-1])
	if !(high > low) {
		return nil
	}
	goPCHIP, _ := newPCHIP(goCurve.logRate, goCurve.quality)
	cwebpPCHIP, _ := newPCHIP(cwebpCurve.logRate, cwebpCurve.quality)
	goIntegral, ok := goPCHIP.integral(low, high)
	if !ok {
		return nil
	}
	cwebpIntegral, ok := cwebpPCHIP.integral(low, high)
	if !ok {
		return nil
	}
	value := (goIntegral - cwebpIntegral) / (high - low)
	return &value
}

func makeBDCurve(points []rdPoint, metric func(rdPoint) rdMetric) (bdCurve, bool) {
	type candidate struct {
		quality float64
		logRate float64
	}
	candidates := make([]candidate, 0, len(points))
	for _, point := range points {
		quality := metric(point)
		if !quality.valid || !finiteFloat(quality.value) || !finiteFloat(point.logRate) {
			continue
		}
		candidates = append(candidates, candidate{quality: quality.value, logRate: point.logRate})
	}
	sort.SliceStable(candidates, func(i int, j int) bool {
		if candidates[i].logRate == candidates[j].logRate {
			return candidates[i].quality > candidates[j].quality
		}
		return candidates[i].logRate < candidates[j].logRate
	})
	result := bdCurve{}
	for _, candidate := range candidates {
		if len(result.logRate) > 0 && candidate.logRate == result.logRate[len(result.logRate)-1] {
			continue
		}
		// Dominated points cannot describe a single-valued rate-distortion curve
		if len(result.quality) > 0 && candidate.quality <= result.quality[len(result.quality)-1] {
			continue
		}
		result.logRate = append(result.logRate, candidate.logRate)
		result.quality = append(result.quality, candidate.quality)
	}
	// Four points is the minimum used by this report for stable BD integration
	return result, len(result.quality) >= 4
}
