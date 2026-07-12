package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	encode := func() ([]byte, time.Duration, error) {
		var buf bytes.Buffer
		start := time.Now()
		if err := webp.Encode(&buf, source, &webp.Options{Compression: webp.CompressionLossy, Quality: quality, Mode: mode}); err != nil {
			return nil, time.Since(start), err
		}
		elapsed := time.Since(start)
		return append([]byte(nil), buf.Bytes()...), elapsed, nil
	}

	warmupOutput, _, err := encode()
	if err != nil {
		return sample{}, fmt.Errorf("%s/go-webp/q%d: warm-up encode: %w", fixtureName, quality, err)
	}
	encoded := warmupOutput
	durations := make([]time.Duration, 0, runs)
	for run := range runs {
		candidate, elapsed, err := encode()
		durations = append(durations, elapsed)
		if err != nil {
			return sample{}, fmt.Errorf("%s/go-webp/q%d: encode run %d: %w", fixtureName, quality, run+1, err)
		}
		if !bytes.Equal(warmupOutput, candidate) {
			return sample{}, fmt.Errorf("%s/go-webp/q%d: encoded output changed after warm-up on run %d", fixtureName, quality, run+1)
		}
		encoded = candidate
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
		Quality:      quality,
		EncodedBytes: len(encoded),
		Timing:       summarizeTiming(durations),
		OutputSHA256: outputSHA256(encoded),
		Layout:       layout,
		Distortion:   metrics,
	}, nil
}

func runCWebP(dir string, fixtureName string, source image.Image, pngPath string, quality int, runs int, cfg cwebpConfig) (sample, error) {
	webpPath := filepath.Join(dir, fixtureName+".cwebp.q"+strconv.Itoa(quality)+".webp")
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
	runCommand := func() error {
		cmd := exec.Command("cwebp", args...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%w: %s", err, output)
		}
		return nil
	}
	if err := runCommand(); err != nil {
		return sample{}, fmt.Errorf("%s/cwebp/q%d: warm-up: %w", fixtureName, quality, err)
	}
	warmupOutput, err := os.ReadFile(webpPath)
	if err != nil {
		return sample{}, fmt.Errorf("%s/cwebp/q%d: read warm-up output: %w", fixtureName, quality, err)
	}
	encoded := warmupOutput
	durations := make([]time.Duration, 0, runs)
	for run := range runs {
		start := time.Now()
		err := runCommand()
		durations = append(durations, time.Since(start))
		if err != nil {
			return sample{}, fmt.Errorf("%s/cwebp/q%d: run %d: %w", fixtureName, quality, run+1, err)
		}
		candidate, err := os.ReadFile(webpPath)
		if err != nil {
			return sample{}, fmt.Errorf("%s/cwebp/q%d: read run %d output: %w", fixtureName, quality, run+1, err)
		}
		if !bytes.Equal(warmupOutput, candidate) {
			return sample{}, fmt.Errorf("%s/cwebp/q%d: encoded output changed after warm-up on run %d", fixtureName, quality, run+1)
		}
		encoded = candidate
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
		Quality:      quality,
		EncodedBytes: len(encoded),
		Timing:       summarizeTiming(durations),
		OutputSHA256: outputSHA256(encoded),
		Layout:       layout,
		Distortion:   metrics,
	}, nil
}

func summarizeTiming(durations []time.Duration) timingSummary {
	result := timingSummary{
		Runs:       len(durations),
		WarmupRuns: comparisonWarmupRuns,
	}
	if len(durations) == 0 {
		return result
	}
	sorted := slices.Clone(durations)
	slices.Sort(sorted)
	result.MinNS = sorted[0].Nanoseconds()
	result.MaxNS = sorted[len(sorted)-1].Nanoseconds()
	middle := len(sorted) / 2
	if len(sorted)&1 != 0 {
		result.MedianNS = sorted[middle].Nanoseconds()
	} else {
		lower := sorted[middle-1].Nanoseconds()
		upper := sorted[middle].Nanoseconds()
		result.MedianNS = lower + (upper-lower)/2
	}
	return result
}

func outputSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest)
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
