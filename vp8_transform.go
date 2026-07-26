package webp

func quantizeVP8Block(residual [16]int, dcQ int, acQ int) vp8QuantizedBlock {
	transformed := forwardDCT4(residual)
	return quantizeTransformedVP8Block(transformed, dcQ, acQ)
}

func quantizeTransformedVP8Block(transformed [16]int, dcQ int, acQ int) vp8QuantizedBlock {
	return quantizeTransformedVP8BlockBiased(transformed, dcQ, acQ, 128, 128)
}

func quantizeTransformedVP8BlockBiased(transformed [16]int, dcQ int, acQ int, dcBias int, acBias int) vp8QuantizedBlock {
	var coeff vp8QuantizedBlock
	coeff[0] = quantizeTransformCoeffBiased(transformed[0], dcQ, dcBias)
	for i := 1; i < 16; i++ {
		coeff[i] = quantizeTransformCoeffBiased(transformed[i], acQ, acBias)
	}
	return coeff
}

func quantizeTransformedVP8BlockACOnly(transformed [16]int, acQ int) vp8QuantizedBlock {
	var coeff vp8QuantizedBlock
	for i := 1; i < 16; i++ {
		coeff[i] = quantizeTransformCoeff(transformed[i], acQ)
	}
	return coeff
}

func forwardDCT4(residual [16]int) [16]int {
	countLossyCounter(lossyCounterForwardDCTCount, 1)
	var tmp [16]int
	for i := 0; i < 4; i++ {
		d0 := residual[i*4+0]
		d1 := residual[i*4+1]
		d2 := residual[i*4+2]
		d3 := residual[i*4+3]
		a0 := d0 + d3
		a1 := d1 + d2
		a2 := d1 - d2
		a3 := d0 - d3
		tmp[0+i*4] = (a0 + a1) * 8
		tmp[1+i*4] = (a2*2217 + a3*5352 + 1812) >> 9
		tmp[2+i*4] = (a0 - a1) * 8
		tmp[3+i*4] = (a3*2217 - a2*5352 + 937) >> 9
	}

	var out [16]int
	for i := 0; i < 4; i++ {
		a0 := tmp[0+i] + tmp[12+i]
		a1 := tmp[4+i] + tmp[8+i]
		a2 := tmp[4+i] - tmp[8+i]
		a3 := tmp[0+i] - tmp[12+i]
		out[0+i] = (a0 + a1 + 7) >> 4
		out[4+i] = ((a2*2217 + a3*5352 + 12000) >> 16) + boolInt(a3 != 0)
		out[8+i] = (a0 - a1 + 7) >> 4
		out[12+i] = (a3*2217 - a2*5352 + 51000) >> 16
	}
	return out
}

func quantizeTransformCoeff(v int, q int) int16 {
	return quantizeTransformCoeffBiased(v, q, 128)
}

func quantizeTransformCoeffBiased(v int, q int, bias int) int16 {
	if q <= 0 {
		return 0
	}
	sign := 1
	if v < 0 {
		sign = -1
		v = -v
	}
	bias = clipInt(bias, 0, 255)
	level := (v*256 + q*bias) / (q * 256)
	return int16(sign * clipInt(level, 0, 2047))
}

func forwardWHT4(in [16]int) [16]int {
	var tmp [16]int
	for i := 0; i < 4; i++ {
		a0 := in[i*4+0] + in[i*4+2]
		a1 := in[i*4+1] + in[i*4+3]
		a2 := in[i*4+1] - in[i*4+3]
		a3 := in[i*4+0] - in[i*4+2]
		tmp[0+i*4] = a0 + a1
		tmp[1+i*4] = a3 + a2
		tmp[2+i*4] = a3 - a2
		tmp[3+i*4] = a0 - a1
	}

	var out [16]int
	for i := 0; i < 4; i++ {
		a0 := tmp[0+i] + tmp[8+i]
		a1 := tmp[4+i] + tmp[12+i]
		a2 := tmp[4+i] - tmp[12+i]
		a3 := tmp[0+i] - tmp[8+i]
		out[0+i] = (a0 + a1) >> 1
		out[4+i] = (a3 + a2) >> 1
		out[8+i] = (a3 - a2) >> 1
		out[12+i] = (a0 - a1) >> 1
	}
	return out
}

