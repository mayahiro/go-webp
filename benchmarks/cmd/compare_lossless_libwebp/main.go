package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	webp "github.com/mayahiro/go-webp"
	"github.com/mayahiro/go-webp/benchmarks/internal/benchmarkfixture"
)

type fixture struct {
	name string
	img  image.Image
}

type comparisonConfig struct {
	name      string
	goOptions webp.Options
	cwebpArgs []string
	exact     bool
}

type distortionMetrics struct {
	rgbMAE     float64
	rgbMaxAbs  int
	alphaExact bool
	exact      bool
}

type result struct {
	fixture    string
	encoder    string
	runs       int
	size       int64
	avg        time.Duration
	distortion distortionMetrics
}

func main() {
	runs := flag.Int("runs", 3, "number of encode runs per fixture and encoder")
	mode := flag.String("mode", "default", "go-webp profile: default, fast, balanced, best, low-memory, auto, or near-lossless")
	quality := flag.Int("quality", 75, "near-lossless quality from 1 to 100")
	method := flag.Int("method", 4, "cwebp method from 0 to 6")
	outDir := flag.String("out", "", "directory for generated PNG and WebP files")
	keep := flag.Bool("keep", false, "keep generated files when out is empty")
	flag.Parse()
	if *runs <= 0 {
		fatal(errors.New("runs must be positive"))
	}
	if *quality < 1 || *quality > 100 {
		fatal(errors.New("quality must be between 1 and 100"))
	}
	if *method < 0 || *method > 6 {
		fatal(errors.New("method must be between 0 and 6"))
	}
	cfg, err := makeComparisonConfig(*mode, *quality, *method)
	if err != nil {
		fatal(err)
	}
	if _, err := exec.LookPath("cwebp"); err != nil {
		fatal(fmt.Errorf("cwebp not found in PATH: %w", err))
	}
	if _, err := exec.LookPath("dwebp"); err != nil {
		fatal(fmt.Errorf("dwebp not found in PATH: %w", err))
	}

	dir := *outDir
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "go-webp-lossless-compare-*")
		if err != nil {
			fatal(err)
		}
		if !*keep {
			defer os.RemoveAll(dir)
		}
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		fatal(err)
	}

	version, err := commandVersion("cwebp", "-version")
	if err != nil {
		fatal(err)
	}
	fmt.Printf("workdir=%s\n", dir)
	fmt.Printf("mode=%s method=%d quality=%d cwebp=%s\n", cfg.name, *method, *quality, version)
	fmt.Printf("%-14s %-10s %4s %12s %10s %10s %8s %11s\n", "fixture", "encoder", "runs", "encoded_B", "avg_ms", "rgb_mae", "rgb_max", "alpha_exact")
	for _, f := range fixtures() {
		pngPath := filepath.Join(dir, f.name+".png")
		if err := writePNG(pngPath, f.img); err != nil {
			fatal(fmt.Errorf("%s: write png: %w", f.name, err))
		}
		goResult, err := runGoWebP(dir, f, *runs, cfg)
		if err != nil {
			fatal(err)
		}
		libwebpResult, err := runLibWebP(dir, f, pngPath, *runs, cfg)
		if err != nil {
			fatal(err)
		}
		for _, r := range []result{goResult, libwebpResult} {
			fmt.Printf(
				"%-14s %-10s %4d %12d %10.3f %10.4f %8d %11t\n",
				r.fixture,
				r.encoder,
				r.runs,
				r.size,
				float64(r.avg.Microseconds())/1000,
				r.distortion.rgbMAE,
				r.distortion.rgbMaxAbs,
				r.distortion.alphaExact,
			)
		}
	}
}

func makeComparisonConfig(mode string, quality int, method int) (comparisonConfig, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	cfg := comparisonConfig{
		name:      mode,
		goOptions: webp.Options{Compression: webp.CompressionLossless},
		cwebpArgs: []string{"-lossless", "-m", strconv.Itoa(method)},
		exact:     true,
	}
	switch mode {
	case "default":
		cfg.goOptions.Mode = webp.ModeDefault
	case "fast":
		cfg.goOptions.Mode = webp.ModeFast
	case "balanced":
		cfg.goOptions.Mode = webp.ModeBalanced
	case "best", "best-compression":
		cfg.name = "best"
		cfg.goOptions.Mode = webp.ModeBestCompression
	case "low-memory":
		cfg.goOptions.Mode = webp.ModeLowMemory
	case "auto":
		cfg.goOptions.Mode = webp.ModeAuto
	case "near-lossless":
		cfg.goOptions.Mode = webp.ModeNearLossless
		cfg.goOptions.Quality = quality
		cfg.cwebpArgs = append(cfg.cwebpArgs, "-near_lossless", strconv.Itoa(quality))
		cfg.exact = quality == 100
	default:
		return comparisonConfig{}, fmt.Errorf("unsupported mode %q", mode)
	}
	return cfg, nil
}

