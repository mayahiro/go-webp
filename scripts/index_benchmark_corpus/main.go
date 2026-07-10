package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mayahiro/go-webp/internal/benchmarkcorpus"
)

func main() {
	input := flag.String("in", ".local/corpus/production", "local corpus directory")
	output := flag.String("out", ".local/results/production-corpus.json", "private JSON report path")
	name := flag.String("name", "production", "anonymous corpus name")
	holdout := flag.Int("holdout", 20, "deterministic holdout percentage from 1 to 99")
	flag.Parse()

	if err := run(*input, *output, *name, *holdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(input string, output string, name string, holdout int) error {
	if strings.TrimSpace(output) == "" {
		return errors.New("output path must not be empty")
	}
	report, err := benchmarkcorpus.Scan(input, name, holdout)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode corpus report: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	if err := os.WriteFile(output, data, 0o600); err != nil {
		return fmt.Errorf("write corpus report: %w", err)
	}
	if err := os.Chmod(output, 0o600); err != nil {
		return fmt.Errorf("set corpus report permissions: %w", err)
	}

	summary := report.Summary
	fmt.Printf(
		"indexed=%d unique=%d duplicates=%d train=%d holdout=%d alpha=%d skipped=%d bytes=%d pixels=%d\n",
		summary.Files,
		summary.UniquePixels,
		summary.DuplicateFiles,
		summary.TrainFiles,
		summary.HoldoutFiles,
		summary.AlphaFiles,
		summary.SkippedFiles,
		summary.TotalBytes,
		summary.TotalPixels,
	)
	fmt.Printf(
		"width=%d/%d/%d/%d height=%d/%d/%d/%d pixels_per_image=%d/%d/%d/%d (min/p50/p90/max)\n",
		summary.Width.Min,
		summary.Width.P50,
		summary.Width.P90,
		summary.Width.Max,
		summary.Height.Min,
		summary.Height.P50,
		summary.Height.P90,
		summary.Height.Max,
		summary.PixelsPerImage.Min,
		summary.PixelsPerImage.P50,
		summary.PixelsPerImage.P90,
		summary.PixelsPerImage.Max,
	)
	return nil
}
