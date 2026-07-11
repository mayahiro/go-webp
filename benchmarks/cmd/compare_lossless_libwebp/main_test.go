package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	webp "github.com/mayahiro/go-webp"
	"github.com/mayahiro/go-webp/internal/benchmarkbitstream"
)

// These unit tests do not invoke the optional external cwebp and dwebp binaries
func TestMakeComparisonConfig(t *testing.T) {
	best, err := makeComparisonConfig("best", 75, 6)
	if err != nil {
		t.Fatalf("best config failed: %v", err)
	}
	if best.goOptions.Mode != webp.ModeBestCompression || !best.exact {
		t.Fatalf("best config = %#v", best)
	}
	if !slices.Equal(best.cwebpArgs, []string{"-lossless", "-m", "6"}) {
		t.Fatalf("best cwebp args = %q", best.cwebpArgs)
	}

	near, err := makeComparisonConfig("near-lossless", 75, 4)
	if err != nil {
		t.Fatalf("near-lossless config failed: %v", err)
	}
	if near.goOptions.Mode != webp.ModeNearLossless || near.goOptions.Quality != 75 || near.exact {
		t.Fatalf("near-lossless config = %#v", near)
	}
	if !slices.Equal(near.cwebpArgs, []string{"-lossless", "-m", "4", "-near_lossless", "75"}) {
		t.Fatalf("near-lossless cwebp args = %q", near.cwebpArgs)
	}
}

func TestMeasureImageReportsRGBAndAlphaError(t *testing.T) {
	want := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	want.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 40})
	got := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	got.SetNRGBA(0, 0, color.NRGBA{R: 12, G: 19, B: 30, A: 41})

	metrics, err := measureImage(got, want)
	if err != nil {
		t.Fatalf("measureImage failed: %v", err)
	}
	if metrics.rgbMAE != 1 || metrics.rgbMaxAbs != 2 || metrics.alphaExact || metrics.exact {
		t.Fatalf("metrics = %#v", metrics)
	}
}

func TestFilterLosslessFixtures(t *testing.T) {
	fixtures := []fixture{{name: "a"}, {name: "b"}, {name: "c"}}
	got, filter, err := filterLosslessFixtures(fixtures, "c, a,c")
	if err != nil {
		t.Fatal(err)
	}
	if names := []string{got[0].name, got[1].name}; !slices.Equal(names, []string{"a", "c"}) {
		t.Fatalf("filtered fixtures = %v", names)
	}
	if !slices.Equal(filter, []string{"c", "a"}) {
		t.Fatalf("recorded filter = %v", filter)
	}
	if _, _, err := filterLosslessFixtures(fixtures, "missing"); err == nil {
		t.Fatal("missing fixture filter succeeded")
	}
}

func TestLoadLosslessComparisonFixturesUsesAnonymousCorpusIdentity(t *testing.T) {
	root := t.TempDir()
	const privateName = "private-customer-name.jpg"
	file, err := os.Create(filepath.Join(root, privateName))
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 23), G: uint8(y * 31), B: uint8(x*7 + y*11), A: 255})
		}
	}
	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	fixtures, corpus, err := loadLosslessComparisonFixtures(root, "production", "all", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 1 {
		t.Fatalf("fixtures = %d, want 1", len(fixtures))
	}
	if strings.Contains(fixtures[0].name, "private") || len(fixtures[0].name) != 16 {
		t.Fatalf("fixture name is not anonymous: %q", fixtures[0].name)
	}
	if corpus.name != "production" || len(corpus.sha256) != 64 || corpus.split != "all" {
		t.Fatalf("corpus configuration = %#v", corpus)
	}
	if fixtures[0].format != "jpeg" || fixtures[0].split == "" {
		t.Fatalf("fixture metadata = %#v", fixtures[0])
	}
}

func TestAggregateLosslessFixtures(t *testing.T) {
	fixtures := []losslessFixtureReport{
		{
			GoWebP: losslessSample{EncodedBytes: 90, AverageEncodeNS: 10, Layout: benchmarkbitstream.LosslessLayout{LiteralBits: 11}},
			CWebP:  losslessSample{EncodedBytes: 100, AverageEncodeNS: 4, Layout: benchmarkbitstream.LosslessLayout{LiteralBits: 7}},
		},
		{
			GoWebP: losslessSample{EncodedBytes: 120, AverageEncodeNS: 20, Layout: benchmarkbitstream.LosslessLayout{CopyBits: 13}},
			CWebP:  losslessSample{EncodedBytes: 100, AverageEncodeNS: 6, Layout: benchmarkbitstream.LosslessLayout{CopyBits: 5}},
		},
		{
			GoWebP: losslessSample{EncodedBytes: 50, AverageEncodeNS: 30},
			CWebP:  losslessSample{EncodedBytes: 50, AverageEncodeNS: 8},
		},
	}
	got := aggregateLosslessFixtures(fixtures)
	if got.Files != 3 || got.GoWebPBytes != 260 || got.CWebPBytes != 250 {
		t.Fatalf("aggregate sizes = %#v", got)
	}
	if got.GoSizeDeltaBytes != 10 || got.GoSizeDeltaPct != 4 {
		t.Fatalf("aggregate delta = %#v", got)
	}
	if got.GoAverageEncodeNS != 60 || got.CWebPAverageNS != 18 {
		t.Fatalf("aggregate time = %#v", got)
	}
	if got.GoSmaller != 1 || got.CWebPSmaller != 1 || got.EqualSize != 1 {
		t.Fatalf("aggregate wins = %#v", got)
	}
	if got.GoWebPLayout.LiteralBits != 11 || got.GoWebPLayout.CopyBits != 13 || got.CWebPLayout.LiteralBits != 7 || got.CWebPLayout.CopyBits != 5 {
		t.Fatalf("aggregate layouts = %#v/%#v", got.GoWebPLayout, got.CWebPLayout)
	}

	zero := aggregateLosslessFixtures([]losslessFixtureReport{{}})
	if zero.GoSizeDeltaPct != 0 {
		t.Fatalf("zero cwebp delta = %v", zero.GoSizeDeltaPct)
	}
}

func TestWriteLosslessReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "report.json")
	want := losslessComparisonReport{
		SchemaVersion: 1,
		Configuration: losslessReportConfiguration{Corpus: "generated-standard"},
		Aggregate:     losslessAggregateReport{Files: 1},
	}
	if err := writeLosslessReport(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("report mode = %o, want 600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got losslessComparisonReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != want.SchemaVersion || got.Configuration.Corpus != want.Configuration.Corpus || got.Aggregate.Files != 1 {
		t.Fatalf("decoded report = %#v", got)
	}
}