func runGoWebP(dir string, f fixture, runs int, cfg comparisonConfig) (result, error) {
	var total time.Duration
	var encoded []byte
	for i := 0; i < runs; i++ {
		var buf bytes.Buffer
		start := time.Now()
		if err := webp.Encode(&buf, f.img, &cfg.goOptions); err != nil {
			return result{}, fmt.Errorf("%s/go-webp: encode: %w", f.name, err)
		}
		total += time.Since(start)
		encoded = buf.Bytes()
	}
	webpPath := filepath.Join(dir, f.name+".go-webp.webp")
	if err := os.WriteFile(webpPath, encoded, 0o600); err != nil {
		return result{}, fmt.Errorf("%s/go-webp: write webp: %w", f.name, err)
	}
	metrics, err := decodeAndMeasure(dir, f, webpPath, "go-webp", cfg.exact)
	if err != nil {
		return result{}, err
	}
	return result{
		fixture:    f.name,
		encoder:    "go-webp",
		runs:       runs,
		size:       int64(len(encoded)),
		avg:        total / time.Duration(runs),
		distortion: metrics,
	}, nil
}

func runLibWebP(dir string, f fixture, pngPath string, runs int, cfg comparisonConfig) (result, error) {
	webpPath := filepath.Join(dir, f.name+".libwebp.webp")
	var total time.Duration
	for i := 0; i < runs; i++ {
		args := append([]string{"-quiet"}, cfg.cwebpArgs...)
		args = append(args, pngPath, "-o", webpPath)
		cmd := exec.Command("cwebp", args...)
		start := time.Now()
		if out, err := cmd.CombinedOutput(); err != nil {
			return result{}, fmt.Errorf("%s/libwebp: cwebp: %w: %s", f.name, err, string(out))
		}
		total += time.Since(start)
	}
	info, err := os.Stat(webpPath)
	if err != nil {
		return result{}, fmt.Errorf("%s/libwebp: stat webp: %w", f.name, err)
	}
	metrics, err := decodeAndMeasure(dir, f, webpPath, "libwebp", cfg.exact)
	if err != nil {
		return result{}, err
	}
	return result{
		fixture:    f.name,
		encoder:    "libwebp",
		runs:       runs,
		size:       info.Size(),
		avg:        total / time.Duration(runs),
		distortion: metrics,
	}, nil
}

func decodeAndMeasure(dir string, f fixture, webpPath string, suffix string, requireExact bool) (distortionMetrics, error) {
	pngPath := filepath.Join(dir, f.name+"."+suffix+".decoded.png")
	cmd := exec.Command("dwebp", "-quiet", webpPath, "-o", pngPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return distortionMetrics{}, fmt.Errorf("%s/%s: dwebp: %w: %s", f.name, suffix, err, string(out))
	}
	got, err := readPNG(pngPath)
	if err != nil {
		return distortionMetrics{}, fmt.Errorf("%s/%s: read decoded png: %w", f.name, suffix, err)
	}
	metrics, err := measureImage(got, f.img)
	if err != nil {
		return distortionMetrics{}, fmt.Errorf("%s/%s: decoded image: %w", f.name, suffix, err)
	}
	if requireExact && !metrics.exact {
		return distortionMetrics{}, fmt.Errorf("%s/%s: decoded image is not exact", f.name, suffix)
	}
	return metrics, nil
}

func measureImage(got image.Image, want image.Image) (distortionMetrics, error) {
	gotBounds := got.Bounds()
	wantBounds := want.Bounds()
	if gotBounds.Dx() != wantBounds.Dx() || gotBounds.Dy() != wantBounds.Dy() {
		return distortionMetrics{}, fmt.Errorf("dimensions = %dx%d, want %dx%d", gotBounds.Dx(), gotBounds.Dy(), wantBounds.Dx(), wantBounds.Dy())
	}
	metrics := distortionMetrics{alphaExact: true, exact: true}
	var totalAbs uint64
	for y := 0; y < wantBounds.Dy(); y++ {
		for x := 0; x < wantBounds.Dx(); x++ {
			gotPixel := color.NRGBAModel.Convert(got.At(gotBounds.Min.X+x, gotBounds.Min.Y+y)).(color.NRGBA)
			wantPixel := color.NRGBAModel.Convert(want.At(wantBounds.Min.X+x, wantBounds.Min.Y+y)).(color.NRGBA)
			for _, diff := range []int{
				absDiff(gotPixel.R, wantPixel.R),
				absDiff(gotPixel.G, wantPixel.G),
				absDiff(gotPixel.B, wantPixel.B),
			} {
				totalAbs += uint64(diff)
				if diff > metrics.rgbMaxAbs {
					metrics.rgbMaxAbs = diff
				}
			}
			if gotPixel.A != wantPixel.A {
				metrics.alphaExact = false
			}
			if gotPixel != wantPixel {
				metrics.exact = false
			}
		}
	}
	samples := wantBounds.Dx() * wantBounds.Dy() * 3
	if samples != 0 {
		metrics.rgbMAE = float64(totalAbs) / float64(samples)
	}
	return metrics, nil
}

func absDiff(a uint8, b uint8) int {
	if a >= b {
		return int(a - b)
	}
	return int(b - a)
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

func commandVersion(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %w: %s", cmd.Args, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

func fixtures() []fixture {
	shared := benchmarkfixture.Standard()
	result := make([]fixture, len(shared))
	for i, f := range shared {
		result[i] = fixture{name: f.Name, img: f.Image}
	}
	return result
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
