// Package benchmarkcorpus indexes local image corpora without exposing source paths
package benchmarkcorpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mayahiro/go-webp/internal/benchmarkimage"
)

const splitAlgorithm = "pixel-sha256-u16-v1"

// Report describes an anonymous, deterministic view of a local image corpus
type Report struct {
	SchemaVersion  int     `json:"schema_version"`
	Corpus         string  `json:"corpus"`
	CorpusSHA256   string  `json:"corpus_sha256"`
	HoldoutPercent int     `json:"holdout_percent"`
	SplitAlgorithm string  `json:"split_algorithm"`
	Summary        Summary `json:"summary"`
	Images         []Image `json:"images"`
}

// Image records image properties without its source name or path
type Image struct {
	ID           string `json:"id"`
	SourceSHA256 string `json:"source_sha256"`
	PixelSHA256  string `json:"pixel_sha256"`
	Format       string `json:"format"`
	HasAlpha     bool   `json:"has_alpha"`
	Bytes        int64  `json:"bytes"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Pixels       int64  `json:"pixels"`
	Split        string `json:"split"`
}

// Sample pairs anonymous metadata with decoded pixels for local comparisons
type Sample struct {
	Metadata Image
	Pixels   image.Image
}

// Summary aggregates the indexed images
type Summary struct {
	Files          int            `json:"files"`
	UniquePixels   int            `json:"unique_pixels"`
	DuplicateFiles int            `json:"duplicate_files"`
	SkippedFiles   int            `json:"skipped_files"`
	TrainFiles     int            `json:"train_files"`
	HoldoutFiles   int            `json:"holdout_files"`
	AlphaFiles     int            `json:"alpha_files"`
	TotalBytes     int64          `json:"total_bytes"`
	TotalPixels    int64          `json:"total_pixels"`
	Formats        map[string]int `json:"formats"`
	Width          Distribution   `json:"width"`
	Height         Distribution   `json:"height"`
	SourceBytes    Distribution   `json:"source_bytes"`
	PixelsPerImage Distribution   `json:"pixels_per_image"`
}

// Distribution records stable integer percentiles for a corpus property
type Distribution struct {
	Min int64 `json:"min"`
	P50 int64 `json:"p50"`
	P90 int64 `json:"p90"`
	Max int64 `json:"max"`
}

// Scan validates and anonymously indexes supported images below root
func Scan(root string, corpus string, holdoutPercent int) (Report, error) {
	report, _, err := scan(root, corpus, holdoutPercent, "")
	return report, err
}

// LoadSplit indexes the corpus and retains decoded pixels for one split
func LoadSplit(root string, corpus string, holdoutPercent int, split string) (Report, []Sample, error) {
	if split != "train" && split != "holdout" && split != "all" {
		return Report{}, nil, fmt.Errorf("invalid corpus split %q", split)
	}
	return scan(root, corpus, holdoutPercent, split)
}

func scan(root string, corpus string, holdoutPercent int, retainedSplit string) (Report, []Sample, error) {
	if strings.TrimSpace(root) == "" {
		return Report{}, nil, errors.New("corpus root must not be empty")
	}
	if strings.TrimSpace(corpus) == "" {
		return Report{}, nil, errors.New("corpus name must not be empty")
	}
	if holdoutPercent < 1 || holdoutPercent > 99 {
		return Report{}, nil, errors.New("holdout percent must be between 1 and 99")
	}

	paths, skipped, err := corpusPaths(root)
	if err != nil {
		return Report{}, nil, err
	}
	if len(paths) == 0 {
		return Report{}, nil, errors.New("corpus contains no supported images")
	}

	report := Report{
		SchemaVersion:  1,
		Corpus:         corpus,
		HoldoutPercent: holdoutPercent,
		SplitAlgorithm: splitAlgorithm,
	}
	identities := make(map[string]string, len(paths))
	uniquePixels := make(map[string]struct{}, len(paths))
	var samples []Sample
	for index, path := range paths {
		entry, decoded, err := indexImage(path, holdoutPercent)
		if err != nil {
			return Report{}, nil, anonymousEntryError(index, err)
		}
		if previous, ok := identities[entry.ID]; ok && previous != entry.PixelSHA256 {
			return Report{}, nil, fmt.Errorf("anonymous ID collision for corpus entry %d", index)
		}
		identities[entry.ID] = entry.PixelSHA256
		uniquePixels[entry.PixelSHA256] = struct{}{}
		report.Images = append(report.Images, entry)
		if retainedSplit == "all" || retainedSplit == entry.Split {
			samples = append(samples, Sample{Metadata: entry, Pixels: decoded})
		}
	}

	slices.SortFunc(report.Images, func(a Image, b Image) int {
		if order := strings.Compare(a.ID, b.ID); order != 0 {
			return order
		}
		return strings.Compare(a.SourceSHA256, b.SourceSHA256)
	})
	slices.SortFunc(samples, func(a Sample, b Sample) int {
		if order := strings.Compare(a.Metadata.ID, b.Metadata.ID); order != 0 {
			return order
		}
		return strings.Compare(a.Metadata.SourceSHA256, b.Metadata.SourceSHA256)
	})
	report.CorpusSHA256 = corpusSHA256(report.Images)
	report.Summary = summarize(report.Images, skipped, len(uniquePixels))
	return report, samples, nil
}

func corpusPaths(root string) ([]string, int, error) {
	var paths []string
	skipped := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !supportedExtension(filepath.Ext(entry.Name())) {
			skipped++
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("walk corpus: %w", err)
	}
	slices.Sort(paths)
	return paths, skipped, nil
}

func supportedExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".jpg", ".jpeg", ".png":
		return true
	default:
		return false
	}
}

func indexImage(path string, holdoutPercent int) (Image, image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Image{}, nil, fmt.Errorf("read image: %w", err)
	}
	sourceDigest := sha256.Sum256(data)
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, nil, fmt.Errorf("decode image: %w", err)
	}
	bounds := img.Bounds()
	if bounds.Empty() {
		return Image{}, nil, errors.New("decode image: empty bounds")
	}
	identity := benchmarkimage.IdentifyPixels(img)
	pixelDigest := identity.SHA256
	pixelHex := fmt.Sprintf("%x", pixelDigest)
	pixels := int64(bounds.Dx()) * int64(bounds.Dy())
	return Image{
		ID:           pixelHex[:16],
		SourceSHA256: fmt.Sprintf("%x", sourceDigest),
		PixelSHA256:  pixelHex,
		Format:       format,
		HasAlpha:     identity.HasAlpha,
		Bytes:        int64(len(data)),
		Width:        bounds.Dx(),
		Height:       bounds.Dy(),
		Pixels:       pixels,
		Split:        splitForDigest(pixelDigest, holdoutPercent),
	}, img, nil
}

func splitForDigest(digest [sha256.Size]byte, holdoutPercent int) string {
	value := uint32(binary.BigEndian.Uint16(digest[0:2]))
	if value*100 < uint32(holdoutPercent)*(1<<16) {
		return "holdout"
	}
	return "train"
}

func corpusSHA256(images []Image) string {
	hash := sha256.New()
	for _, img := range images {
		_, _ = hash.Write([]byte(img.SourceSHA256))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(img.PixelSHA256))
		_, _ = hash.Write([]byte{0})
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func summarize(images []Image, skipped int, uniquePixels int) Summary {
	widths := make([]int64, 0, len(images))
	heights := make([]int64, 0, len(images))
	bytesPerImage := make([]int64, 0, len(images))
	pixelsPerImage := make([]int64, 0, len(images))
	summary := Summary{
		Files:          len(images),
		UniquePixels:   uniquePixels,
		DuplicateFiles: len(images) - uniquePixels,
		SkippedFiles:   skipped,
		Formats:        make(map[string]int),
	}
	for _, img := range images {
		widths = append(widths, int64(img.Width))
		heights = append(heights, int64(img.Height))
		bytesPerImage = append(bytesPerImage, img.Bytes)
		pixelsPerImage = append(pixelsPerImage, img.Pixels)
		summary.TotalBytes += img.Bytes
		summary.TotalPixels += img.Pixels
		summary.Formats[img.Format]++
		if img.HasAlpha {
			summary.AlphaFiles++
		}
		if img.Split == "holdout" {
			summary.HoldoutFiles++
		} else {
			summary.TrainFiles++
		}
	}
	summary.Width = distribution(widths)
	summary.Height = distribution(heights)
	summary.SourceBytes = distribution(bytesPerImage)
	summary.PixelsPerImage = distribution(pixelsPerImage)
	return summary
}

func distribution(values []int64) Distribution {
	slices.Sort(values)
	return Distribution{
		Min: values[0],
		P50: percentile(values, 50),
		P90: percentile(values, 90),
		Max: values[len(values)-1],
	}
}

func percentile(sorted []int64, percentile int) int64 {
	index := (len(sorted) - 1) * percentile / 100
	return sorted[index]
}

func anonymousEntryError(index int, err error) error {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		err = pathError.Err
	}
	return fmt.Errorf("corpus entry %d: %w", index, err)
}
