package benchmarkmetric

import (
	"errors"
	"math"
)

const ssimKernelRadius = 3

var ssimKernelWeights = [...]uint64{1, 2, 3, 4, 3, 2, 1}

type ssimMoments struct {
	weight       uint64
	source       uint64
	target       uint64
	sourceSquare uint64
	sourceTarget uint64
	targetSquare uint64
}

// measurePlaneSSIM follows libwebp's BSD-licensed weighted 7x7 plane SSIM
// calculation in src/dsp/ssim.c and src/enc/picture_psnr_enc.c
func measurePlaneSSIM(source []byte, target []byte, width int, height int) (float64, error) {
	if width <= 0 || height <= 0 || len(source) != width*height || len(target) != width*height {
		return 0, errors.New("invalid SSIM plane dimensions")
	}

	total := 0.0
	for centerY := 0; centerY < height; centerY++ {
		minY := max(0, centerY-ssimKernelRadius)
		maxY := min(height-1, centerY+ssimKernelRadius)
		for centerX := 0; centerX < width; centerX++ {
			minX := max(0, centerX-ssimKernelRadius)
			maxX := min(width-1, centerX+ssimKernelRadius)
			moments := ssimMoments{}
			for y := minY; y <= maxY; y++ {
				yWeight := ssimKernelWeights[ssimKernelRadius+y-centerY]
				row := y * width
				for x := minX; x <= maxX; x++ {
					weight := yWeight * ssimKernelWeights[ssimKernelRadius+x-centerX]
					sourceValue := uint64(source[row+x])
					targetValue := uint64(target[row+x])
					moments.weight += weight
					moments.source += weight * sourceValue
					moments.target += weight * targetValue
					moments.sourceSquare += weight * sourceValue * sourceValue
					moments.sourceTarget += weight * sourceValue * targetValue
					moments.targetSquare += weight * targetValue * targetValue
				}
			}
			total += ssimFromMoments(moments)
		}
	}
	return total / float64(width*height), nil
}

func ssimFromMoments(moments ssimMoments) float64 {
	weightSquared := moments.weight * moments.weight
	stabilityMean := 20 * weightSquared
	stabilityVariance := 60 * weightSquared
	darkThreshold := 8 * 8 * weightSquared
	sourceMeanSquared := moments.source * moments.source
	targetMeanSquared := moments.target * moments.target
	if sourceMeanSquared+targetMeanSquared < darkThreshold {
		return 1
	}

	meanProduct := moments.source * moments.target
	covariance := int64(moments.sourceTarget*moments.weight) - int64(meanProduct)
	positiveCovariance := uint64(0)
	if covariance > 0 {
		positiveCovariance = uint64(covariance)
	}
	sourceVariance := moments.sourceSquare*moments.weight - sourceMeanSquared
	targetVariance := moments.targetSquare*moments.weight - targetMeanSquared
	structureNumerator := (2*positiveCovariance + stabilityVariance) >> 8
	structureDenominator := (sourceVariance + targetVariance + stabilityVariance) >> 8
	numerator := (2*meanProduct + stabilityMean) * structureNumerator
	denominator := (sourceMeanSquared + targetMeanSquared + stabilityMean) * structureDenominator
	score := float64(numerator) / float64(denominator)
	return min(1, max(0, score))
}

func ssimDB(ssim float64) *float64 {
	if ssim >= 1 {
		return nil
	}
	value := -10 * math.Log10(1-ssim)
	return &value
}
