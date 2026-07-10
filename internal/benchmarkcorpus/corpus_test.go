package benchmarkcorpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanBuildsAnonymousDeterministicReport(t *testing.T) {
	root := t.TempDir()
	writeTestJPEG(t, filepath.Join(root, "private-name.jpg"), testImage(7, 5, 3))
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	alphaImage := testImage(4, 6, 11)
	alphaPixel := alphaImage.NRGBAAt(0, 0)
	alphaPixel.A = 127
	alphaImage.SetNRGBA(0, 0, alphaPixel)
	writeTestPNG(t, filepath.Join(root, "nested", "another-private-name.png"), alphaImage)
	if err := os.WriteFile(filepath.Join(root, "ignored.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := Scan(root, "production", 20)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Scan(root, "production", 20)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("corpus report is not deterministic")
	}
	if strings.Contains(string(firstJSON), "private-name") {
		t.Fatal("corpus report exposes a source file name")
	}
	if first.Summary.Files != 2 || first.Summary.SkippedFiles != 1 {
		t.Fatalf("summary files/skipped = %d/%d, want 2/1", first.Summary.Files, first.Summary.SkippedFiles)
	}
	if first.Summary.TotalPixels != 59 {
		t.Fatalf("total pixels = %d, want 59", first.Summary.TotalPixels)
	}
	if first.Summary.Formats["jpeg"] != 1 || first.Summary.Formats["png"] != 1 || first.Summary.AlphaFiles != 1 {
		t.Fatalf("formats/alpha = %v/%d, want jpeg:1 png:1/1", first.Summary.Formats, first.Summary.AlphaFiles)
	}
	for _, entry := range first.Images {
		if len(entry.ID) != 16 || len(entry.SourceSHA256) != 64 || len(entry.PixelSHA256) != 64 {
			t.Fatalf("invalid anonymous identity lengths for %#v", entry)
		}
		if entry.Split != "train" && entry.Split != "holdout" {
			t.Fatalf("invalid split %q", entry.Split)
		}
	}
}

func TestScanCountsDuplicatePixels(t *testing.T) {
	root := t.TempDir()
	img := testImage(8, 8, 7)
	writeTestJPEG(t, filepath.Join(root, "first.jpg"), img)
	writeTestJPEG(t, filepath.Join(root, "second.jpeg"), img)

	report, err := Scan(root, "production", 20)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.UniquePixels != 1 || report.Summary.DuplicateFiles != 1 {
		t.Fatalf("unique/duplicates = %d/%d, want 1/1", report.Summary.UniquePixels, report.Summary.DuplicateFiles)
	}
}

func TestLoadSplitReturnsOnlySelectedAnonymousPixels(t *testing.T) {
	root := t.TempDir()
	for i := range 12 {
		writeTestJPEG(t, filepath.Join(root, string(rune('a'+i))+".jpg"), testImage(8+i, 6, i+1))
	}

	report, train, err := LoadSplit(root, "production", 20, "train")
	if err != nil {
		t.Fatal(err)
	}
	_, holdout, err := LoadSplit(root, "production", 20, "holdout")
	if err != nil {
		t.Fatal(err)
	}
	if len(train) != report.Summary.TrainFiles || len(holdout) != report.Summary.HoldoutFiles {
		t.Fatalf("loaded train/holdout = %d/%d, want %d/%d", len(train), len(holdout), report.Summary.TrainFiles, report.Summary.HoldoutFiles)
	}
	if len(train)+len(holdout) != report.Summary.Files {
		t.Fatalf("loaded samples = %d, want %d", len(train)+len(holdout), report.Summary.Files)
	}
	for _, sample := range append(train, holdout...) {
		if sample.Pixels == nil {
			t.Fatalf("sample %s has nil pixels", sample.Metadata.ID)
		}
		bounds := sample.Pixels.Bounds()
		if bounds.Dx() != sample.Metadata.Width || bounds.Dy() != sample.Metadata.Height {
			t.Fatalf("sample %s bounds = %v, want %dx%d", sample.Metadata.ID, bounds, sample.Metadata.Width, sample.Metadata.Height)
		}
	}
}

func TestScanRejectsCorruptImageWithoutExposingName(t *testing.T) {
	root := t.TempDir()
	const privateName = "confidential-customer-name.jpg"
	if err := os.WriteFile(filepath.Join(root, privateName), []byte("not a jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Scan(root, "production", 20)
	if err == nil {
		t.Fatal("Scan accepted a corrupt JPEG")
	}
	if strings.Contains(err.Error(), privateName) {
		t.Fatalf("error exposes the source file name: %v", err)
	}
}

func TestSplitForDigestUsesConfiguredThreshold(t *testing.T) {
	var low [sha256.Size]byte
	var high [sha256.Size]byte
	high[0] = 0xff
	high[1] = 0xff
	if got := splitForDigest(low, 20); got != "holdout" {
		t.Fatalf("low digest split = %q, want holdout", got)
	}
	if got := splitForDigest(high, 20); got != "train" {
		t.Fatalf("high digest split = %q, want train", got)
	}
}

func testImage(width int, height int, seed int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*17 + y*3 + seed),
				G: uint8(x*5 + y*19 + seed*2),
				B: uint8(x*11 + y*7 + seed*3),
				A: 255,
			})
		}
	}
	return img
}

func writeTestJPEG(t *testing.T, path string, img image.Image) {
	t.Helper()
	var data bytes.Buffer
	if err := jpeg.Encode(&data, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestPNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
