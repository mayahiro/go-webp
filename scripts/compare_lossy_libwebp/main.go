package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	webp "github.com/mayahiro/go-webp"
	"github.com/mayahiro/go-webp/internal/benchmarkfixture"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	runs := flag.Int("runs", 3, "number of encode runs per fixture, quality, and encoder")
	qualitiesFlag := flag.String("qualities", "1,5,10,25,40,50,60,70,75,80,85,90,95,100", "comma-separated quality values from 1 to 100")
	method := flag.Int("method", 4, "cwebp method from 0 to 6")
	goModeFlag := flag.String("go-mode", "default", "go-webp mode: default, fast, balanced, best, low-memory, lossy-quality, or auto")
	sharpYUV := flag.Bool("sharp-yuv", false, "enable cwebp -sharp_yuv")
	mt := flag.Bool("mt", false, "enable cwebp -mt")
	outDir := flag.String("out", "", "directory for generated PNG and WebP files")
	keep := flag.Bool("keep", false, "keep generated files when out is empty")
	jsonPath := flag.String("json", "-", "JSON report path, or - for stdout")
	flag.Parse()
	if *runs <= 0 {
		return errors.New("runs must be positive")
	}
	if *method < 0 || *method > 6 {
		return errors.New("method must be between 0 and 6")
	}
	qualities, err := parseQualities(*qualitiesFlag)
	if err != nil {
		return err
	}
	goMode, goModeName, err := parseGoMode(*goModeFlag)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("cwebp"); err != nil {
		return fmt.Errorf("cwebp not found in PATH: %w", err)
	}
	if _, err := exec.LookPath("dwebp"); err != nil {
		return fmt.Errorf("dwebp not found in PATH: %w", err)
	}
	version, err := commandVersion("cwebp", "-version")
	if err != nil {
		return err
	}
	dir, cleanup, err := comparisonDirectory(*outDir, *keep)
	if err != nil {
		return err
	}
	defer cleanup()

	report := comparisonReport{
		SchemaVersion: 1,
		Configuration: reportConfiguration{
			Runs:               *runs,
			Qualities:          qualities,
			CWebPVersion:       version,
			CWebPMethod:        *method,
			CWebPSharpYUV:      *sharpYUV,
			CWebPMT:            *mt,
			GoVersion:          runtime.Version(),
			GOOS:               runtime.GOOS,
			GOARCH:             runtime.GOARCH,
			GoMode:             goModeName,
			GoTimingScope:      "in-process webp.Encode call",
			CWebPTimingScope:   "process startup, PNG decode, encode, and output write",
			QualityMatchMetric: "rgb_psnr_db",
			MatchStrategy:      "nearest sampled cwebp point for each go-webp point",
			ExactPSNRValue:     "null",
		},
	}
	cfg := cwebpConfig{method: *method, sharpYUV: *sharpYUV, mt: *mt}
	for _, fixture := range benchmarkfixture.Standard() {
		fmt.Fprintf(os.Stderr, "fixture=%s\n", fixture.Name)
		pngPath := filepath.Join(dir, fixture.Name+".png")
		if err := writePNG(pngPath, fixture.Image); err != nil {
			return fmt.Errorf("%s: write PNG: %w", fixture.Name, err)
		}
		fixtureResult := fixtureReport{
			Name:   fixture.Name,
			Width:  fixture.Image.Bounds().Dx(),
			Height: fixture.Image.Bounds().Dy(),
			GoWebP: make([]sample, 0, len(qualities)),
			CWebP:  make([]sample, 0, len(qualities)),
		}
		for _, quality := range qualities {
			goSample, err := runGoWebP(dir, fixture.Name, fixture.Image, quality, *runs, goMode)
			if err != nil {
				return err
			}
			cwebpSample, err := runCWebP(dir, fixture.Name, fixture.Image, pngPath, quality, *runs, cfg)
			if err != nil {
				return err
			}
			fixtureResult.GoWebP = append(fixtureResult.GoWebP, goSample)
			fixtureResult.CWebP = append(fixtureResult.CWebP, cwebpSample)
		}
		fixtureResult.MatchedSize, fixtureResult.MatchedQuality = buildMatches(fixtureResult.GoWebP, fixtureResult.CWebP)
		report.Fixtures = append(report.Fixtures, fixtureResult)
	}
	return writeReport(*jsonPath, report)
}

func parseGoMode(value string) (webp.Mode, string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "default":
		return webp.ModeDefault, value, nil
	case "fast":
		return webp.ModeFast, value, nil
	case "balanced":
		return webp.ModeBalanced, value, nil
	case "best", "best-compression":
		return webp.ModeBestCompression, "best", nil
	case "low-memory":
		return webp.ModeLowMemory, value, nil
	case "lossy-quality":
		return webp.ModeLossyQuality, value, nil
	case "auto":
		return webp.ModeAuto, value, nil
	default:
		return webp.ModeDefault, "", fmt.Errorf("invalid go mode %q", value)
	}
}

func parseQualities(value string) ([]int, error) {
	seen := make(map[int]struct{})
	var qualities []int
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, errors.New("qualities contains an empty value")
		}
		quality, err := strconv.Atoi(part)
		if err != nil || quality < 1 || quality > 100 {
			return nil, fmt.Errorf("invalid quality %q: want an integer from 1 to 100", part)
		}
		if _, ok := seen[quality]; ok {
			continue
		}
		seen[quality] = struct{}{}
		qualities = append(qualities, quality)
	}
	if len(qualities) == 0 {
		return nil, errors.New("qualities must not be empty")
	}
	slices.Sort(qualities)
	return qualities, nil
}

func comparisonDirectory(path string, keep bool) (string, func(), error) {
	if path != "" {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return "", nil, err
		}
		return path, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "go-webp-lossy-compare-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {}
	if !keep {
		cleanup = func() { _ = os.RemoveAll(dir) }
	} else {
		fmt.Fprintf(os.Stderr, "workdir=%s\n", dir)
	}
	return dir, cleanup, nil
}

func commandVersion(name string, args ...string) (string, error) {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s version: %w: %s", name, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

func writeReport(path string, report comparisonReport) error {
	var writer io.Writer = os.Stdout
	var file *os.File
	if path != "-" {
		var err error
		file, err = os.Create(path)
		if err != nil {
			return err
		}
		defer file.Close()
		writer = file
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
