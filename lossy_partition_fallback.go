package webp

import "fmt"

type vp8FirstPartitionFallbackStage uint8

const (
	vp8FirstPartitionFallbackNone vp8FirstPartitionFallbackStage = iota
	vp8FirstPartitionFallbackTokenProbs
	vp8FirstPartitionFallbackTwoSegments
	vp8FirstPartitionFallbackNoSegmentation
	vp8FirstPartitionFallbackLimitedY4
	vp8FirstPartitionFallbackY16Only
	vp8FirstPartitionFallbackDCPrediction
)

func vp8FirstPartitionWithFallback(source vp8Source, cfg vp8LossyConfig, work *vp8EncodeBuffers, plan vp8FramePlan, limit int) ([]byte, vp8LossyConfig, vp8FramePlan, vp8FirstPartitionFallbackStage, error) {
	firstPart, err := vp8FirstPartitionWithLimit(plan.mbw, plan.mbh, cfg.qIndex, cfg.quantDeltas, cfg.filter, &plan.segmentation, plan.modes, plan.tokenProbs, plan.skipMap, plan.skipProb, limit)
	if err == nil {
		return firstPart, cfg, plan, vp8FirstPartitionFallbackNone, nil
	}
	countLossyCounter(lossyCounterFirstPartitionFallbacks, 1)

	fallbackCfg := cfg
	fallbackCfg.updateTokenProb = false
	if plan.tokenProbs != vp8DefaultTokenProbs {
		plan.tokenProbs = vp8DefaultTokenProbs
		if firstPart, ok := fittingVP8FirstPartition(fallbackCfg, plan, limit); ok {
			return firstPart, fallbackCfg, plan, vp8FirstPartitionFallbackTokenProbs, nil
		}
	}

	if plan.segmentation.count > 2 {
		fallbackCfg.maxSegments = 2
		plan = makeVP8FramePlan(source, fallbackCfg, work)
		if firstPart, ok := fittingVP8FirstPartition(fallbackCfg, plan, limit); ok {
			return firstPart, fallbackCfg, plan, vp8FirstPartitionFallbackTwoSegments, nil
		}
	}

	if plan.segmentation.enabled() {
		fallbackCfg.maxSegments = 1
		plan = makeVP8FramePlan(source, fallbackCfg, work)
		if firstPart, ok := fittingVP8FirstPartition(fallbackCfg, plan, limit); ok {
			return firstPart, fallbackCfg, plan, vp8FirstPartitionFallbackNoSegmentation, nil
		}
	} else {
		fallbackCfg.maxSegments = 1
	}

	if fallbackCfg.tryY4 {
		fallbackCfg.y4SearchStride = 2
		plan = makeVP8FramePlan(source, fallbackCfg, work)
		if firstPart, ok := fittingVP8FirstPartition(fallbackCfg, plan, limit); ok {
			return firstPart, fallbackCfg, plan, vp8FirstPartitionFallbackLimitedY4, nil
		}

		fallbackCfg.tryY4 = false
		fallbackCfg.y4SearchStride = 0
		plan = makeVP8FramePlan(source, fallbackCfg, work)
		if firstPart, ok := fittingVP8FirstPartition(fallbackCfg, plan, limit); ok {
			return firstPart, fallbackCfg, plan, vp8FirstPartitionFallbackY16Only, nil
		}
	}

	fallbackCfg = vp8DCPredictionFallbackConfig(fallbackCfg)
	plan = makeVP8FramePlan(source, fallbackCfg, work)
	if firstPart, ok := fittingVP8FirstPartition(fallbackCfg, plan, limit); ok {
		return firstPart, fallbackCfg, plan, vp8FirstPartitionFallbackDCPrediction, nil
	}
	return nil, fallbackCfg, plan, vp8FirstPartitionFallbackDCPrediction, fmt.Errorf("webp: lossy image is too large for the simple VP8 first partition after fallback")
}

func vp8DCPredictionFallbackConfig(cfg vp8LossyConfig) vp8LossyConfig {
	cfg.tryY4 = false
	cfg.trySkip = false
	cfg.updateTokenProb = false
	cfg.bufferResiduals = false
	cfg.maxSegments = 1
	cfg.rdPasses = 1
	cfg.trellis = false
	cfg.dcDiffusion = false
	cfg.forceDCPrediction = true
	return cfg
}

func fittingVP8FirstPartition(cfg vp8LossyConfig, plan vp8FramePlan, limit int) ([]byte, bool) {
	if vp8FirstPartitionSize(plan.mbw, plan.mbh, cfg.qIndex, cfg.quantDeltas, cfg.filter, &plan.segmentation, plan.modes, plan.tokenProbs, plan.skipMap, plan.skipProb) > limit {
		return nil, false
	}
	firstPart, err := vp8FirstPartitionWithLimit(plan.mbw, plan.mbh, cfg.qIndex, cfg.quantDeltas, cfg.filter, &plan.segmentation, plan.modes, plan.tokenProbs, plan.skipMap, plan.skipProb, limit)
	return firstPart, err == nil
}
