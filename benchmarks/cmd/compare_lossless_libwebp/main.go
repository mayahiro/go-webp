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
	"runtime"
	"strconv"
	"strings"
	"time"

	webp "github.com/mayahiro/go-webp"
	"github.com/mayahiro/go-webp/benchmarks/internal/benchmarkfixture"
	"github.com/mayahiro/go-webp/internal/benchmarkbitstream"
	"github.com/mayahiro/go-webp/internal/benchmarkcorpus"
	"github.com/mayahiro/go-webp/internal/benchmarkimage"
)

type fixture struct {
	name     string
	format   string
	split    string
	hasAlpha bool
	img      image.Image
}

type corpusConfiguration struct {
	name           string
	sha256         string
	split          string
	holdoutPercent int
	private        bool
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
	fixture      string
	encoder      string
	runs         int
	size         int64
	avg          time.Duration
	timing       losslessTimingSummary
	outputSHA256 string
	layout       benchmarkbitstream.LosslessLayout
	distortion   distortionMetrics
}

func main() {
	runs := flag.Int("runs", 3, "number of encode runs per fixture and encoder")
	mode := flag.String("mode", "default", "go-webp profile: default, fast, balanced, best, low-memory, auto, or near-lossless")
	quality := flag.Int("quality", 75, "near-lossless quality from 1 to 100")
	method := flag.Int("method", 4, "cwebp method from 0 to 6")
	outDir := flag.String("out", "", "directory for generated PNG and WebP files")
	keep := flag.Bool("keep", false, "keep generated files when out is empty")
	corpusDir := flag.String("corpus", "", "private image corpus directory; empty uses generated fixtures")
	corpusName := flag.String("corpus-name", "production", "anonymous private corpus name")
	corpusSplit := flag.String("split", "validation", "private corpus split: development, validation, or all; train and holdout remain accepted aliases")
	holdoutPercent := flag.Int("holdout", 20, "deterministic private corpus holdout percentage")
	jsonPath := flag.String("json", "", "optional JSON report path")
	fixtureIDs := flag.String("fixtures", "", "optional comma-separated fixture names or anonymous corpus IDs")
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
	fixtures, corpus, err := loadLosslessComparisonFixtures(*corpusDir, *corpusName, *corpusSplit, *holdoutPercent)
	if err != nil {
		fatal(err)
	}
	fixtures, selectedFixtureIDs, err := filterLosslessFixtures(fixtures, *fixtureIDs)
	if err != nil {
		fatal(err)
	}

	cwebpVersion, err := commandVersion("cwebp", "-version")
	if err != nil {
		fatal(err)
	}
	dwebpVersion, err := commandVersion("dwebp", "-version")
	if err != nil {
		fatal(err)
	}
	repository, err := currentRepositoryMetadata()
	if err != nil {
		fatal(err)
	}
	host := currentHostMetadata()
	fmt.Printf("workdir=%s\n", dir)
	fmt.Printf("mode=%s method=%d quality=%d cwebp=%s dwebp=%s\n", cfg.name, *method, *quality, cwebpVersion, dwebpVersion)
	if corpus.private {
		fmt.Printf("corpus=%s split=%s holdout=%d fixtures=%d private=true\n", corpus.name, corpus.split, corpus.holdoutPercent, len(fixtures))
	} else {
		fmt.Printf("corpus=%s split=%s holdout=%d sha256=%s\n", corpus.name, corpus.split, corpus.holdoutPercent, corpus.sha256)
	}
	if len(selectedFixtureIDs) != 0 {
		if corpus.private {
			fmt.Printf("fixture_filter_count=%d private=true\n", len(selectedFixtureIDs))
		} else {
			fmt.Printf("fixtures=%s\n", strings.Join(selectedFixtureIDs, ","))
		}
	}
	fmt.Printf("%-14s %-10s %4s %12s %10s %10s %8s %11s\n", "fixture", "encoder", "runs", "encoded_B", "avg_ms", "rgb_mae", "rgb_max", "alpha_exact")
	report := losslessComparisonReport{
		SchemaVersion: losslessComparisonReportSchemaVersion,
		Configuration: losslessReportConfiguration{
			Runs:                *runs,
			WarmupRuns:          losslessComparisonWarmupRuns,
			TimingStatistic:     "median_ns with min_ns and max_ns range; average_encode_ns retained for schema compatibility",
			OutputHashAlgorithm: "sha256",
			CWebPVersion:        cwebpVersion,
			CWebPMethod:         *method,
			CWebPQuality:        *quality,
			CWebPArguments:      reportCWebPArguments(cfg),
			CWebPInputFormat:    "png",
			DWebPVersion:        dwebpVersion,
			GoVersion:           runtime.Version(),
			GOOS:                runtime.GOOS,
			GOARCH:              runtime.GOARCH,
			GOMAXPROCS:          runtime.GOMAXPROCS(0),
			GoCommit:            repository.commit,
			GoDirty:             repository.dirty,
			CPUModel:            host.cpuModel,
			OSVersion:           host.osVersion,
			GoMode:              cfg.name,
			GoTimingScope:       "in-process webp.Encode call",
			CWebPTimingScope:    "process startup, PNG decode, encode, and output write",
			Corpus:              corpus.name,
			CorpusSHA256:        corpus.sha256,
			CorpusSplit:         corpus.split,
			HoldoutPercent:      corpus.holdoutPercent,
			FixtureFilter:       selectedFixtureIDs,
		},
	}
	for index, f := range fixtures {
		workFixture := f
		if corpus.private {
			workFixture.name = fmt.Sprintf("private-%03d", index+1)
		}
		pngPath := filepath.Join(dir, workFixture.name+".png")
		if err := writePNG(pngPath, workFixture.img); err != nil {
			fatal(fmt.Errorf("%s: write png: %w", workFixture.name, err))
		}
		goResult, err := runGoWebP(dir, workFixture, *runs, cfg)
		if err != nil {
			fatal(err)
		}
		libwebpResult, err := runLibWebP(dir, workFixture, pngPath, *runs, cfg)
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
		report.Fixtures = append(report.Fixtures, losslessFixtureReport{
			Name:               f.name,
			SourceFormat:       f.format,
			SourceOriginFormat: f.format,
			CWebPInputFormat:   "png",
			Split:              f.split,
			HasAlpha:           f.hasAlpha,
			Width:              f.img.Bounds().Dx(),
			Height:             f.img.Bounds().Dy(),
			GoWebP:             losslessSampleFromResult(goResult),
			CWebP:              losslessSampleFromResult(libwebpResult),
		})
	}
	report.Aggregate = aggregateLosslessFixtures(report.Fixtures)
	fmt.Printf(
		"aggregate go-webp_B=%d libwebp_B=%d go-webp_median_total_ms=%.3f libwebp_median_total_ms=%.3f\n",
		report.Aggregate.GoWebPBytes,
		report.Aggregate.CWebPBytes,
		float64(report.Aggregate.GoMedianTotalNS)/float64(time.Millisecond),
		float64(report.Aggregate.CWebPMedianTotalNS)/float64(time.Millisecond),
	)
	if *jsonPath != "" {
		if err := writeLosslessReport(*jsonPath, report); err != nil {
			fatal(err)
		}
	}
}

