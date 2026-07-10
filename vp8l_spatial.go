package webp

import "image"

func makeVP8LIndexedPredictorPlan(readPixel pixelReader, bounds image.Rectangle, width int, height int, indexed vp8lEncodingPlan, cfg vp8lEncodingConfig) (vp8lEncodingPlan, bool) {
	if !indexed.colorIndexing || len(cfg.predictorModes) == 0 {
		return vp8lEncodingPlan{}, false
	}
	indexedWidth, indexedHeight := vp8lPlanImageDimensions(width, height, indexed)
	indexedBounds := image.Rect(0, 0, indexedWidth, indexedHeight)
	readIndexed := vp8lPlanPixelReader(readPixel, bounds, width, height, indexed)
	best := vp8lEncodingPlan{}
	bestBits := uint64(0)
	found := false
	consider := func(candidate vp8lEncodingPlan) {
		candidateBits := vp8lPayloadBits(width, height, candidate)
		if !found || candidateBits < bestBits {
			best = candidate
			bestBits = candidateBits
			found = true
		}
	}

	for _, mode := range cfg.predictorModes {
		readResidual := vp8lPredictorResidualReader(readIndexed, indexedBounds, indexedWidth, indexedHeight, mode)
		candidate := indexed
		candidate.analysis = analyzeImage(readResidual, indexedBounds)
		candidate.predictor = true
		candidate.predictorMode = mode
		candidate.predictorSizeBits = vp8lDefaultPredictorSizeBits
		candidate.predictorAnalysis = vp8lPredictorImageAnalysis(mode)
		consider(candidate)
	}

	if cfg.tryBlockPredictor {
		for _, sizeBits := range cfg.predictorBlockSizeBits {
			predictorWidth, predictorHeight := vp8lTransformDimensions(indexedWidth, indexedHeight, sizeBits)
			if predictorWidth*predictorHeight < 2 {
				continue
			}
			predictorImage, uniform := vp8lChooseBlockPredictorImage(readIndexed, indexedBounds, indexedWidth, indexedHeight, sizeBits, cfg.predictorModes)
			if len(predictorImage) == 0 || uniform {
				continue
			}
			readResidual := vp8lBlockPredictorResidualReader(readIndexed, indexedBounds, indexedWidth, indexedHeight, sizeBits, predictorImage, predictorWidth)
			candidate := indexed
			candidate.analysis = analyzeImage(readResidual, indexedBounds)
			candidate.predictor = true
			candidate.predictorMode = predictorImage[0]
			candidate.predictorSizeBits = sizeBits
			candidate.predictorImage = predictorImage
			predictorBounds := image.Rect(0, 0, predictorWidth, predictorHeight)
			candidate.predictorAnalysis = analyzeImage(vp8lPredictorImageReaderFromImage(predictorImage, predictorWidth), predictorBounds)
			consider(candidate)
		}
	}
	return best, found
}
