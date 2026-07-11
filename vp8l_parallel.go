package webp

import (
	"image"
	"runtime"
	"sync"
)

const (
	vp8lParallelMinPixels  = 64 * 64
	vp8lParallelMaxWorkers = 4
)

type vp8lParallelTransformResults struct {
	blockPredictor         vp8lEncodingPlan
	hasBlockPredictor      bool
	blockColor             vp8lEncodingPlan
	hasBlockColor          bool
	blockPredictorColor    vp8lEncodingPlan
	hasBlockPredictorColor bool
	baselineBlockPredictor vp8lEncodingPlan
	hasBaselinePredictor   bool
	baselinePredictorColor vp8lEncodingPlan
	hasBaselineColor       bool
	predictors             []imageAnalysis
	subtractGreen          imageAnalysis
	colors                 []imageAnalysis
}

func vp8lPrepareParallelTransformReader(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int, enabled bool, knownConcurrent bool) (pixelReader, bool) {
	if !enabled || width*height < vp8lParallelMinPixels || runtime.GOMAXPROCS(0) < 2 {
		return readPixel, false
	}
	if knownConcurrent || standardImageSupportsConcurrentRead(m) {
		return readPixel, true
	}
	plane, ok := materializePixelPlane(readPixel, bounds, width, height, vp8lSourcePlaneMaxBytes)
	if !ok {
		return readPixel, false
	}
	return plane.pixel, true
}

func vp8lAddParallelTransformCandidates(
	readPixel pixelReader,
	bounds image.Rectangle,
	width int,
	height int,
	analysis imageAnalysis,
	cfg vp8lEncodingConfig,
	candidates *[vp8lMaxEncodingPlanCandidates]vp8lEncodingPlan,
	candidateCount int,
	literalBestIndex int,
	best vp8lEncodingPlan,
	bestBits uint64,
) (int, int, vp8lEncodingPlan, uint64) {
	results := vp8lBuildParallelTransformCandidates(readPixel, bounds, width, height, analysis, cfg)
	if results.hasBlockPredictor {
		candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(
			candidates, candidateCount, literalBestIndex, results.blockPredictor, width, height, best, bestBits,
		)
	}
	if results.hasBlockPredictorColor {
		candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(
			candidates, candidateCount, literalBestIndex, results.blockPredictorColor, width, height, best, bestBits,
		)
	}
	if results.hasBaselinePredictor {
		candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(
			candidates, candidateCount, literalBestIndex, results.baselineBlockPredictor, width, height, best, bestBits,
		)
	}
	if results.hasBaselineColor {
		candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(
			candidates, candidateCount, literalBestIndex, results.baselinePredictorColor, width, height, best, bestBits,
		)
	}
	if results.hasBlockColor {
		candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(
			candidates, candidateCount, literalBestIndex, results.blockColor, width, height, best, bestBits,
		)
	}
	for i, candidateAnalysis := range results.predictors {
		mode := cfg.predictorModes[i]
		candidate := vp8lEncodingPlan{
			analysis:          candidateAnalysis,
			alpha:             analysis.alpha,
			predictor:         true,
			predictorMode:     mode,
			predictorSizeBits: vp8lDefaultPredictorSizeBits,
			predictorAnalysis: vp8lPredictorImageAnalysis(mode),
		}
		candidateBits := vp8lPayloadBits(width, height, candidate)
		candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(
			candidates, candidateCount, literalBestIndex, candidate, width, height, best, bestBits,
		)
		if !cfg.tryCombinedTransforms || !vp8lShouldTryCombinedTransform(candidateBits, bestBits) {
			continue
		}
		readResidual := vp8lPredictorResidualReader(readPixel, bounds, width, height, cfg.predictorModes[i])
		if vp8lShouldTrySubtractGreenAfterTransform(readResidual, bounds, width) {
			combined := candidate
			combined.analysis = analyzeImage(vp8lSubtractGreenReader(readResidual), bounds)
			combined.subtractGreen = true
			candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(
				candidates, candidateCount, literalBestIndex, combined, width, height, best, bestBits,
			)
		}
		for _, element := range cfg.colorTransformCandidates {
			if !vp8lShouldTryColorTransformAfterTransform(readResidual, bounds, width, element) {
				continue
			}
			combined := candidate
			combined.analysis = analyzeImage(vp8lColorTransformReader(readResidual, element), bounds)
			combined.colorTransform = true
			combined.colorSizeBits = vp8lDefaultColorTransformSizeBits
			combined.colorElement = element
			combined.colorAnalysis = vp8lColorTransformImageAnalysis(element)
			candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(
				candidates, candidateCount, literalBestIndex, combined, width, height, best, bestBits,
			)
		}
	}
	subtractGreen := vp8lEncodingPlan{
		analysis:      results.subtractGreen,
		alpha:         analysis.alpha,
		subtractGreen: true,
	}
	candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(candidates, candidateCount, literalBestIndex, subtractGreen, width, height, best, bestBits)
	for i, candidateAnalysis := range results.colors {
		element := cfg.colorTransformCandidates[i]
		candidate := vp8lEncodingPlan{
			analysis:       candidateAnalysis,
			alpha:          analysis.alpha,
			colorTransform: true,
			colorSizeBits:  vp8lDefaultColorTransformSizeBits,
			colorElement:   element,
			colorAnalysis:  vp8lColorTransformImageAnalysis(element),
		}
		candidateBits := vp8lPayloadBits(width, height, candidate)
		candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(
			candidates, candidateCount, literalBestIndex, candidate, width, height, best, bestBits,
		)
		if !cfg.tryCombinedTransforms || !vp8lShouldTryCombinedTransform(candidateBits, bestBits) {
			continue
		}
		readColorTransform := vp8lColorTransformReader(readPixel, cfg.colorTransformCandidates[i])
		if !vp8lShouldTrySubtractGreenAfterTransform(readColorTransform, bounds, width) {
			continue
		}
		combined := candidate
		combined.analysis = analyzeImage(vp8lSubtractGreenReader(readColorTransform), bounds)
		combined.subtractGreen = true
		candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(
			candidates, candidateCount, literalBestIndex, combined, width, height, best, bestBits,
		)
	}
	return candidateCount, literalBestIndex, best, bestBits
}

