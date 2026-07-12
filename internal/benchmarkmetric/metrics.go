// Package benchmarkmetric measures decoded image distortion for development benchmarks
package benchmarkmetric

import (
	"fmt"
	"image"
	"image/color"
	"math"
)

// Metrics records distortion between source and decoded images
type Metrics struct {
	RGBMAE                     float64  `json:"rgb_mae"`
	RGBMSE                     float64  `json:"rgb_mse"`
	RGBPSNRDB                  *float64 `json:"rgb_psnr_db"`
	YMSE                       float64  `json:"y_mse"`
	YPSNRDB                    *float64 `json:"y_psnr_db"`
	UVMSE                      float64  `json:"uv_mse"`
	UVPSNRDB                   *float64 `json:"uv_psnr_db"`
	YSSIM                      float64  `json:"y_ssim"`
	YSSIMDB                    *float64 `json:"y_ssim_db"`
	AlphaMAE                   float64  `json:"alpha_mae"`
	CompositeOverBlackMSE      float64  `json:"composite_over_black_mse"`
	CompositeOverBlackPSNRDB   *float64 `json:"composite_over_black_psnr_db"`
	CompositeOverWhiteMSE      float64  `json:"composite_over_white_mse"`
	CompositeOverWhitePSNRDB   *float64 `json:"composite_over_white_psnr_db"`
	CompositeOverCheckerMSE    float64  `json:"composite_over_checker_mse"`
	CompositeOverCheckerPSNRDB *float64 `json:"composite_over_checker_psnr_db"`
	RGBExact                   bool     `json:"rgb_exact"`
	AlphaExact                 bool     `json:"alpha_exact"`
}

