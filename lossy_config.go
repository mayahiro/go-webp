package webp

type vp8QualityProfile struct {
	qIndex          int
	quant           vp8Quant
	quantDeltas     vp8QuantDeltas
	quantBias       vp8QuantBias
	filter          vp8LoopFilter
	rd              vp8RDConfig
	rdYLambdaScale  int
	rdUVLambdaScale int
	textureStrength int
}

type vp8EffortProfile struct {
	tryY4                  bool
	trySkip                bool
	updateTokenProb        bool
	bufferResiduals        bool
	commitWinningResiduals bool
	defaultFrameIncumbent  bool
	materializeSource      bool
	maxSegments            int
	segmentStrength        int
	rdPasses               int
	trellis                bool
	trellisPasses          int
	dcDiffusion            bool
	sharpYUV               bool
	parallelAlpha          bool
	y4SearchStride         int
	y4FlatnessLimit        int
	y4RefinementBeamWidth  int
	forceDCPrediction      bool
}

func vp8QualityProfileForQIndex(qIndex int) vp8QualityProfile {
	qIndex = clipInt(qIndex, 0, 127)
	quantDeltas := vp8QuantDeltas{uvDC: -2}
	quantBias := vp8MildQuantBiasForIndex(qIndex)
	yLambdaScale, uvLambdaScale := 64, 96
	textureStrength := 0
	if qIndex <= 9 {
		yLambdaScale = 32
	} else if qIndex >= 15 && qIndex <= 30 {
		quantBias.y1DC = 114
		quantBias.y1AC = 114
		quantBias.y2DC = 114
		quantBias.y2AC = 114
		textureStrength = 200
	}
	return makeVP8QualityProfile(qIndex, quantDeltas, quantBias, yLambdaScale, uvLambdaScale, textureStrength)
}

func vp8ConservativeQualityProfileForQIndex(qIndex int) vp8QualityProfile {
	qIndex = clipInt(qIndex, 0, 127)
	return makeVP8QualityProfile(qIndex, vp8QuantDeltas{uvDC: -2}, vp8ConservativeQuantBias(), 256, 256, 0)
}

func makeVP8QualityProfile(qIndex int, quantDeltas vp8QuantDeltas, quantBias vp8QuantBias, yLambdaScale int, uvLambdaScale int, textureStrength int) vp8QualityProfile {
	quant := vp8QuantForIndexDeltasBias(qIndex, quantDeltas, quantBias)
	return vp8QualityProfile{
		qIndex:          qIndex,
		quant:           quant,
		quantDeltas:     quantDeltas,
		quantBias:       quantBias,
		filter:          vp8LoopFilterForQuant(quant),
		rd:              newVP8RDConfigScaledTexture(quant, yLambdaScale, uvLambdaScale, textureStrength),
		rdYLambdaScale:  yLambdaScale,
		rdUVLambdaScale: uvLambdaScale,
		textureStrength: textureStrength,
	}
}

func vp8EffortProfileForModeQIndex(mode Mode, qIndex int) vp8EffortProfile {
	highQualitySearch := qIndex <= 9
	effort := vp8EffortProfile{
		tryY4:                  true,
		trySkip:                true,
		updateTokenProb:        true,
		bufferResiduals:        true,
		commitWinningResiduals: true,
		materializeSource:      highQualitySearch,
		maxSegments:            4,
		rdPasses:               1,
		sharpYUV:               highQualitySearch,
		parallelAlpha:          true,
		y4FlatnessLimit:        1,
	}
	switch mode {
	case ModeBestCompression:
		effort.materializeSource = true
		effort.rdPasses = 2
		effort.sharpYUV = true
		effort.y4FlatnessLimit = 0
		effort.y4RefinementBeamWidth = 2
		effort.defaultFrameIncumbent = true
	case ModeFast:
		effort.tryY4 = false
		effort.trySkip = false
		effort.updateTokenProb = false
		effort.bufferResiduals = false
		effort.materializeSource = false
		effort.maxSegments = 1
		effort.parallelAlpha = false
	case ModeLowMemory:
		effort.tryY4 = false
		effort.bufferResiduals = false
		effort.materializeSource = false
		effort.maxSegments = 1
		effort.parallelAlpha = false
	}
	return effort
}

func makeVP8LossyConfig(quality vp8QualityProfile, effort vp8EffortProfile) vp8LossyConfig {
	return vp8LossyConfig{
		qIndex:                 quality.qIndex,
		quant:                  quality.quant,
		quantDeltas:            quality.quantDeltas,
		quantBias:              quality.quantBias,
		filter:                 quality.filter,
		rd:                     quality.rd,
		rdYLambdaScale:         quality.rdYLambdaScale,
		rdUVLambdaScale:        quality.rdUVLambdaScale,
		tryY4:                  effort.tryY4,
		trySkip:                effort.trySkip,
		updateTokenProb:        effort.updateTokenProb,
		bufferResiduals:        effort.bufferResiduals,
		commitWinningResiduals: effort.commitWinningResiduals,
		defaultFrameIncumbent:  effort.defaultFrameIncumbent,
		materializeSource:      effort.materializeSource,
		maxSegments:            effort.maxSegments,
		segmentStrength:        effort.segmentStrength,
		textureStrength:        quality.textureStrength,
		rdPasses:               effort.rdPasses,
		trellis:                effort.trellis,
		trellisPasses:          effort.trellisPasses,
		dcDiffusion:            effort.dcDiffusion,
		sharpYUV:               effort.sharpYUV,
		parallelAlpha:          effort.parallelAlpha,
		y4SearchStride:         effort.y4SearchStride,
		y4FlatnessLimit:        effort.y4FlatnessLimit,
		y4RefinementBeamWidth:  effort.y4RefinementBeamWidth,
		forceDCPrediction:      effort.forceDCPrediction,
	}
}

func (cfg vp8LossyConfig) qualityProfile() vp8QualityProfile {
	return vp8QualityProfile{
		qIndex:          cfg.qIndex,
		quant:           cfg.quant,
		quantDeltas:     cfg.quantDeltas,
		quantBias:       cfg.quantBias,
		filter:          cfg.filter,
		rd:              cfg.rd,
		rdYLambdaScale:  cfg.rdYLambdaScale,
		rdUVLambdaScale: cfg.rdUVLambdaScale,
		textureStrength: cfg.textureStrength,
	}
}
