package webp

import "testing"

func TestVP8TextureDistortion(t *testing.T) {
	flat := [16]uint8{}
	shifted := [16]uint8{}
	for i := range shifted {
		shifted[i] = 1
	}
	if got := vp8TextureDistortion(&flat, &flat); got != 0 {
		t.Fatalf("identical texture distortion = %d, want 0", got)
	}
	if got := vp8TextureDistortion(&flat, &shifted); got != 19 {
		t.Fatalf("flat shift texture distortion = %d, want 19", got)
	}
}

func TestVP8LumaDistortionCanIncludeTexture(t *testing.T) {
	flat := [16]uint8{}
	shifted := [16]uint8{}
	for i := range shifted {
		shifted[i] = 1
	}
	withoutTexture := vp8RDConfig{}.lumaDistortion(&flat, shifted)
	withTexture := vp8RDConfig{textureLambda: 256}.lumaDistortion(&flat, shifted)
	if withoutTexture != 16 || withTexture != 35 {
		t.Fatalf("luma distortion without/with texture = %d/%d, want 16/35", withoutTexture, withTexture)
	}
}
