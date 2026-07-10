package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	webp "github.com/mayahiro/go-webp"
)

type cwebpConfig struct {
	method   int
	sharpYUV bool
	mt       bool
}

func runGoWebP(dir string, fixtureName string, source image.Image, quality int, runs int, mode webp.Mode) (sample, error) {
	var encoded []byte
	var elapsed time.Duration
	for range runs {
		var buf bytes.Buffer
		start := time.Now()
		if err := webp.Encode(&buf, source, &webp.Options{Compression: webp.CompressionLossy, Quality: quality, Mode: mode}); err != nil {
			return sample{}, fmt.Errorf("%s/go-webp/q%d: encode: %w", fixtureName, quality, err)
		}
		elapsed += time.Since(start)
		encoded = append(encoded[:0], buf.Bytes()...)
	}
	webpPath := filepath.Join(dir, fixtureName+".go-webp.q"+strconv.Itoa(quality)+".webp")
	if err := os.WriteFile(webpPath, encoded, 0o600); err != nil {
		return sample{}, fmt.Errorf("%s/go-webp/q%d: write: %w", fixtureName, quality, err)
	}
	metrics, err := decodeAndMeasure(dir, fixtureName+".go-webp.q"+strconv.Itoa(quality), webpPath, source)
	if err != nil {
		return sample{}, err
	}
	return sample{
		Quality:         quality,
		EncodedBytes:    len(encoded),
		AverageEncodeNS: (elapsed / time.Duration(runs)).Nanoseconds(),
		Distortion:      metrics,
	}, nil
}

func runCWebP(dir string, fixtureName string, source image.Image, pngPath string, quality int, runs int, cfg cwebpConfig) (sample, error) {
	webpPath := filepath.Join(dir, fixtureName+".cwebp.q"+strconv.Itoa(quality)+".webp")
	var elapsed time.Duration
	for range runs {
		args := []string{
			"-quiet",
			"-q", strconv.Itoa(quality),
			"-m", strconv.Itoa(cfg.method),
		}
		if cfg.sharpYUV {
			args = append(args, "-sharp_yuv")
		}
		if cfg.mt {
			args = append(args, "-mt")
		}
		args = append(args, pngPath, "-o", webpPath)
		cmd := exec.Command("cwebp", args...)
		start := time.Now()
		if output, err := cmd.CombinedOutput(); err != nil {
			return sample{}, fmt.Errorf("%s/cwebp/q%d: %w: %s", fixtureName, quality, err, output)
		}
		elapsed += time.Since(start)
	}
	info, err := os.Stat(webpPath)
	if err != nil {
		return sample{}, fmt.Errorf("%s/cwebp/q%d: stat: %w", fixtureName, quality, err)
	}
	metrics, err := decodeAndMeasure(dir, fixtureName+".cwebp.q"+strconv.Itoa(quality), webpPath, source)
	if err != nil {
		return sample{}, err
	}
	return sample{
		Quality:         quality,
		EncodedBytes:    int(info.Size()),
		AverageEncodeNS: (elapsed / time.Duration(runs)).Nanoseconds(),
		Distortion:      metrics,
	}, nil
}

func decodeAndMeasure(dir string, name string, webpPath string, source image.Image) (distortionMetrics, error) {
	pngPath := filepath.Join(dir, name+".decoded.png")
	cmd := exec.Command("dwebp", "-quiet", webpPath, "-o", pngPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return distortionMetrics{}, fmt.Errorf("%s: dwebp: %w: %s", name, err, output)
	}
	decoded, err := readPNG(pngPath)
	if err != nil {
		return distortionMetrics{}, fmt.Errorf("%s: read decoded PNG: %w", name, err)
	}
	metrics, err := measureDistortion(source, decoded)
	if err != nil {
		return distortionMetrics{}, fmt.Errorf("%s: distortion: %w", name, err)
	}
	return metrics, nil
}

func writePNG(path string, img image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}

func readPNG(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return png.Decode(file)
}

func measureDistortion(source image.Image, decoded image.Image) (distortionMetrics, error) {
	sourceBounds := source.Bounds()
	decodedBounds := decoded.Bounds()
	if sourceBounds.Dx() != decodedBounds.Dx() || sourceBounds.Dy() != decodedBounds.Dy() {
		return distortionMetrics{}, fmt.Errorf("dimensions = %dx%d, want %dx%d", decodedBounds.Dx(), decodedBounds.Dy(), sourceBounds.Dx(), sourceBounds.Dy())
	}
	var rgbAbs, alphaAbs uint64
	var rgbSquared, ySquared, uvSquared float64
	rgbExact, alphaExact := true, true
	pixels := sourceBounds.Dx() * sourceBounds.Dy()
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
		}
	}
	rgbSamples := 3 * pixels
	uvSamples := 2 * pixels
	rgbMSE := rgbSquared / float64(rgbSamples)
	return distortionMetrics{
		RGBMAE:     float64(rgbAbs) / float64(rgbSamples),
		RGBMSE:     rgbMSE,
		RGBPSNRDB:  psnrDB(rgbMSE),
		YPSNRDB:    psnrDB(ySquared / float64(pixels)),
		UVPSNRDB:   psnrDB(uvSquared / float64(uvSamples)),
		AlphaMAE:   float64(alphaAbs) / float64(pixels),
		RGBExact:   rgbExact,
		AlphaExact: alphaExact,
	}, nil
}

func psnrDB(mse float64) *float64 {
	if mse == 0 {
		return nil
	}
	value := 10 * math.Log10(255*255/mse)
	return &value
}