// Measure calculates RGB, YUV, alpha, composite, and weighted Y SSIM distortion
func Measure(source image.Image, decoded image.Image) (Metrics, error) {
	sourceBounds := source.Bounds()
	decodedBounds := decoded.Bounds()
	if sourceBounds.Dx() != decodedBounds.Dx() || sourceBounds.Dy() != decodedBounds.Dy() {
		return Metrics{}, fmt.Errorf("dimensions = %dx%d, want %dx%d", decodedBounds.Dx(), decodedBounds.Dy(), sourceBounds.Dx(), sourceBounds.Dy())
	}
	var rgbAbs, alphaAbs uint64
	var rgbSquared, ySquared, uvSquared float64
	var compositeBlackSquared, compositeWhiteSquared, compositeCheckerSquared float64
	rgbExact, alphaExact := true, true
	pixels := sourceBounds.Dx() * sourceBounds.Dy()
	sourceYPlane := make([]byte, pixels)
	decodedYPlane := make([]byte, pixels)
	for y := 0; y < sourceBounds.Dy(); y++ {
		for x := 0; x < sourceBounds.Dx(); x++ {
			sourcePixel := color.NRGBAModel.Convert(source.At(sourceBounds.Min.X+x, sourceBounds.Min.Y+y)).(color.NRGBA)
			decodedPixel := color.NRGBAModel.Convert(decoded.At(decodedBounds.Min.X+x, decodedBounds.Min.Y+y)).(color.NRGBA)
			channels := [3][2]uint8{{sourcePixel.R, decodedPixel.R}, {sourcePixel.G, decodedPixel.G}, {sourcePixel.B, decodedPixel.B}}
			for _, channel := range channels {
				diff := int(channel[1]) - int(channel[0])
				if diff != 0 {
					rgbExact = false
				}
				rgbAbs += uint64(absInt(diff))
				rgbSquared += float64(diff * diff)
			}
			sourceY, sourceCb, sourceCr := color.RGBToYCbCr(sourcePixel.R, sourcePixel.G, sourcePixel.B)
			decodedY, decodedCb, decodedCr := color.RGBToYCbCr(decodedPixel.R, decodedPixel.G, decodedPixel.B)
			pixelIndex := y*sourceBounds.Dx() + x
			sourceYPlane[pixelIndex] = sourceY
			decodedYPlane[pixelIndex] = decodedY
			yDiff := int(decodedY) - int(sourceY)
			cbDiff := int(decodedCb) - int(sourceCb)
			crDiff := int(decodedCr) - int(sourceCr)
			ySquared += float64(yDiff * yDiff)
			uvSquared += float64(cbDiff*cbDiff + crDiff*crDiff)
			alphaDiff := int(decodedPixel.A) - int(sourcePixel.A)
			if alphaDiff != 0 {
				alphaExact = false
			}
			alphaAbs += uint64(absInt(alphaDiff))

			checker := uint8(0)
			if ((x>>3)+(y>>3))&1 != 0 {
				checker = 255
			}
			compositeBlackSquared += compositeSquaredError(sourcePixel, decodedPixel, 0)
			compositeWhiteSquared += compositeSquaredError(sourcePixel, decodedPixel, 255)
			compositeCheckerSquared += compositeSquaredError(sourcePixel, decodedPixel, checker)
		}
	}
	ySSIM, err := measurePlaneSSIM(sourceYPlane, decodedYPlane, sourceBounds.Dx(), sourceBounds.Dy())
	if err != nil {
		return Metrics{}, err
	}
	rgbSamples := 3 * pixels
	uvSamples := 2 * pixels
	rgbMSE := rgbSquared / float64(rgbSamples)
	yMSE := ySquared / float64(pixels)
	uvMSE := uvSquared / float64(uvSamples)
	compositeBlackMSE := compositeBlackSquared / float64(rgbSamples)
	compositeWhiteMSE := compositeWhiteSquared / float64(rgbSamples)
	compositeCheckerMSE := compositeCheckerSquared / float64(rgbSamples)
	return Metrics{
		RGBMAE:                     float64(rgbAbs) / float64(rgbSamples),
		RGBMSE:                     rgbMSE,
		RGBPSNRDB:                  psnrDB(rgbMSE),
		YMSE:                       yMSE,
		YPSNRDB:                    psnrDB(yMSE),
		UVMSE:                      uvMSE,
		UVPSNRDB:                   psnrDB(uvMSE),
		YSSIM:                      ySSIM,
		YSSIMDB:                    ssimDB(ySSIM),
		AlphaMAE:                   float64(alphaAbs) / float64(pixels),
		CompositeOverBlackMSE:      compositeBlackMSE,
		CompositeOverBlackPSNRDB:   psnrDB(compositeBlackMSE),
		CompositeOverWhiteMSE:      compositeWhiteMSE,
		CompositeOverWhitePSNRDB:   psnrDB(compositeWhiteMSE),
		CompositeOverCheckerMSE:    compositeCheckerMSE,
		CompositeOverCheckerPSNRDB: psnrDB(compositeCheckerMSE),
		RGBExact:                   rgbExact,
		AlphaExact:                 alphaExact,
	}, nil
}

func compositeSquaredError(source color.NRGBA, decoded color.NRGBA, background uint8) float64 {
	sourceChannels := [3]uint8{source.R, source.G, source.B}
	decodedChannels := [3]uint8{decoded.R, decoded.G, decoded.B}
	var squared float64
	for i := range sourceChannels {
		sourceValue := compositeChannel(sourceChannels[i], source.A, background)
		decodedValue := compositeChannel(decodedChannels[i], decoded.A, background)
		diff := decodedValue - sourceValue
		squared += float64(diff * diff)
	}
	return squared
}

func compositeChannel(value uint8, alpha uint8, background uint8) int {
	return (int(value)*int(alpha) + int(background)*(255-int(alpha)) + 127) / 255
}

func psnrDB(mse float64) *float64 {
	if mse == 0 {
		return nil
	}
	value := 10 * math.Log10(255*255/mse)
	return &value
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
