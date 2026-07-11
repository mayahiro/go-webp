package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	webp "github.com/mayahiro/go-webp"
	"github.com/mayahiro/go-webp/internal/benchmarkbitstream"
	"github.com/mayahiro/go-webp/internal/benchmarkmetric"
)

type cwebpConfig struct {
	method   int
	sharpYUV bool
	mt       bool
}

// runGoWebP times only the in-process Encode call for each sample
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
	layout, err := benchmarkbitstream.ParseLossy(encoded)
	if err != nil {
		return sample{}, fmt.Errorf("%s/go-webp/q%d: layout: %w", fixtureName, quality, err)
	}
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
		Layout:          layout,
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
	encoded, err := os.ReadFile(webpPath)
	if err != nil {
		return sample{}, fmt.Errorf("%s/cwebp/q%d: read: %w", fixtureName, quality, err)
	}
	layout, err := benchmarkbitstream.ParseLossy(encoded)
	if err != nil {
		return sample{}, fmt.Errorf("%s/cwebp/q%d: layout: %w", fixtureName, quality, err)
	}
	metrics, err := decodeAndMeasure(dir, fixtureName+".cwebp.q"+strconv.Itoa(quality), webpPath, source)
	if err != nil {
		return sample{}, err
	}
	return sample{
		Quality:         quality,
		EncodedBytes:    int(info.Size()),
		AverageEncodeNS: (elapsed / time.Duration(runs)).Nanoseconds(),
		Layout:          layout,
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
	return benchmarkmetric.Measure(source, decoded)
}
