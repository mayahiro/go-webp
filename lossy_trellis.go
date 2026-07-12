package webp

type vp8QuantBias struct {
	y1DC int
	y1AC int
	y2DC int
	y2AC int
	uvDC int
	uvAC int
}

func vp8NeutralQuantBias() vp8QuantBias {
	return vp8QuantBias{y1DC: 128, y1AC: 128, y2DC: 128, y2AC: 128, uvDC: 128, uvAC: 128}
}

func vp8MildQuantBias() vp8QuantBias {
	return vp8QuantBias{y1DC: 116, y1AC: 116, y2DC: 116, y2AC: 116, uvDC: 124, uvAC: 124}
}

func vp8MildQuantBiasForIndex(qIndex int) vp8QuantBias {
	bias := vp8MildQuantBias()
	if qIndex < 26 {
		bias.uvDC = clipInt(130-qIndex/4, 124, 128)
		bias.uvAC = bias.uvDC
		return bias
	}
	bias.uvDC = clipInt(150-qIndex, 96, 124)
	bias.uvAC = bias.uvDC
	return bias
}

func vp8ConservativeQuantBias() vp8QuantBias {
	return vp8QuantBias{y1DC: 124, y1AC: 124, y2DC: 124, y2AC: 124, uvDC: 124, uvAC: 124}
}

func (q vp8Quant) withTrellis(tokenProbs *vp8TokenProbs) vp8Quant {
	q.trellisProbs = tokenProbs
	q.trellisPasses = 2
	return q
}

func (q vp8Quant) quantizeY1(residual [16]int, plane int, context uint8) vp8QuantizedBlock {
	transformed := forwardDCT4(residual)
	return quantizeTransformedVP8BlockRDBiasedPasses(transformed, q.y1DC, q.y1AC, q.bias.y1DC, q.bias.y1AC, plane, context, 0, vp8TrellisLambda(q.y1AC), q.trellisProbs, q.trellisPasses)
}

func (q vp8Quant) quantizeY1AC(transformed [16]int, context uint8) vp8QuantizedBlock {
	return quantizeTransformedVP8BlockRDBiasedPasses(transformed, 0, q.y1AC, q.bias.y1DC, q.bias.y1AC, vp8PlaneY1WithY2, context, 1, vp8TrellisLambda(q.y1AC), q.trellisProbs, q.trellisPasses)
}

func (q vp8Quant) quantizeY2(transformed [16]int, context uint8) vp8QuantizedBlock {
	return quantizeTransformedVP8BlockBiased(transformed, q.y2DC, q.y2AC, q.bias.y2DC, q.bias.y2AC)
}

func (q vp8Quant) quantizeUV(residual [16]int, context uint8) vp8QuantizedBlock {
	transformed := forwardDCT4(residual)
	return q.quantizeUVTransformed(transformed)
}

func (q vp8Quant) quantizeUVTransformed(transformed [16]int) vp8QuantizedBlock {
	return quantizeTransformedVP8BlockBiased(transformed, q.uvDC, q.uvAC, q.bias.uvDC, q.bias.uvAC)
}

func vp8TrellisLambda(q int) int64 {
	return max(vp8RDLambda(q)/16, 1)
}

func quantizeTransformedVP8BlockRD(transformed [16]int, dcQ int, acQ int, plane int, context uint8, start int, lambda int64, tokenProbs *vp8TokenProbs) vp8QuantizedBlock {
	return quantizeTransformedVP8BlockRDBiased(transformed, dcQ, acQ, 128, 128, plane, context, start, lambda, tokenProbs)
}

func quantizeTransformedVP8BlockRDBiased(transformed [16]int, dcQ int, acQ int, dcBias int, acBias int, plane int, context uint8, start int, lambda int64, tokenProbs *vp8TokenProbs) vp8QuantizedBlock {
	return quantizeTransformedVP8BlockRDBiasedPasses(transformed, dcQ, acQ, dcBias, acBias, plane, context, start, lambda, tokenProbs, 2)
}

