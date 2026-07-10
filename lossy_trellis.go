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
	return q
}

func (q vp8Quant) quantizeY1(residual [16]int, plane int, context uint8) vp8QuantizedBlock {
	transformed := forwardDCT4(residual)
	return quantizeTransformedVP8BlockRDBiased(transformed, q.y1DC, q.y1AC, q.bias.y1DC, q.bias.y1AC, plane, context, 0, vp8TrellisLambda(q.y1AC), q.trellisProbs)
}

func (q vp8Quant) quantizeY1AC(transformed [16]int, context uint8) vp8QuantizedBlock {
	return quantizeTransformedVP8BlockRDBiased(transformed, 0, q.y1AC, q.bias.y1DC, q.bias.y1AC, vp8PlaneY1WithY2, context, 1, vp8TrellisLambda(q.y1AC), q.trellisProbs)
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
	coeff := quantizeTransformedVP8BlockFromBiased(transformed, dcQ, acQ, dcBias, acBias, start)
	if tokenProbs == nil {
		return coeff
	}

	bestScore := vp8TrellisBlockScore(transformed, coeff, dcQ, acQ, plane, context, start, lambda, tokenProbs)
	for pass := 0; pass < 2; pass++ {
		changed := false
		for n := 15; n >= start; n-- {
			index := int(vp8Zigzag[n])
			level := coeff[index]
			if level == 0 {
				continue
			}
			towardZero := level - 1
			if level < 0 {
				towardZero = level + 1
			}
			candidates := [2]int16{towardZero, 0}
			bestLevel := level
			for _, candidate := range candidates {
				if candidate == level || candidate == bestLevel {
					continue
				}
				coeff[index] = candidate
				score := vp8TrellisBlockScore(transformed, coeff, dcQ, acQ, plane, context, start, lambda, tokenProbs)
				if score < bestScore || score == bestScore && absInt(int(candidate)) < absInt(int(bestLevel)) {
					bestScore = score
					bestLevel = candidate
				}
			}
			coeff[index] = bestLevel
			if bestLevel != level {
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return coeff
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
	rate := vp8BlockBitCostFromWithProbs(tokenProbs, plane, context, coeff, start)
	return (transformError+2)/4 + (rate*lambda+128)/256
}
