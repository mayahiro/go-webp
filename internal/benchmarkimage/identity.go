// Package benchmarkimage provides normalized image identities for benchmark data
package benchmarkimage

import (
	"crypto/sha256"
	"encoding/binary"
	"image"
	"image/color"
)

// PixelIdentity identifies normalized, row-major NRGBA pixels and alpha presence
type PixelIdentity struct {
	SHA256   [sha256.Size]byte
	HasAlpha bool
}

// IdentifyPixels returns a representation-independent identity for an image
func IdentifyPixels(img image.Image) PixelIdentity {
	bounds := img.Bounds()
	hash := sha256.New()
	var dimensions [8]byte
	binary.LittleEndian.PutUint32(dimensions[0:4], uint32(bounds.Dx()))
	binary.LittleEndian.PutUint32(dimensions[4:8], uint32(bounds.Dy()))
	_, _ = hash.Write(dimensions[:])

	identity := PixelIdentity{}
	var rgba [4]byte
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if pixel.A != 0xff {
				identity.HasAlpha = true
			}
			rgba[0] = pixel.R
			rgba[1] = pixel.G
			rgba[2] = pixel.B
			rgba[3] = pixel.A
			_, _ = hash.Write(rgba[:])
		}
	}
	copy(identity.SHA256[:], hash.Sum(nil))
	return identity
}