func quantizeTransformedVP8BlockRDBiasedPasses(transformed [16]int, dcQ int, acQ int, dcBias int, acBias int, plane int, context uint8, start int, lambda int64, tokenProbs *vp8TokenProbs, maxPasses int) vp8QuantizedBlock {
	coeff := quantizeTransformedVP8BlockFromBiased(transformed, dcQ, acQ, dcBias, acBias, start)
	if tokenProbs == nil {
		return coeff
	}
	countLossyCounter(lossyCounterTrellisBlocks, 1)
	if vp8LastNonZeroCoeffPtr(&coeff, start) < start {
		return coeff
	}

	transformError := vp8TrellisTransformError(transformed, coeff, dcQ, acQ, start)
	rate := vp8BlockBitCostFromWithProbs(tokenProbs, plane, context, coeff, start)
	bestScore := vp8TrellisScore(transformError, rate, lambda)
	maxPasses = maxInt(maxPasses, 1)
	for pass := 0; pass < maxPasses; pass++ {
		countLossyCounter(lossyCounterTrellisPasses, 1)
		prefixes, last := vp8TrellisTokenPrefixes(tokenProbs, plane, context, &coeff, start)
		if last < start {
			break
		}
		var suffixes [16][3]int64
		hasHigherNonZero := false
		changed := false
		for n := last; n >= start; n-- {
			index := int(vp8Zigzag[n])
			level := coeff[index]
			if level == 0 {
				hasHigherNonZero = vp8TrellisPrependTokenSuffix(&suffixes, hasHigherNonZero, tokenProbs, plane, &coeff, n)
				continue
			}
			countLossyCounter(lossyCounterTrellisCoefficientVisits, 1)
			towardZero := level - 1
			if level < 0 {
				towardZero = level + 1
			}
			candidates := [2]int16{towardZero, 0}
			bestLevel := level
			bestTransformError := transformError
			quantizer := acQ
			if index == 0 {
				quantizer = dcQ
			}
			oldDelta := int64(transformed[index] - int(level)*quantizer)
			for candidateIndex, candidate := range candidates {
				if candidate == level || candidate == bestLevel || candidateIndex > 0 && candidate == candidates[0] {
					continue
				}
				coeff[index] = candidate
				newDelta := int64(transformed[index] - int(candidate)*quantizer)
				candidateTransformError := transformError - oldDelta*oldDelta + newDelta*newDelta
				candidateRate := int64(0)
				if candidate == 0 && !hasHigherNonZero {
					candidateRate = vp8BlockBitCostFromWithProbs(tokenProbs, plane, context, coeff, start)
				} else {
					candidateRate = vp8TrellisCandidateTokenRate(prefixes[n], &suffixes, hasHigherNonZero, tokenProbs, plane, n, candidate)
				}
				score := vp8TrellisScore(candidateTransformError, candidateRate, lambda)
				if score < bestScore || score == bestScore && absInt(int(candidate)) < absInt(int(bestLevel)) {
					bestScore = score
					bestLevel = candidate
					bestTransformError = candidateTransformError
				}
			}
			coeff[index] = bestLevel
			if bestLevel != level {
				countLossyCounter(lossyCounterTrellisLevelChanges, 1)
				transformError = bestTransformError
				changed = true
			}
			hasHigherNonZero = vp8TrellisPrependTokenSuffix(&suffixes, hasHigherNonZero, tokenProbs, plane, &coeff, n)
		}
		if !changed {
			break
		}
	}
	return coeff
}

type vp8TrellisTokenPrefix struct {
	cost int64
	prob *[11]uint8
}

func vp8TrellisTokenPrefixes(probs *vp8TokenProbs, plane int, context uint8, coeff *vp8QuantizedBlock, start int) ([16]vp8TrellisTokenPrefix, int) {
	var prefixes [16]vp8TrellisTokenPrefix
	context = min(context, 2)
	last := vp8LastNonZeroCoeffPtr(coeff, start)
	if last < start {
		return prefixes, last
	}
	prob := vp8TokenProbPtr(probs, plane, int(vp8Bands[start]), context)
	cost := vp8BitCost(prob[0], true)
	for n := start; n <= last; n++ {
		prefixes[n] = vp8TrellisTokenPrefix{cost: cost, prob: prob}
		value := (*coeff)[vp8Zigzag[n]]
		if value == 0 {
			cost += vp8BitCost(prob[1], false)
			prob = vp8TokenProbPtr(probs, plane, int(vp8Bands[n+1]), 0)
			continue
		}
		cost += vp8BitCost(prob[1], true)
		absolute := absInt(int(value))
		cost += vp8CoeffValueBitCostFrom(prob, absolute)
		cost += vp8BitCost(128, value < 0)
		if n == 15 {
			break
		}
		prob = vp8TokenProbPtr(probs, plane, int(vp8Bands[n+1]), coeffContext(absolute))
		cost += vp8BitCost(prob[0], true)
	}
	return prefixes, last
}