func inverseWHT4(coeff [16]int) [16]int {
	var tmp [16]int
	for i := 0; i < 4; i++ {
		a0 := coeff[0+i] + coeff[12+i]
		a1 := coeff[4+i] + coeff[8+i]
		a2 := coeff[4+i] - coeff[8+i]
		a3 := coeff[0+i] - coeff[12+i]
		tmp[0+i] = a0 + a1
		tmp[8+i] = a0 - a1
		tmp[4+i] = a3 + a2
		tmp[12+i] = a3 - a2
	}

	var out [16]int
	for i := 0; i < 4; i++ {
		dc := tmp[0+i*4] + 3
		a0 := dc + tmp[3+i*4]
		a1 := tmp[1+i*4] + tmp[2+i*4]
		a2 := tmp[1+i*4] - tmp[2+i*4]
		a3 := dc - tmp[3+i*4]
		out[i*4+0] = (a0 + a1) >> 3
		out[i*4+1] = (a3 + a2) >> 3
		out[i*4+2] = (a0 - a1) >> 3
		out[i*4+3] = (a3 - a2) >> 3
	}
	return out
}

func reconstructVP8Block(pred [16]uint8, coeff vp8QuantizedBlock, dcQ int, acQ int) [16]uint8 {
	return inverseDCT4(pred, dequantizeVP8Block(coeff, dcQ, acQ))
}

func dequantizeVP8Block(coeff vp8QuantizedBlock, dcQ int, acQ int) [16]int {
	var dequant [16]int
	dequant[0] = int(coeff[0]) * dcQ
	for i := 1; i < 16; i++ {
		dequant[i] = int(coeff[i]) * acQ
	}
	return dequant
}

func inverseDCT4(pred [16]uint8, coeff [16]int) [16]uint8 {
	countLossyCounter(lossyCounterInverseDCTCount, 1)
	const (
		c1 = 85627
		c2 = 35468
	)

	var m [16]int
	for i := 0; i < 4; i++ {
		a := coeff[0+i] + coeff[8+i]
		b := coeff[0+i] - coeff[8+i]
		c := (coeff[4+i]*c2)>>16 - (coeff[12+i]*c1)>>16
		d := (coeff[4+i]*c1)>>16 + (coeff[12+i]*c2)>>16
		m[i*4+0] = a + d
		m[i*4+1] = b + c
		m[i*4+2] = b - c
		m[i*4+3] = a - d
	}

	var out [16]uint8
	for j := 0; j < 4; j++ {
		dc := m[0*4+j] + 4
		a := dc + m[2*4+j]
		b := dc - m[2*4+j]
		c := (m[1*4+j]*c2)>>16 - (m[3*4+j]*c1)>>16
		d := (m[1*4+j]*c1)>>16 + (m[3*4+j]*c2)>>16
		out[j*4+0] = clipUint8(int(pred[j*4+0]) + ((a + d) >> 3))
		out[j*4+1] = clipUint8(int(pred[j*4+1]) + ((b + c) >> 3))
		out[j*4+2] = clipUint8(int(pred[j*4+2]) + ((b - c) >> 3))
		out[j*4+3] = clipUint8(int(pred[j*4+3]) + ((a - d) >> 3))
	}
	return out
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func hasNonZeroBlockCoeff(coeff vp8QuantizedBlock) bool {
	return hasNonZeroBlockCoeffFrom(coeff, 0)
}

func hasNonZeroBlockCoeffFrom(coeff vp8QuantizedBlock, start int) bool {
	for i := start; i < len(coeff); i++ {
		if coeff[i] != 0 {
			return true
		}
	}
	return false
}

func clipUint8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func clipInt(v int, min int, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
