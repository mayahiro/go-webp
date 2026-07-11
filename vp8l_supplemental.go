package webp

import (
	"image"
	"runtime"
)

const vp8lSupplementalPredictorMode = 3

func vp8lChooseDefaultEncodingPlan(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int, cfg vp8lEncodingConfig) vp8lEncodingPlan {
	if !vp8lCanSearchSupplementalPlanInParallel(m, width, height) {
		baseline := chooseVP8LEncodingPlanForPreparedImage(m, readPixel, bounds, width, height, cfg)
		supplemental := vp8lChooseSupplementalPredictorModePlan(m, readPixel, bounds, width, height, vp8lSupplementalPredictorMode, cfg)
		return vp8lSmallerEncodingPlan(width, height, baseline, supplemental)
	}

	supplementalResult := make(chan vp8lEncodingPlan, 1)
	go func() {
		supplementalResult <- vp8lChooseSupplementalPredictorModePlan(m, readPixel, bounds, width, height, vp8lSupplementalPredictorMode, cfg)
	}()
	baseline := chooseVP8LEncodingPlanForPreparedImage(m, readPixel, bounds, width, height, cfg)
	return vp8lSmallerEncodingPlan(width, height, baseline, <-supplementalResult)
}

func vp8lCanSearchSupplementalPlanInParallel(m image.Image, width int, height int) bool {
	return width*height >= vp8lParallelMinPixels && runtime.GOMAXPROCS(0) > 1 && standardImageSupportsConcurrentRead(m)
}

func vp8lChooseSupplementalPredictorModePlan(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int, mode uint8, cfg vp8lEncodingConfig) vp8lEncodingPlan {
	readResidual := vp8lPredictorResidualReader(readPixel, bounds, width, height, mode)
	candidate := vp8lEncodingPlan{
		analysis:          analyzeImage(readResidual, bounds),
		alpha:             vp8lSourceHasAlpha(m, readPixel, bounds),
		predictor:         true,
		predictorMode:     mode,
		predictorSizeBits: vp8lDefaultPredictorSizeBits,
		predictorAnalysis: vp8lPredictorImageAnalysis(mode),
	}
	var candidates [vp8lMaxEncodingPlanCandidates]vp8lEncodingPlan
	candidates[0] = candidate
	return vp8lFinalizeEncodingPlan(
		readPixel,
		bounds,
		width,
		height,
		candidate,
		&candidates,
		1,
		0,
		candidate,
		^uint64(0),
		cfg,
	)
}

func vp8lSourceHasAlpha(m image.Image, readPixel pixelReader, bounds image.Rectangle) bool {
	switch m.(type) {
	case *image.Gray, *image.Gray16, *image.YCbCr:
		return false
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if readPixel(x, y).A != 255 {
				return true
			}
		}
	}
	return false
}

func vp8lSmallerEncodingPlan(width int, height int, a vp8lEncodingPlan, b vp8lEncodingPlan) vp8lEncodingPlan {
	if vp8lPayloadBits(width, height, b) < vp8lPayloadBits(width, height, a) {
		return b
	}
	return a
}
