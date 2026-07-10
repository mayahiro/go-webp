package webp

type vp8ModePass struct {
	modes          []vp8MBMode
	residualBuffer *vp8ResidualBuffer
}

func runVP8ModePass(source vp8Source, cfg vp8LossyConfig, work *vp8EncodeBuffers, mbw int, mbh int, segmentation *vp8Segmentation, tokenProbs *vp8TokenProbs, bufferResiduals bool) vp8ModePass {
	useTrellis := cfg.trellis && bufferResiduals && tokenProbs != nil
	useDCDiffusion := cfg.dcDiffusion && bufferResiduals && tokenProbs != nil && segmentation.useDCDiffusion()
	cfg.trellis = false
	clearVP8Reconstruction(work)
	if bufferResiduals && !cfg.tryY4 {
		residualBuffer := work.resetResidualBuffer(mbw * mbh)
		sink := vp8ResidualSink{buffer: residualBuffer}
		modes := analyzeVP8ModesConfigWithSink(source.readLuma, source.readChroma, source.bounds, mbw, mbh, cfg, work, segmentation, tokenProbs, &sink)
		return vp8ModePass{modes: modes, residualBuffer: residualBuffer}
	}

	modes := analyzeVP8ModesConfigWithSink(source.readLuma, source.readChroma, source.bounds, mbw, mbh, cfg, work, segmentation, tokenProbs, nil)
	if !bufferResiduals {
		return vp8ModePass{modes: modes}
	}
	clearVP8Reconstruction(work)
	baseQuant := cfg.quant
	if useTrellis {
		baseQuant = baseQuant.withTrellis(tokenProbs)
	}
	if useDCDiffusion {
		baseQuant.dcDiffusion = newVP8DCDiffusion(mbw)
	}
	residualBuffer := collectVP8ResidualBuffer(source.readLuma, source.readChroma, source.bounds, mbw, mbh, baseQuant, segmentation, modes, work)
	return vp8ModePass{modes: modes, residualBuffer: residualBuffer}
}

func analyzeVP8ModePassEntropy(source vp8Source, cfg vp8LossyConfig, work *vp8EncodeBuffers, mbw int, mbh int, segmentation *vp8Segmentation, pass vp8ModePass) (vp8TokenProbs, []bool) {
	tokenProbs := vp8DefaultTokenProbs
	if pass.residualBuffer != nil {
		var skipWorkspace []bool
		if cfg.trySkip {
			skipWorkspace = work.resetSkipMap(mbw * mbh)
		}
		skipMap := pass.residualBuffer.candidateSkipMapInto(cfg.trySkip, skipWorkspace)
		if cfg.updateTokenProb {
			tokenStats := pass.residualBuffer.tokenStats(skipMap)
			tokenProbs = chooseVP8TokenProbsConfig(&tokenStats, true)
		}
		if skipMap != nil && !pass.residualBuffer.shouldUseSkipMap(skipMap, &tokenProbs) {
			skipMap = nil
			if cfg.updateTokenProb {
				tokenStats := pass.residualBuffer.tokenStats(nil)
				tokenProbs = chooseVP8TokenProbsConfig(&tokenStats, true)
			}
		}
		return tokenProbs, skipMap
	}

	var skipMap []bool
	if cfg.trySkip {
		clearVP8Reconstruction(work)
		skipMap = analyzeVP8MacroblockSkips(source.readLuma, source.readChroma, source.bounds, mbw, mbh, cfg.quant, segmentation, pass.modes, work)
	}
	if cfg.updateTokenProb {
		clearVP8Reconstruction(work)
		tokenStats := collectVP8TokenStatsConfig(source.readLuma, source.readChroma, source.bounds, mbw, mbh, cfg.quant, segmentation, pass.modes, work, skipMap)
		tokenProbs = chooseVP8TokenProbsConfig(&tokenStats, true)
	}
	return tokenProbs, skipMap
}

func clearVP8Reconstruction(work *vp8EncodeBuffers) {
	clear(work.recY)
	clear(work.recCb)
	clear(work.recCr)
}