func vp8lBuildParallelTransformCandidates(readPixel pixelReader, bounds image.Rectangle, width int, height int, analysis imageAnalysis, cfg vp8lEncodingConfig) vp8lParallelTransformResults {
	results := vp8lParallelTransformResults{
		predictors: make([]imageAnalysis, len(cfg.predictorModes)),
		colors:     make([]imageAnalysis, len(cfg.colorTransformCandidates)),
	}
	taskCount := len(results.predictors) + len(results.colors) + 1
	blockColorTask := -1
	if cfg.tryBlockColorTransform {
		blockColorTask = taskCount
		taskCount++
	}
	blockTask := -1
	if cfg.tryBlockPredictor {
		blockTask = taskCount
		taskCount++
	}
	workers := minInt(runtime.GOMAXPROCS(0), vp8lParallelMaxWorkers)
	workers = minInt(workers, taskCount)
	jobs := make(chan int, taskCount)
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			for task := range jobs {
				switch {
				case task < len(results.predictors):
					readResidual := vp8lPredictorResidualReader(readPixel, bounds, width, height, cfg.predictorModes[task])
					results.predictors[task] = analyzeImage(readResidual, bounds)
				case task < len(results.predictors)+len(results.colors):
					colorIndex := task - len(results.predictors)
					readColorTransform := vp8lColorTransformReader(readPixel, cfg.colorTransformCandidates[colorIndex])
					results.colors[colorIndex] = analyzeImage(readColorTransform, bounds)
				case task == len(results.predictors)+len(results.colors):
					readSubtractGreen := vp8lSubtractGreenReader(readPixel)
					results.subtractGreen = analyzeImage(readSubtractGreen, bounds)
				case task == blockColorTask:
					base := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
					results.blockColor, results.hasBlockColor = makeVP8LBlockColorTransformPlan(readPixel, bounds, width, height, base, cfg)
				case task == blockTask:
					results.blockPredictor, results.hasBlockPredictor = makeVP8LBlockPredictorPlan(readPixel, bounds, width, height, analysis.alpha, cfg)
					if results.hasBlockPredictor && cfg.tryBlockColorTransform {
						results.blockPredictorColor, results.hasBlockPredictorColor = makeVP8LBlockColorTransformPlan(readPixel, bounds, width, height, results.blockPredictor, cfg)
					}
					if len(cfg.predictorModes) > len(vp8lPredictorModeCandidates) {
						baselineConfig := cfg
						baselineConfig.predictorModes = vp8lPredictorModeCandidates[:]
						results.baselineBlockPredictor, results.hasBaselinePredictor = makeVP8LBlockPredictorPlan(readPixel, bounds, width, height, analysis.alpha, baselineConfig)
						if results.hasBaselinePredictor {
							results.baselineBlockPredictor.baselineCandidate = true
							if cfg.tryBlockColorTransform {
								results.baselinePredictorColor, results.hasBaselineColor = makeVP8LBlockColorTransformPlan(readPixel, bounds, width, height, results.baselineBlockPredictor, baselineConfig)
								results.baselinePredictorColor.baselineCandidate = results.hasBaselineColor
							}
						}
					}
				}
			}
		}()
	}
	for task := 0; task < taskCount; task++ {
		jobs <- task
	}
	close(jobs)
	wait.Wait()
	return results
}