func filterLosslessFixtures(fixtures []fixture, raw string) ([]fixture, []string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fixtures, nil, nil
	}
	requested := make(map[string]bool)
	ordered := make([]string, 0)
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" || requested[value] {
			continue
		}
		requested[value] = true
		ordered = append(ordered, value)
	}
	selected := make([]fixture, 0, len(ordered))
	for _, candidate := range fixtures {
		if requested[candidate.name] {
			selected = append(selected, candidate)
			requested[candidate.name] = false
		}
	}
	missing := make([]string, 0)
	for _, value := range ordered {
		if requested[value] {
			missing = append(missing, value)
		}
	}
	if len(missing) != 0 {
		return nil, nil, fmt.Errorf("fixtures not found in selected corpus: %s", strings.Join(missing, ","))
	}
	if len(selected) == 0 {
		return nil, nil, errors.New("fixture filter selected no images")
	}
	return selected, ordered, nil
}

func makeComparisonConfig(mode string, quality int, method int) (comparisonConfig, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	cfg := comparisonConfig{
		name:      mode,
		goOptions: webp.Options{Compression: webp.CompressionLossless},
		cwebpArgs: []string{"-lossless", "-exact", "-q", strconv.Itoa(quality), "-m", strconv.Itoa(method)},
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

func reportCWebPArguments(cfg comparisonConfig) []string {
	args := append([]string{"-quiet"}, cfg.cwebpArgs...)
	return append(args, "<input.png>", "-o", "<output.webp>")
}

func runGoWebP(dir string, f fixture, runs int, cfg comparisonConfig) (result, error) {
	encode := func() ([]byte, time.Duration, error) {
		var buf bytes.Buffer
		start := time.Now()
		if err := webp.Encode(&buf, f.img, &cfg.goOptions); err != nil {
			return nil, time.Since(start), err
		}
		return append([]byte(nil), buf.Bytes()...), time.Since(start), nil
	}
	encoded, durations, err := runDeterministicLosslessEncodes(f.name+"/go-webp", runs, encode)
	if err != nil {
		return result{}, err
	}
	webpPath := filepath.Join(dir, f.name+".go-webp.webp")
	layout, err := benchmarkbitstream.ParseLossless(encoded)
	if err != nil {
		return result{}, fmt.Errorf("%s/go-webp: layout: %w", f.name, err)
	}
	if err := os.WriteFile(webpPath, encoded, 0o600); err != nil {
		return result{}, fmt.Errorf("%s/go-webp: write webp: %w", f.name, err)
	}
	metrics, err := decodeAndMeasure(dir, f, webpPath, "go-webp", cfg.exact)
	if err != nil {
		return result{}, err
	}
	return result{
		fixture:      f.name,
		encoder:      "go-webp",
		runs:         runs,
		size:         int64(len(encoded)),
		avg:          averageLosslessDuration(durations),
		timing:       summarizeLosslessTiming(durations),
		outputSHA256: losslessOutputSHA256(encoded),
		layout:       layout,
		distortion:   metrics,
	}, nil
}

func runLibWebP(dir string, f fixture, pngPath string, runs int, cfg comparisonConfig) (result, error) {
	webpPath := filepath.Join(dir, f.name+".libwebp.webp")
	encode := func() ([]byte, time.Duration, error) {
		args := append([]string{"-quiet"}, cfg.cwebpArgs...)
		args = append(args, pngPath, "-o", webpPath)
		cmd := exec.Command("cwebp", args...)
		start := time.Now()
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, time.Since(start), fmt.Errorf("cwebp: %w: %s", err, string(out))
		}
		elapsed := time.Since(start)
		encoded, err := os.ReadFile(webpPath)
		if err != nil {
			return nil, elapsed, fmt.Errorf("read webp: %w", err)
		}
		return encoded, elapsed, nil
	}
	encoded, durations, err := runDeterministicLosslessEncodes(f.name+"/libwebp", runs, encode)
	if err != nil {
		return result{}, err
	}
	layout, err := benchmarkbitstream.ParseLossless(encoded)
	if err != nil {
		return result{}, fmt.Errorf("%s/libwebp: layout: %w", f.name, err)
	}
	metrics, err := decodeAndMeasure(dir, f, webpPath, "libwebp", cfg.exact)
	if err != nil {
		return result{}, err
	}
	return result{
		fixture:      f.name,
		encoder:      "libwebp",
		runs:         runs,
		size:         int64(len(encoded)),
		avg:          averageLosslessDuration(durations),
		timing:       summarizeLosslessTiming(durations),
		outputSHA256: losslessOutputSHA256(encoded),
		layout:       layout,
		distortion:   metrics,
	}, nil
}

func runDeterministicLosslessEncodes(name string, runs int, encode func() ([]byte, time.Duration, error)) ([]byte, []time.Duration, error) {
	warmupOutput, _, err := encode()
	if err != nil {
		return nil, nil, fmt.Errorf("%s: warm-up encode: %w", name, err)
	}
	encoded := warmupOutput
	durations := make([]time.Duration, 0, runs)
	for run := range runs {
		candidate, elapsed, err := encode()
		if err != nil {
			return nil, nil, fmt.Errorf("%s: encode run %d: %w", name, run+1, err)
		}
		durations = append(durations, elapsed)
		if !bytes.Equal(warmupOutput, candidate) {
			return nil, nil, fmt.Errorf("%s: encoded output changed after warm-up on run %d", name, run+1)
		}
		encoded = candidate
	}
	return encoded, durations, nil
}

func averageLosslessDuration(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	var total time.Duration
	for _, duration := range durations {
		total += duration
	}
	return total / time.Duration(len(durations))
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

func loadLosslessComparisonFixtures(corpusDir string, corpusName string, split string, holdoutPercent int) ([]fixture, corpusConfiguration, error) {
	if strings.TrimSpace(corpusDir) == "" {
		shared := append(benchmarkfixture.Standard(), benchmarkfixture.Fixture{
			Name:     "hidden-rgb-alpha16",
			Category: "alpha-exact",
			Image:    hiddenRGBAlphaFixture(),
		})
		result := make([]fixture, len(shared))
		for i, f := range shared {
			identity := benchmarkimage.IdentifyPixels(f.Image)
			result[i] = fixture{name: f.Name, format: "generated", split: "all", hasAlpha: identity.HasAlpha, img: f.Image}
		}
		return result, corpusConfiguration{name: "generated-standard", split: "all"}, nil
	}
	storageSplit, reportSplit, err := normalizeLosslessCorpusSplit(split)
	if err != nil {
		return nil, corpusConfiguration{}, err
	}
	report, samples, err := benchmarkcorpus.LoadSplit(corpusDir, corpusName, holdoutPercent, storageSplit)
	if err != nil {
		return nil, corpusConfiguration{}, err
	}
	if len(samples) == 0 {
		return nil, corpusConfiguration{}, fmt.Errorf("corpus split %q contains no images", reportSplit)
	}
	result := make([]fixture, len(samples))
	for i, sample := range samples {
		result[i] = fixture{
			name:     sample.Metadata.ID,
			format:   sample.Metadata.Format,
			split:    roadmapSplitName(sample.Metadata.Split),
			hasAlpha: sample.Metadata.HasAlpha,
			img:      sample.Pixels,
		}
	}
	return result, corpusConfiguration{
		name:           report.Corpus,
		sha256:         report.CorpusSHA256,
		split:          reportSplit,
		holdoutPercent: report.HoldoutPercent,
		private:        true,
	}, nil
}

func normalizeLosslessCorpusSplit(split string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(split)) {
	case "development", "train":
		return "train", "development", nil
	case "validation", "holdout":
		return "holdout", "validation", nil
	case "all":
		return "all", "all", nil
	default:
		return "", "", fmt.Errorf("invalid corpus split %q", split)
	}
}

func roadmapSplitName(split string) string {
	switch split {
	case "train":
		return "development"
	case "holdout":
		return "validation"
	default:
		return split
	}
}

func hiddenRGBAlphaFixture() image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			alpha := uint8(255)
			if (x+y)%3 == 0 {
				alpha = 0
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(17 + x*11),
				G: uint8(29 + y*13),
				B: uint8(43 + (x+y)*7),
				A: alpha,
			})
		}
	}
	return img
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
