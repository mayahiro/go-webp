package webp

const (
	vp8PlaneUV       = 2
	vp8PlaneY1SansY2 = 3
)

var vp8Cat3456 = [4][]uint8{
	{173, 148, 140},
	{176, 155, 140, 135},
	{180, 157, 141, 134, 130},
	{254, 254, 243, 230, 196, 177, 153, 140, 133, 130, 129},
}

var vp8UVTokenProb = [2][3][11]uint8{
	{
		{253, 9, 248, 251, 207, 208, 255, 192, 128, 128, 128},
		{175, 13, 224, 243, 193, 185, 249, 198, 255, 255, 128},
		{73, 17, 171, 221, 161, 179, 236, 167, 255, 234, 128},
	},
	{
		{1, 95, 247, 253, 212, 183, 255, 255, 128, 128, 128},
		{239, 90, 244, 250, 211, 209, 255, 255, 128, 128, 128},
		{155, 77, 195, 248, 188, 195, 255, 255, 128, 128, 128},
	},
}

var vp8Y1SansY2TokenProb = [2][3][11]uint8{
	{
		{202, 24, 213, 235, 186, 191, 220, 160, 240, 175, 255},
		{126, 38, 182, 232, 169, 184, 228, 174, 255, 187, 128},
		{61, 46, 138, 219, 151, 178, 240, 170, 255, 216, 128},
	},
	{
		{1, 112, 230, 250, 199, 191, 247, 159, 255, 255, 128},
		{166, 109, 228, 252, 211, 215, 255, 174, 128, 128, 128},
		{39, 77, 162, 232, 172, 180, 245, 178, 255, 255, 128},
	},
}

func encodeVP8Coeff(enc *vp8BoolEncoder, plane int, context uint8, coeff int) uint8 {
	if context > 2 {
		context = 2
	}
	prob := vp8TokenProb(plane, 0, context)
	if coeff == 0 {
		enc.writeBit(prob[0], false)
		return 0
	}

	enc.writeBit(prob[0], true)
	enc.writeBit(prob[1], true)
	absCoeff := coeff
	if absCoeff < 0 {
		absCoeff = -absCoeff
	}
	encodeVP8CoeffValue(enc, prob, absCoeff)
	enc.writeBit(128, coeff < 0)

	endProb := vp8TokenProb(plane, 1, coeffContext(absCoeff))
	enc.writeBit(endProb[0], false)
	return 1
}

func encodeVP8CoeffValue(enc *vp8BoolEncoder, prob [11]uint8, v int) {
	if v <= 1 {
		enc.writeBit(prob[2], false)
		return
	}

	enc.writeBit(prob[2], true)
	if v == 2 {
		enc.writeBit(prob[3], false)
		enc.writeBit(prob[4], false)
		return
	}
	if v <= 4 {
		enc.writeBit(prob[3], false)
		enc.writeBit(prob[4], true)
		enc.writeBit(prob[5], v == 4)
		return
	}

	enc.writeBit(prob[3], true)
	if v <= 6 {
		enc.writeBit(prob[6], false)
		enc.writeBit(prob[7], false)
		enc.writeBit(159, v == 6)
		return
	}
	if v <= 10 {
		d := v - 7
		enc.writeBit(prob[6], false)
		enc.writeBit(prob[7], true)
		enc.writeBit(165, d&2 != 0)
		enc.writeBit(145, d&1 != 0)
		return
	}

	enc.writeBit(prob[6], true)
	cat, offset := vp8CoeffCategory(v)
	enc.writeBit(prob[8], cat&2 != 0)
	enc.writeBit(prob[9+cat/2], cat&1 != 0)
	writeVP8CategoryBits(enc, vp8Cat3456[cat], v-offset)
}

func vp8CoeffCategory(v int) (cat int, offset int) {
	switch {
	case v <= 18:
		return 0, 11
	case v <= 34:
		return 1, 19
	case v <= 66:
		return 2, 35
	default:
		return 3, 67
	}
}

func writeVP8CategoryBits(enc *vp8BoolEncoder, probs []uint8, value int) {
	for i, prob := range probs {
		shift := len(probs) - 1 - i
		enc.writeBit(prob, value&(1<<shift) != 0)
	}
}

func coeffContext(v int) uint8 {
	if v == 1 {
		return 1
	}
	return 2
}

func vp8TokenProb(plane int, band int, context uint8) [11]uint8 {
	if context > 2 {
		context = 2
	}
	if band != 0 {
		band = 1
	}
	if plane == vp8PlaneUV {
		return vp8UVTokenProb[band][context]
	}
	return vp8Y1SansY2TokenProb[band][context]
}
