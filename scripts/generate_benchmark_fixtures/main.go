package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/mayahiro/go-webp/internal/benchmarkfixture"
	"github.com/mayahiro/go-webp/internal/benchmarkimage"
)

const manifestName = "manifest.json"

type corpusManifest struct {
	SchemaVersion int               `json:"schema_version"`
	Corpus        string            `json:"corpus"`
	License       string            `json:"license"`
	Fixtures      []fixtureManifest `json:"fixtures"`
}

type fixtureManifest struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	File        string `json:"file"`
	Format      string `json:"format"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	HasAlpha    bool   `json:"has_alpha"`
	PixelSHA256 string `json:"pixel_sha256"`
	PNGSHA256   string `json:"png_sha256"`
}

func main() {
	out := flag.String("out", ".local/fixtures/public", "output directory for generated PNG fixtures and manifest")
	flag.Parse()

	manifest, err := writePublicFixtures(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("generated %d fixtures in %s\n", len(manifest.Fixtures), *out)
}

func writePublicFixtures(out string) (corpusManifest, error) {
	if strings.TrimSpace(out) == "" {
		return corpusManifest{}, errors.New("output directory must not be empty")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return corpusManifest{}, fmt.Errorf("create output directory: %w", err)
	}

	manifest := corpusManifest{
		SchemaVersion: 1,
		Corpus:        "go-webp-generated-benchmark-fixtures",
		License:       "MIT",
	}
	for _, fixture := range benchmarkfixture.Standard() {
		entry, data, err := encodeFixture(fixture)
		if err != nil {
			return corpusManifest{}, fmt.Errorf("%s: %w", fixture.Name, err)
		}
		if err := os.WriteFile(filepath.Join(out, entry.File), data, 0o644); err != nil {
			return corpusManifest{}, fmt.Errorf("%s: write PNG: %w", fixture.Name, err)
		}
		manifest.Fixtures = append(manifest.Fixtures, entry)
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return corpusManifest{}, fmt.Errorf("encode manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(out, manifestName), data, 0o644); err != nil {
		return corpusManifest{}, fmt.Errorf("write manifest: %w", err)
	}
	return manifest, nil
}

func encodeFixture(fixture benchmarkfixture.Fixture) (fixtureManifest, []byte, error) {
	if fixture.Image == nil {
		return fixtureManifest{}, nil, errors.New("fixture image must not be nil")
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, fixture.Image); err != nil {
		return fixtureManifest{}, nil, fmt.Errorf("encode PNG: %w", err)
	}

	bounds := fixture.Image.Bounds()
	pngSum := sha256.Sum256(encoded.Bytes())
	pixelIdentity := benchmarkimage.IdentifyPixels(fixture.Image)
	return fixtureManifest{
		Name:        fixture.Name,
		Category:    fixture.Category,
		File:        fixture.Name + ".png",
		Format:      "png",
		Width:       bounds.Dx(),
		Height:      bounds.Dy(),
		HasAlpha:    pixelIdentity.HasAlpha,
		PixelSHA256: fmt.Sprintf("%x", pixelIdentity.SHA256),
		PNGSHA256:   fmt.Sprintf("%x", pngSum),
	}, encoded.Bytes(), nil
}
