package webp

const (
	vp8PlaneY1WithY2 = iota
	vp8PlaneY2
	vp8PlaneUV
	vp8PlaneY1SansY2 = 3
)

var vp8Cat3456 = [4][]uint8{
	{173, 148, 140},
	{176, 155, 140, 135},
	{180, 157, 141, 134, 130},
	{254, 254, 243, 230, 196, 177, 153, 140, 133, 130, 129},
}

var vp8Bands = [17]uint8{0, 1, 2, 3, 6, 4, 5, 6, 6, 6, 6, 6, 6, 6, 6, 7, 0}

var vp8Zigzag = [16]uint8{0, 1, 4, 8, 5, 2, 3, 6, 9, 12, 13, 10, 7, 11, 14, 15}

type vp8TokenProbs [4][8][3][11]uint8

type vp8TokenBranchCounts struct {
	zero uint32
	one  uint32
}

type vp8TokenStats [4][8][3][11]vp8TokenBranchCounts

// vp8QuantizedBlock stores coefficient levels after quantization.
// VP8 quantization clamps every value to [-2047, 2047], which fits in int16.
type vp8QuantizedBlock [16]int16

var vp8TokenProbUpdateProb = [4][8][3][11]uint8{
	{
		{
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{176, 246, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{223, 241, 252, 255, 255, 255, 255, 255, 255, 255, 255},
			{249, 253, 253, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 244, 252, 255, 255, 255, 255, 255, 255, 255, 255},
			{234, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{253, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 246, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{239, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{254, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 248, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{251, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{251, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{254, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 254, 253, 255, 254, 255, 255, 255, 255, 255, 255},
			{250, 255, 254, 255, 254, 255, 255, 255, 255, 255, 255},
			{254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
	},
	{
		{
			{217, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{225, 252, 241, 253, 255, 255, 254, 255, 255, 255, 255},
			{234, 250, 241, 250, 253, 255, 253, 254, 255, 255, 255},
		},
		{
			{255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{223, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{238, 253, 254, 254, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 248, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{249, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 253, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{247, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{252, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{253, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 254, 253, 255, 255, 255, 255, 255, 255, 255, 255},
			{250, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
	},
	{
		{
			{186, 251, 250, 255, 255, 255, 255, 255, 255, 255, 255},
			{234, 251, 244, 254, 255, 255, 255, 255, 255, 255, 255},
			{251, 251, 243, 253, 254, 255, 254, 255, 255, 255, 255},
		},
		{
			{255, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{236, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{251, 253, 253, 254, 254, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{254, 254, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{254, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
	},
	{
		{
			{248, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{250, 254, 252, 254, 255, 255, 255, 255, 255, 255, 255},
			{248, 254, 249, 253, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 253, 253, 255, 255, 255, 255, 255, 255, 255, 255},
			{246, 253, 253, 255, 255, 255, 255, 255, 255, 255, 255},
			{252, 254, 251, 254, 254, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 254, 252, 255, 255, 255, 255, 255, 255, 255, 255},
			{248, 254, 253, 255, 255, 255, 255, 255, 255, 255, 255},
			{253, 255, 254, 254, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 251, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{245, 251, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{253, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 251, 253, 255, 255, 255, 255, 255, 255, 255, 255},
			{252, 253, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 254, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 252, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{249, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 254, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 255, 253, 255, 255, 255, 255, 255, 255, 255, 255},
			{250, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{254, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
			{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		},
	},
}

var vp8Y1WithY2TokenProb = [8][3][11]uint8{
	{
		{128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128},
		{128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128},
		{128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128},
	},
	{
		{253, 136, 254, 255, 228, 219, 128, 128, 128, 128, 128},
		{189, 129, 242, 255, 227, 213, 255, 219, 128, 128, 128},
		{106, 126, 227, 252, 214, 209, 255, 255, 128, 128, 128},
	},
	{
		{1, 98, 248, 255, 236, 226, 255, 255, 128, 128, 128},
		{181, 133, 238, 254, 221, 234, 255, 154, 128, 128, 128},
		{78, 134, 202, 247, 198, 180, 255, 219, 128, 128, 128},
	},
	{
		{1, 185, 249, 255, 243, 255, 128, 128, 128, 128, 128},
		{184, 150, 247, 255, 236, 224, 128, 128, 128, 128, 128},
		{77, 110, 216, 255, 236, 230, 128, 128, 128, 128, 128},
	},
	{
		{1, 101, 251, 255, 241, 255, 128, 128, 128, 128, 128},
		{170, 139, 241, 252, 236, 209, 255, 255, 128, 128, 128},
		{37, 116, 196, 243, 228, 255, 255, 255, 128, 128, 128},
	},
	{
		{1, 204, 254, 255, 245, 255, 128, 128, 128, 128, 128},
		{207, 160, 250, 255, 238, 128, 128, 128, 128, 128, 128},
		{102, 103, 231, 255, 211, 171, 128, 128, 128, 128, 128},
	},
	{
		{1, 152, 252, 255, 240, 255, 128, 128, 128, 128, 128},
		{177, 135, 243, 255, 234, 225, 128, 128, 128, 128, 128},
		{80, 129, 211, 255, 194, 224, 128, 128, 128, 128, 128},
	},
	{
		{1, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128},
		{246, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128},
		{255, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128},
	},
}

var vp8Y2TokenProb = [8][3][11]uint8{
	{
		{198, 35, 237, 223, 193, 187, 162, 160, 145, 155, 62},
		{131, 45, 198, 221, 172, 176, 220, 157, 252, 221, 1},
		{68, 47, 146, 208, 149, 167, 221, 162, 255, 223, 128},
	},
	{
		{1, 149, 241, 255, 221, 224, 255, 255, 128, 128, 128},
		{184, 141, 234, 253, 222, 220, 255, 199, 128, 128, 128},
		{81, 99, 181, 242, 176, 190, 249, 202, 255, 255, 128},
	},
	{
		{1, 129, 232, 253, 214, 197, 242, 196, 255, 255, 128},
		{99, 121, 210, 250, 201, 198, 255, 202, 128, 128, 128},
		{23, 91, 163, 242, 170, 187, 247, 210, 255, 255, 128},
	},
	{
		{1, 200, 246, 255, 234, 255, 128, 128, 128, 128, 128},
		{109, 178, 241, 255, 231, 245, 255, 255, 128, 128, 128},
		{44, 130, 201, 253, 205, 192, 255, 255, 128, 128, 128},
	},
	{
		{1, 132, 239, 251, 219, 209, 255, 165, 128, 128, 128},
		{94, 136, 225, 251, 218, 190, 255, 255, 128, 128, 128},
		{22, 100, 174, 245, 186, 161, 255, 199, 128, 128, 128},
	},
	{
		{1, 182, 249, 255, 232, 235, 128, 128, 128, 128, 128},
		{124, 143, 241, 255, 227, 234, 128, 128, 128, 128, 128},
		{35, 77, 181, 251, 193, 211, 255, 205, 128, 128, 128},
	},
	{
		{1, 157, 247, 255, 236, 231, 255, 255, 128, 128, 128},
		{121, 141, 235, 255, 225, 227, 255, 255, 128, 128, 128},
		{45, 99, 188, 251, 195, 217, 255, 224, 128, 128, 128},
	},
	{
		{1, 1, 251, 255, 213, 255, 128, 128, 128, 128, 128},
		{203, 1, 248, 255, 255, 128, 128, 128, 128, 128, 128},
		{137, 1, 177, 255, 224, 255, 128, 128, 128, 128, 128},
	},
}

var vp8UVTokenProb = [8][3][11]uint8{
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
	{
		{1, 24, 239, 251, 218, 219, 255, 205, 128, 128, 128},
		{201, 51, 219, 255, 196, 186, 128, 128, 128, 128, 128},
		{69, 46, 190, 239, 201, 218, 255, 228, 128, 128, 128},
	},
	{
		{1, 191, 251, 255, 255, 128, 128, 128, 128, 128, 128},
		{223, 165, 249, 255, 213, 255, 128, 128, 128, 128, 128},
		{141, 124, 248, 255, 255, 128, 128, 128, 128, 128, 128},
	},
	{
		{1, 16, 248, 255, 255, 128, 128, 128, 128, 128, 128},
		{190, 36, 230, 255, 236, 255, 128, 128, 128, 128, 128},
		{149, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128},
	},
	{
		{1, 226, 255, 128, 128, 128, 128, 128, 128, 128, 128},
		{247, 192, 255, 128, 128, 128, 128, 128, 128, 128, 128},
		{240, 128, 255, 128, 128, 128, 128, 128, 128, 128, 128},
	},
	{
		{1, 134, 252, 255, 255, 128, 128, 128, 128, 128, 128},
		{213, 62, 250, 255, 255, 128, 128, 128, 128, 128, 128},
		{55, 93, 255, 128, 128, 128, 128, 128, 128, 128, 128},
	},
	{
		{128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128},
		{128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128},
		{128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128},
	},
}

var vp8Y1SansY2TokenProb = [8][3][11]uint8{
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
	{
		{1, 52, 220, 246, 198, 199, 249, 220, 255, 255, 128},
		{124, 74, 191, 243, 183, 193, 250, 221, 255, 255, 128},
		{24, 71, 130, 219, 154, 170, 243, 182, 255, 255, 128},
	},
	{
		{1, 182, 225, 249, 219, 240, 255, 224, 128, 128, 128},
		{149, 150, 226, 252, 216, 205, 255, 171, 128, 128, 128},
		{28, 108, 170, 242, 183, 194, 254, 223, 255, 255, 128},
	},
	{
		{1, 81, 230, 252, 204, 203, 255, 192, 128, 128, 128},
		{123, 102, 209, 247, 188, 196, 255, 233, 128, 128, 128},
		{20, 95, 153, 243, 164, 173, 255, 203, 128, 128, 128},
	},
	{
		{1, 222, 248, 255, 216, 213, 128, 128, 128, 128, 128},
		{168, 175, 246, 252, 235, 205, 255, 255, 128, 128, 128},
		{47, 116, 215, 255, 211, 212, 255, 255, 128, 128, 128},
	},
	{
		{1, 121, 236, 253, 212, 214, 255, 255, 128, 128, 128},
		{141, 84, 213, 252, 201, 202, 255, 219, 128, 128, 128},
		{42, 80, 160, 240, 162, 185, 255, 205, 128, 128, 128},
	},
	{
		{1, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128},
		{244, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128},
		{238, 1, 255, 128, 128, 128, 128, 128, 128, 128, 128},
	},
}

var vp8DefaultTokenProbs = makeVP8DefaultTokenProbs()

const vp8CoeffValueCostCacheLimit = 256

var vp8DefaultCoeffValueSignBitCosts [4][8][3][vp8CoeffValueCostCacheLimit]int32
var vp8ZeroCoeff vp8QuantizedBlock

func init() {
	initVP8DefaultCoeffValueSignBitCosts()
}

func initVP8DefaultCoeffValueSignBitCosts() {
	signCost := vp8BitCost(128, false)
	for plane := range vp8DefaultCoeffValueSignBitCosts {
		for band := range vp8DefaultCoeffValueSignBitCosts[plane] {
			for context := range vp8DefaultCoeffValueSignBitCosts[plane][band] {
				prob := vp8StaticTokenProb(plane, band, uint8(context))
				for v := range vp8DefaultCoeffValueSignBitCosts[plane][band][context] {
					vp8DefaultCoeffValueSignBitCosts[plane][band][context][v] = int32(vp8CoeffValueBitCost(prob, v) + signCost)
				}
			}
		}
	}
}

func makeVP8DefaultTokenProbs() vp8TokenProbs {
	var probs vp8TokenProbs
	for plane := range probs {
		for band := range probs[plane] {
			for context := range probs[plane][band] {
				probs[plane][band][context] = vp8StaticTokenProb(plane, band, uint8(context))
			}
		}
	}
	return probs
}

func encodeVP8Block(enc *vp8BoolEncoder, plane int, context uint8, coeff vp8QuantizedBlock) uint8 {
	return encodeVP8BlockFromWithProbs(enc, nil, plane, context, coeff, 0)
}

func encodeVP8BlockWithProbs(enc *vp8BoolEncoder, probs *vp8TokenProbs, plane int, context uint8, coeff vp8QuantizedBlock) uint8 {
	return encodeVP8BlockFromWithProbs(enc, probs, plane, context, coeff, 0)
}

func encodeVP8BlockSkipFirst(enc *vp8BoolEncoder, plane int, context uint8, coeff vp8QuantizedBlock) uint8 {
	return encodeVP8BlockFromWithProbs(enc, nil, plane, context, coeff, 1)
}

func encodeVP8BlockSkipFirstWithProbs(enc *vp8BoolEncoder, probs *vp8TokenProbs, plane int, context uint8, coeff vp8QuantizedBlock) uint8 {
	return encodeVP8BlockFromWithProbs(enc, probs, plane, context, coeff, 1)
}

func vp8BlockBitCost(plane int, context uint8, coeff vp8QuantizedBlock) int64 {
	return vp8BlockBitCostFromDefault(plane, context, coeff, 0)
}

func vp8BlockBitCostAndNonZero(plane int, context uint8, coeff vp8QuantizedBlock) (int64, bool) {
	return vp8BlockBitCostFromDefaultAndNonZeroPtr(plane, context, &coeff, 0)
}

func vp8BlockBitCostAndNonZeroPtr(plane int, context uint8, coeff *vp8QuantizedBlock) (int64, bool) {
	return vp8BlockBitCostFromDefaultAndNonZeroPtr(plane, context, coeff, 0)
}

func vp8BlockBitCostFrom(plane int, context uint8, coeff vp8QuantizedBlock, start int) int64 {
	return vp8BlockBitCostFromDefault(plane, context, coeff, start)
}

func vp8BlockBitCostFromAndNonZero(plane int, context uint8, coeff vp8QuantizedBlock, start int) (int64, bool) {
	return vp8BlockBitCostFromDefaultAndNonZeroPtr(plane, context, &coeff, start)
}

func vp8BlockBitCostFromAndNonZeroPtr(plane int, context uint8, coeff *vp8QuantizedBlock, start int) (int64, bool) {
	return vp8BlockBitCostFromDefaultAndNonZeroPtr(plane, context, coeff, start)
}

func vp8BlockBitCostWithProbs(probs *vp8TokenProbs, plane int, context uint8, coeff vp8QuantizedBlock) int64 {
	return vp8BlockBitCostFromWithProbs(probs, plane, context, coeff, 0)
}

func vp8BlockBitCostFromWithProbs(probs *vp8TokenProbs, plane int, context uint8, coeff vp8QuantizedBlock, start int) int64 {
	if context > 2 {
		context = 2
	}
	n := start
	prob := vp8TokenProbPtr(probs, plane, int(vp8Bands[n]), context)
	last := vp8LastNonZeroCoeff(coeff, n)
	if last < n {
		return vp8BitCost(prob[0], false)
	}
	cost := vp8BitCost(prob[0], true)

	for n != 16 {
		n++
		z := int(vp8Zigzag[n-1])
		v := coeff[z]
		if v == 0 {
			cost += vp8BitCost(prob[1], false)
			prob = vp8TokenProbPtr(probs, plane, int(vp8Bands[n]), 0)
			continue
		}

		cost += vp8BitCost(prob[1], true)
		absCoeff := int(v)
		if absCoeff < 0 {
			absCoeff = -absCoeff
		}
		cost += vp8CoeffValueBitCostFrom(prob, absCoeff)
		cost += vp8BitCost(128, v < 0)
		prob = vp8TokenProbPtr(probs, plane, int(vp8Bands[n]), coeffContext(absCoeff))
		if n == 16 {
			return cost
		}
		if n > last {
			cost += vp8BitCost(prob[0], false)
			return cost
		}
		cost += vp8BitCost(prob[0], true)
	}

	return cost
}

func vp8BlockBitCostFromDefault(plane int, context uint8, coeff vp8QuantizedBlock, start int) int64 {
	cost, _ := vp8BlockBitCostFromDefaultAndNonZeroPtr(plane, context, &coeff, start)
	return cost
}

func vp8BlockBitCostFromDefaultAndNonZero(plane int, context uint8, coeff vp8QuantizedBlock, start int) (int64, bool) {
	return vp8BlockBitCostFromDefaultAndNonZeroPtr(plane, context, &coeff, start)
}

func vp8BlockBitCostFromDefaultAndNonZeroPtr(plane int, context uint8, coeff *vp8QuantizedBlock, start int) (int64, bool) {
	if context > 2 {
		context = 2
	}
	n := start
	band := int(vp8Bands[n])
	prob := &vp8DefaultTokenProbs[plane][band][context]
	if *coeff == vp8ZeroCoeff {
		return vp8BitCostTable[prob[0]][0], false
	}
	last := vp8LastNonZeroCoeffPtr(coeff, n)
	if last < n {
		return vp8BitCostTable[prob[0]][0], false
	}
	cost := vp8BitCostTable[prob[0]][1]

	for n != 16 {
		n++
		z := int(vp8Zigzag[n-1])
		v := coeff[z]
		if v == 0 {
			cost += vp8BitCostTable[prob[1]][0]
			band = int(vp8Bands[n])
			context = 0
			prob = &vp8DefaultTokenProbs[plane][band][context]
			continue
		}

		cost += vp8BitCostTable[prob[1]][1]
		absCoeff := int(v)
		if absCoeff < 0 {
			absCoeff = -absCoeff
		}
		if absCoeff < vp8CoeffValueCostCacheLimit {
			cost += int64(vp8DefaultCoeffValueSignBitCosts[plane][band][context][absCoeff])
		} else {
			cost += vp8CoeffValueBitCostFrom(prob, absCoeff) + vp8BitCostTable[128][0]
		}
		context = coeffContext(absCoeff)
		if n == 16 {
			return cost, true
		}
		band = int(vp8Bands[n])
		prob = &vp8DefaultTokenProbs[plane][band][context]
		if n > last {
			cost += vp8BitCostTable[prob[0]][0]
			return cost, true
		}
		cost += vp8BitCostTable[prob[0]][1]
	}

	return cost, true
}

func encodeVP8BlockFrom(enc *vp8BoolEncoder, plane int, context uint8, coeff vp8QuantizedBlock, start int) uint8 {
	return encodeVP8BlockFromWithProbs(enc, nil, plane, context, coeff, start)
}

func encodeVP8BlockFromWithProbs(enc *vp8BoolEncoder, probs *vp8TokenProbs, plane int, context uint8, coeff vp8QuantizedBlock, start int) uint8 {
	if context > 2 {
		context = 2
	}
	n := start
	prob := vp8TokenProbPtr(probs, plane, int(vp8Bands[n]), context)
	if coeff == vp8ZeroCoeff {
		enc.writeBit(prob[0], false)
		return 0
	}
	last := vp8LastNonZeroCoeff(coeff, n)
	if last < n {
		enc.writeBit(prob[0], false)
		return 0
	}
	enc.writeBit(prob[0], true)

	for n != 16 {
		n++
		z := int(vp8Zigzag[n-1])
		v := coeff[z]
		if v == 0 {
			enc.writeBit(prob[1], false)
			prob = vp8TokenProbPtr(probs, plane, int(vp8Bands[n]), 0)
			continue
		}

		enc.writeBit(prob[1], true)
		absCoeff := int(v)
		if absCoeff < 0 {
			absCoeff = -absCoeff
		}
		encodeVP8CoeffValue(enc, prob, absCoeff)
		enc.writeBitEqualProb(v < 0)
		prob = vp8TokenProbPtr(probs, plane, int(vp8Bands[n]), coeffContext(absCoeff))
		if n == 16 {
			return 1
		}
		if n > last {
			enc.writeBit(prob[0], false)
			return 1
		}
		enc.writeBit(prob[0], true)
	}

	return 1
}

func vp8HasNonZeroCoeff(coeff vp8QuantizedBlock, after int) bool {
	return vp8LastNonZeroCoeffPtr(&coeff, after) >= after
}

func vp8LastNonZeroCoeff(coeff vp8QuantizedBlock, start int) int {
	return vp8LastNonZeroCoeffPtr(&coeff, start)
}

func vp8LastNonZeroCoeffPtr(coeff *vp8QuantizedBlock, start int) int {
	for i := 15; i >= start; i-- {
		if coeff[vp8Zigzag[i]] != 0 {
			return i
		}
	}
	return -1
}

func vp8RecordBlockTokens(stats *vp8TokenStats, plane int, context uint8, coeff vp8QuantizedBlock) uint8 {
	return vp8RecordBlockTokensFrom(stats, plane, context, coeff, 0)
}

func vp8RecordBlockTokensFrom(stats *vp8TokenStats, plane int, context uint8, coeff vp8QuantizedBlock, start int) uint8 {
	if context > 2 {
		context = 2
	}
	n := start
	band := int(vp8Bands[n])
	if coeff == vp8ZeroCoeff {
		stats.record(plane, band, context, 0, false)
		return 0
	}
	last := vp8LastNonZeroCoeff(coeff, n)
	hasNZ := last >= n
	stats.record(plane, band, context, 0, hasNZ)
	if !hasNZ {
		return 0
	}

	for n != 16 {
		n++
		z := int(vp8Zigzag[n-1])
		v := coeff[z]
		if v == 0 {
			stats.record(plane, band, context, 1, false)
			band = int(vp8Bands[n])
			context = 0
			continue
		}

		stats.record(plane, band, context, 1, true)
		absCoeff := int(v)
		if absCoeff < 0 {
			absCoeff = -absCoeff
		}
		recordVP8CoeffValue(stats, plane, band, context, absCoeff)
		context = coeffContext(absCoeff)
		if n == 16 {
			return 1
		}

		band = int(vp8Bands[n])
		hasNZ = n <= last
		stats.record(plane, band, context, 0, hasNZ)
		if !hasNZ {
			return 1
		}
	}

	return 1
}

func recordVP8CoeffValue(stats *vp8TokenStats, plane int, band int, context uint8, v int) {
	if v <= 1 {
		stats.record(plane, band, context, 2, false)
		return
	}

	stats.record(plane, band, context, 2, true)
	if v == 2 {
		stats.record(plane, band, context, 3, false)
		stats.record(plane, band, context, 4, false)
		return
	}
	if v <= 4 {
		stats.record(plane, band, context, 3, false)
		stats.record(plane, band, context, 4, true)
		stats.record(plane, band, context, 5, v == 4)
		return
	}

	stats.record(plane, band, context, 3, true)
	if v <= 6 {
		stats.record(plane, band, context, 6, false)
		stats.record(plane, band, context, 7, false)
		return
	}
	if v <= 10 {
		stats.record(plane, band, context, 6, false)
		stats.record(plane, band, context, 7, true)
		return
	}

	stats.record(plane, band, context, 6, true)
	cat, _ := vp8CoeffCategory(v)
	stats.record(plane, band, context, 8, cat&2 != 0)
	stats.record(plane, band, context, 9+cat/2, cat&1 != 0)
}

func (stats *vp8TokenStats) record(plane int, band int, context uint8, node int, bit bool) {
	if plane < 0 || plane >= len(stats) || band < 0 || band >= len(stats[plane]) || context > 2 || node < 0 || node >= len(stats[plane][band][context]) {
		return
	}
	counts := &stats[plane][band][context][node]
	if bit {
		counts.one++
		return
	}
	counts.zero++
}

func encodeVP8CoeffValue(enc *vp8BoolEncoder, prob *[11]uint8, v int) {
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

func vp8CoeffValueBitCost(prob [11]uint8, v int) int64 {
	return vp8CoeffValueBitCostFrom(&prob, v)
}

func vp8CoeffValueBitCostFrom(prob *[11]uint8, v int) int64 {
	if v <= 1 {
		return vp8BitCost(prob[2], false)
	}

	cost := vp8BitCost(prob[2], true)
	if v == 2 {
		return cost + vp8BitCost(prob[3], false) + vp8BitCost(prob[4], false)
	}
	if v <= 4 {
		return cost + vp8BitCost(prob[3], false) + vp8BitCost(prob[4], true) + vp8BitCost(prob[5], v == 4)
	}

	cost += vp8BitCost(prob[3], true)
	if v <= 6 {
		return cost + vp8BitCost(prob[6], false) + vp8BitCost(prob[7], false) + vp8BitCost(159, v == 6)
	}
	if v <= 10 {
		d := v - 7
		return cost + vp8BitCost(prob[6], false) + vp8BitCost(prob[7], true) +
			vp8BitCost(165, d&2 != 0) + vp8BitCost(145, d&1 != 0)
	}

	cost += vp8BitCost(prob[6], true)
	cat, offset := vp8CoeffCategory(v)
	cost += vp8BitCost(prob[8], cat&2 != 0)
	cost += vp8BitCost(prob[9+cat/2], cat&1 != 0)
	return cost + vp8CategoryBitCost(vp8Cat3456[cat], v-offset)
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

func vp8CategoryBitCost(probs []uint8, value int) int64 {
	var cost int64
	for i, prob := range probs {
		shift := len(probs) - 1 - i
		cost += vp8BitCost(prob, value&(1<<shift) != 0)
	}
	return cost
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
	return vp8TokenProbFrom(nil, plane, band, context)
}

func vp8TokenProbFrom(probs *vp8TokenProbs, plane int, band int, context uint8) [11]uint8 {
	return *vp8TokenProbPtr(probs, plane, band, context)
}

func vp8TokenProbPtr(probs *vp8TokenProbs, plane int, band int, context uint8) *[11]uint8 {
	if context > 2 {
		context = 2
	}
	if band < 0 {
		band = 0
	}
	if band >= len(vp8UVTokenProb) {
		band = len(vp8UVTokenProb) - 1
	}
	if probs != nil {
		return &probs[plane][band][context]
	}
	return &vp8DefaultTokenProbs[plane][band][context]
}

func vp8StaticTokenProb(plane int, band int, context uint8) [11]uint8 {
	switch plane {
	case vp8PlaneY1WithY2:
		return vp8Y1WithY2TokenProb[band][context]
	case vp8PlaneY2:
		return vp8Y2TokenProb[band][context]
	case vp8PlaneUV:
		return vp8UVTokenProb[band][context]
	default:
		return vp8Y1SansY2TokenProb[band][context]
	}
}

func chooseVP8TokenProbs(stats *vp8TokenStats) vp8TokenProbs {
	return chooseVP8TokenProbsConfig(stats, true)
}

func chooseVP8TokenProbsConfig(stats *vp8TokenStats, update bool) vp8TokenProbs {
	probs := vp8DefaultTokenProbs
	if !update {
		return probs
	}
	for plane := range probs {
		for band := range probs[plane] {
			for context := range probs[plane][band] {
				for node := range probs[plane][band][context] {
					counts := stats[plane][band][context][node]
					total := counts.zero + counts.one
					if total == 0 {
						continue
					}
					current := probs[plane][band][context][node]
					next := estimateVP8TokenProb(counts)
					if next == current {
						continue
					}
					updateProb := vp8TokenProbUpdateProb[plane][band][context][node]
					keepCost := vp8BitCost(updateProb, false) + vp8BranchCountsCost(counts, current)
					updateCost := vp8BitCost(updateProb, true) + vp8TokenProbLiteralCost(next) + vp8BranchCountsCost(counts, next)
					if updateCost < keepCost {
						probs[plane][band][context][node] = next
					}
				}
			}
		}
	}
	return probs
}

func estimateVP8TokenProb(counts vp8TokenBranchCounts) uint8 {
	total := uint64(counts.zero) + uint64(counts.one)
	if total == 0 {
		return 128
	}
	prob := int((uint64(counts.zero)*256 + total/2) / total)
	return uint8(clipInt(prob, 1, 255))
}

func vp8BranchCountsCost(counts vp8TokenBranchCounts, prob uint8) int64 {
	return int64(counts.zero)*vp8BitCost(prob, false) + int64(counts.one)*vp8BitCost(prob, true)
}

func vp8TokenProbLiteralCost(prob uint8) int64 {
	var cost int64
	for i := 7; i >= 0; i-- {
		cost += vp8BitCost(128, prob&(1<<i) != 0)
	}
	return cost
}
