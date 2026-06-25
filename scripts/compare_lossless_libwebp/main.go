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
	"time"

	webp "github.com/mayahiro/go-webp"
)

type fixture struct {
	name string
	img  image.Image
}

type result struct {
	fixture string
	encoder string
	runs    int
	size    int64
	avg     time.Duration
}

func main() {
	runs := flag.Int("runs", 3, "number of encode runs per fixture and encoder")
	outDir := flag.String("out", "", "directory for generated PNG and WebP files")
	keep := flag.Bool("keep", false, "keep generated files when out is empty")
	flag.Parse()
	if *runs <= 0 {
		fatal(errors.New("runs must be positive"))
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

	fmt.Printf("workdir=%s\n", dir)
	fmt.Printf("%-14s %-10s %4s %12s %12s\n", "fixture", "encoder", "runs", "encoded_B", "avg_ms")
	for _, f := range fixtures() {
		pngPath := filepath.Join(dir, f.name+".png")
		if err := writePNG(pngPath, f.img); err != nil {
			fatal(fmt.Errorf("%s: write png: %w", f.name, err))
		}
		goResult, err := runGoWebP(dir, f, *runs)
		if err != nil {
			fatal(err)
		}
		libwebpResult, err := runLibWebP(dir, f, pngPath, *runs)
		if err != nil {
			fatal(err)
		}
		for _, r := range []result{goResult, libwebpResult} {
			fmt.Printf("%-14s %-10s %4d %12d %12.3f\n", r.fixture, r.encoder, r.runs, r.size, float64(r.avg.Microseconds())/1000)
		}
	}
}

func runGoWebP(dir string, f fixture, runs int) (result, error) {
	var total time.Duration
	var encoded []byte
	for i := 0; i < runs; i++ {
		var buf bytes.Buffer
		start := time.Now()
		if err := webp.Encode(&buf, f.img, &webp.Options{Compression: webp.CompressionLossless}); err != nil {
			return result{}, fmt.Errorf("%s/go-webp: encode: %w", f.name, err)
		}
		total += time.Since(start)
		encoded = buf.Bytes()
	}
	webpPath := filepath.Join(dir, f.name+".go-webp.webp")
	if err := os.WriteFile(webpPath, encoded, 0o600); err != nil {
		return result{}, fmt.Errorf("%s/go-webp: write webp: %w", f.name, err)
	}
	if err := verifyDWebP(dir, f, webpPath, "go-webp"); err != nil {
		return result{}, err
	}
	return result{fixture: f.name, encoder: "go-webp", runs: runs, size: int64(len(encoded)), avg: total / time.Duration(runs)}, nil
}

func runLibWebP(dir string, f fixture, pngPath string, runs int) (result, error) {
	webpPath := filepath.Join(dir, f.name+".libwebp.webp")
	var total time.Duration
	for i := 0; i < runs; i++ {
		cmd := exec.Command("cwebp", "-quiet", "-lossless", pngPath, "-o", webpPath)
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
	if err := verifyDWebP(dir, f, webpPath, "libwebp"); err != nil {
		return result{}, err
	}
	return result{fixture: f.name, encoder: "libwebp", runs: runs, size: info.Size(), avg: total / time.Duration(runs)}, nil
}

func verifyDWebP(dir string, f fixture, webpPath string, suffix string) error {
	pngPath := filepath.Join(dir, f.name+"."+suffix+".decoded.png")
	cmd := exec.Command("dwebp", "-quiet", webpPath, "-o", pngPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s/%s: dwebp: %w: %s", f.name, suffix, err, string(out))
	}
	got, err := readPNG(pngPath)
	if err != nil {
		return fmt.Errorf("%s/%s: read decoded png: %w", f.name, suffix, err)
	}
	if err := compareImage(got, f.img); err != nil {
		return fmt.Errorf("%s/%s: decoded image mismatch: %w", f.name, suffix, err)
	}
	return nil
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

func compareImage(got image.Image, want image.Image) error {
	gotBounds := got.Bounds()
	wantBounds := want.Bounds()
	if gotBounds.Dx() != wantBounds.Dx() || gotBounds.Dy() != wantBounds.Dy() {
		return fmt.Errorf("dimensions = %dx%d, want %dx%d", gotBounds.Dx(), gotBounds.Dy(), wantBounds.Dx(), wantBounds.Dy())
	}
	for y := 0; y < wantBounds.Dy(); y++ {
		for x := 0; x < wantBounds.Dx(); x++ {
			gotPixel := color.NRGBAModel.Convert(got.At(gotBounds.Min.X+x, gotBounds.Min.Y+y)).(color.NRGBA)
			wantPixel := color.NRGBAModel.Convert(want.At(wantBounds.Min.X+x, wantBounds.Min.Y+y)).(color.NRGBA)
			if gotPixel != wantPixel {
				return fmt.Errorf("pixel (%d,%d) = %#v, want %#v", x, y, gotPixel, wantPixel)
			}
		}
	}
	return nil
}

func fixtures() []fixture {
	return []fixture{
		{name: "gradient128", img: gradientFixture(128, 128)},
		{name: "ui256", img: uiFixture(256, 256)},
		{name: "flat128", img: flatFixture(128, 128)},
		{name: "palette256", img: paletteFixture(256, 256)},
		{name: "alpha128", img: alphaFixture(128, 128)},
		{name: "photo512", img: photoLikeFixture(512, 512)},
	}
}

func gradientFixture(width int, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	fill(img, func(x int, y int) color.NRGBA {
		return color.NRGBA{
			R: uint8((x*3 + y) & 0xff),
			G: uint8((x + y*5) & 0xff),
			B: uint8((x*7 + y*11) & 0xff),
			A: 255,
		}
	})
	return img
}

func uiFixture(width int, height int) *image.NRGBA {
	palette := []color.NRGBA{
		{R: 22, G: 27, B: 34, A: 255},
		{R: 36, G: 41, B: 47, A: 255},
		{R: 87, G: 166, B: 74, A: 255},
		{R: 210, G: 214, B: 220, A: 255},
		{R: 246, G: 248, B: 250, A: 255},
		{R: 48, G: 116, B: 190, A: 255},
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	fill(img, func(x int, y int) color.NRGBA {
		if x < width/8 || y < height/10 {
			return palette[0]
		}
		if (x/24+y/18)%7 == 0 {
			return palette[2]
		}
		if (x/48+y/32)%5 == 0 {
			return palette[5]
		}
		if (x+y)%19 == 0 {
			return palette[3]
		}
		return palette[(x/64+y/40)%2+3]
	})
	return img
}

func flatFixture(width int, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	fill(img, func(int, int) color.NRGBA {
		return color.NRGBA{R: 16, G: 32, B: 48, A: 255}
	})
	return img
}

func paletteFixture(width int, height int) *image.Paletted {
	palette := make(color.Palette, 16)
	for i := range palette {
		palette[i] = color.NRGBA{
			R: uint8(i * 17),
			G: uint8((i*37 + 19) & 0xff),
			B: uint8((i*53 + 7) & 0xff),
			A: 255,
		}
	}
	img := image.NewPaletted(image.Rect(0, 0, width, height), palette)
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetColorIndex(x, y, uint8((x/4+y/4+x*y)%len(palette)))
		}
	}
	return img
}

func alphaFixture(width int, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	fill(img, func(x int, y int) color.NRGBA {
		return color.NRGBA{
			R: uint8((x*5 + y*2) & 0xff),
			G: uint8((x*3 + y*7 + 11) & 0xff),
			B: uint8((x*13 + y*17 + 29) & 0xff),
			A: uint8(64 + (x*3+y*5)%192),
		}
	})
	return img
}

func photoLikeFixture(width int, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	fill(img, func(x int, y int) color.NRGBA {
		base := (x*37 + y*53 + x*y*3) & 0xff
		return color.NRGBA{
			R: uint8((base + x/3 + y/5) & 0xff),
			G: uint8((base + x/7 + y/2 + 17) & 0xff),
			B: uint8((base + x/5 + y/11 + 41) & 0xff),
			A: 255,
		}
	})
	return img
}

func fill(img *image.NRGBA, fn func(x int, y int) color.NRGBA) {
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, fn(x-img.Rect.Min.X, y-img.Rect.Min.Y))
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