func vp8TrellisCandidateTokenRate(prefix vp8TrellisTokenPrefix, suffixes *[16][3]int64, hasHigherNonZero bool, probs *vp8TokenProbs, plane int, position int, candidate int16) int64 {
	cost := prefix.cost
	prob := prefix.prob
	if candidate == 0 {
		cost += vp8BitCost(prob[1], false)
		return cost + suffixes[position+1][0]
	}
	cost += vp8BitCost(prob[1], true)
	absolute := absInt(int(candidate))
	cost += vp8CoeffValueBitCostFrom(prob, absolute)
	cost += vp8BitCost(128, candidate < 0)
	if position == 15 {
		return cost
	}
	context := coeffContext(absolute)
	prob = vp8TokenProbPtr(probs, plane, int(vp8Bands[position+1]), context)
	if !hasHigherNonZero {
		return cost + vp8BitCost(prob[0], false)
	}
	return cost + vp8BitCost(prob[0], true) + suffixes[position+1][context]
}

func vp8TrellisPrependTokenSuffix(suffixes *[16][3]int64, hasHigherNonZero bool, probs *vp8TokenProbs, plane int, coeff *vp8QuantizedBlock, position int) bool {
	value := (*coeff)[vp8Zigzag[position]]
	for context := 0; context < 3; context++ {
		prob := vp8TokenProbPtr(probs, plane, int(vp8Bands[position]), uint8(context))
		if value == 0 {
			cost := vp8BitCost(prob[1], false)
			if position < 15 {
				cost += suffixes[position+1][0]
			}
			suffixes[position][context] = cost
			continue
		}
		cost := vp8BitCost(prob[1], true)
		absolute := absInt(int(value))
		cost += vp8CoeffValueBitCostFrom(prob, absolute)
		cost += vp8BitCost(128, value < 0)
		if position < 15 {
			nextContext := coeffContext(absolute)
			nextProb := vp8TokenProbPtr(probs, plane, int(vp8Bands[position+1]), nextContext)
			if hasHigherNonZero {
				cost += vp8BitCost(nextProb[0], true) + suffixes[position+1][nextContext]
			} else {
				cost += vp8BitCost(nextProb[0], false)
			}
		}
		suffixes[position][context] = cost
	}
	return hasHigherNonZero || value != 0
}

func quantizeTransformedVP8BlockFrom(transformed [16]int, dcQ int, acQ int, start int) vp8QuantizedBlock {
	return quantizeTransformedVP8BlockFromBiased(transformed, dcQ, acQ, 128, 128, start)
}

func quantizeTransformedVP8BlockFromBiased(transformed [16]int, dcQ int, acQ int, dcBias int, acBias int, start int) vp8QuantizedBlock {
	var coeff vp8QuantizedBlock
	if start == 0 {
		coeff[0] = quantizeTransformCoeffBiased(transformed[0], dcQ, dcBias)
	}
	for i := max(start, 1); i < len(coeff); i++ {
		coeff[i] = quantizeTransformCoeffBiased(transformed[i], acQ, acBias)
	}
	return coeff
}

func vp8TrellisBlockScore(transformed [16]int, coeff vp8QuantizedBlock, dcQ int, acQ int, plane int, context uint8, start int, lambda int64, tokenProbs *vp8TokenProbs) int64 {
	transformError := vp8TrellisTransformError(transformed, coeff, dcQ, acQ, start)
	rate := vp8BlockBitCostFromWithProbs(tokenProbs, plane, context, coeff, start)
	return vp8TrellisScore(transformError, rate, lambda)
}

func vp8TrellisTransformError(transformed [16]int, coeff vp8QuantizedBlock, dcQ int, acQ int, start int) int64 {
	var transformError int64
	for n := start; n < len(vp8Zigzag); n++ {
		index := int(vp8Zigzag[n])
		quantizer := acQ
		if index == 0 {
			quantizer = dcQ
		}
		delta := int64(transformed[index] - int(coeff[index])*quantizer)
		transformError += delta * delta
	}
	return transformError
}

func vp8TrellisScore(transformError int64, rate int64, lambda int64) int64 {
	countLossyCounter(lossyCounterTrellisCandidateScores, 1)
	return (transformError+2)/4 + (rate*lambda+128)/256
}
