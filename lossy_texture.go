package webp

var vp8TextureWeights = [16]int64{
	38, 32, 20, 9,
	32, 28, 17, 7,
	20, 17, 10, 4,
	9, 7, 4, 2,
}

func (rd vp8RDConfig) lumaDistortion(target *[16]uint8, reconstructed [16]uint8) int64 {
	if rd.textureLambda <= 0 {
		return scoreLuma4FromTarget(target, reconstructed)
	}
	return rd.lumaDistortionWithTargetTexture(target, reconstructed, vp8WeightedHadamard(target))
}

func (rd vp8RDConfig) lumaDistortionWithTargetTexture(target *[16]uint8, reconstructed [16]uint8, targetTexture int64) int64 {
	distortion := scoreLuma4FromTarget(target, reconstructed)
	if rd.textureLambda <= 0 {
		return distortion
	}
	texture := absInt64(targetTexture-vp8WeightedHadamard(&reconstructed)) >> 5
	return distortion + (rd.textureLambda*texture+128)/256
}

func vp8TextureDistortion(a *[16]uint8, b *[16]uint8) int64 {
	return absInt64(vp8WeightedHadamard(a)-vp8WeightedHadamard(b)) >> 5
}

func vp8WeightedHadamard(block *[16]uint8) int64 {
	var horizontal [16]int
	for y := 0; y < 4; y++ {
		offset := y * 4
		a0 := int(block[offset]) + int(block[offset+2])
		a1 := int(block[offset+1]) + int(block[offset+3])
		a2 := int(block[offset+1]) - int(block[offset+3])
		a3 := int(block[offset]) - int(block[offset+2])
		horizontal[offset] = a0 + a1
		horizontal[offset+1] = a3 + a2
		horizontal[offset+2] = a3 - a2
		horizontal[offset+3] = a0 - a1
	}
	var sum int64
	for x := 0; x < 4; x++ {
		a0 := horizontal[x] + horizontal[8+x]
		a1 := horizontal[4+x] + horizontal[12+x]
		a2 := horizontal[4+x] - horizontal[12+x]
		a3 := horizontal[x] - horizontal[8+x]
		transformed := [4]int{a0 + a1, a3 + a2, a3 - a2, a0 - a1}
		for y, coefficient := range transformed {
			sum += vp8TextureWeights[y*4+x] * int64(absInt(coefficient))
		}
	}
	return sum
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
