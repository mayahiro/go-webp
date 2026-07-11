package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mayahiro/go-webp/benchmarks/internal/benchmarkfixture"
	"github.com/mayahiro/go-webp/internal/benchmarkimage"
)

func TestWritePublicFixturesCreatesValidCorpus(t *testing.T) {
	dir := t.TempDir()
	generated, err := writePublicFixtures(dir)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	var stored corpusManifest
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, generated) {
		t.Fatalf("stored manifest differs from generated manifest")
	}
	if len(stored.Fixtures) != len(benchmarkfixture.Standard()) {
		t.Fatalf("fixture count = %d, want %d", len(stored.Fixtures), len(benchmarkfixture.Standard()))
	}

	names := make(map[string]struct{}, len(stored.Fixtures))
	for _, fixture := range stored.Fixtures {
		if fixture.Category == "" {
			t.Fatalf("%s has an empty category", fixture.Name)
		}
		if _, ok := names[fixture.Name]; ok {
			t.Fatalf("duplicate fixture name %q", fixture.Name)
		}
		names[fixture.Name] = struct{}{}
		pngData, err := os.ReadFile(filepath.Join(dir, fixture.File))
		if err != nil {
			t.Fatal(err)
		}
		pngSum := sha256.Sum256(pngData)
		if got := fmt.Sprintf("%x", pngSum); got != fixture.PNGSHA256 {
			t.Fatalf("%s PNG SHA-256 = %s, want %s", fixture.Name, got, fixture.PNGSHA256)
		}
		img, err := png.Decode(bytes.NewReader(pngData))
		if err != nil {
			t.Fatalf("%s: decode PNG: %v", fixture.Name, err)
		}
		if img.Bounds().Dx() != fixture.Width || img.Bounds().Dy() != fixture.Height {
			t.Fatalf("%s dimensions = %v, want %dx%d", fixture.Name, img.Bounds(), fixture.Width, fixture.Height)
		}
		if got := fmt.Sprintf("%x", benchmarkimage.IdentifyPixels(img).SHA256); got != fixture.PixelSHA256 {
			t.Fatalf("%s pixel SHA-256 = %s, want %s", fixture.Name, got, fixture.PixelSHA256)
		}
	}
}

func TestWritePublicFixturesIsDeterministic(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	if _, err := writePublicFixtures(first); err != nil {
		t.Fatal(err)
	}
	if _, err := writePublicFixtures(second); err != nil {
		t.Fatal(err)
	}

	files := []string{manifestName}
	for _, fixture := range benchmarkfixture.Standard() {
		files = append(files, fixture.Name+".png")
	}
	for _, name := range files {
		firstData, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatal(err)
		}
		secondData, err := os.ReadFile(filepath.Join(second, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(firstData, secondData) {
			t.Fatalf("%s is not deterministic", name)
		}
	}
}

func TestWritePublicFixturesRejectsEmptyOutputDirectory(t *testing.T) {
	if _, err := writePublicFixtures(" "); err == nil {
		t.Fatal("writePublicFixtures accepted an empty output directory")
	}
}
