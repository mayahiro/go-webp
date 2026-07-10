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

	webp "github.com/mayahiro/go-webp"
)

type decoderKind string

const (
	decoderAuto   decoderKind = "auto"
	decoderDWebP  decoderKind = "dwebp"
	decoderXImage decoderKind = "ximage"
	decoderSIPS   decoderKind = "sips"
)

type fixture struct {
	name         string
	img          image.Image
	opts         *webp.Options
	want         image.Image
	lossy        bool
	maxRGBMAE    float64
	maxRGBMaxAbs int
	alphaExact   bool
}

func main() {
	decoderFlag := flag.String("decoder", string(decoderAuto), "external decoder to use: auto, dwebp, ximage, or sips")
	keep := flag.Bool("keep", false, "keep temporary files")
	flag.Parse()

	decoder, err := chooseDecoder(decoderKind(*decoderFlag))
	if err != nil {
		fatal(err)
	}

	dir, err := os.MkdirTemp("", "go-webp-lossless-external-*")
	if err != nil {
		fatal(err)
	}
	if !*keep {
		defer os.RemoveAll(dir)
	} else {
		fmt.Printf("keeping temporary files in %s\n", dir)
	}

	for _, f := range fixtures() {
		if err := verifyFixture(dir, decoder, f); err != nil {
			fatal(err)
		}
		fmt.Printf("ok %s\n", f.name)
	}
}

func chooseDecoder(kind decoderKind) (decoderKind, error) {
	switch kind {
	case decoderAuto:
		if _, err := exec.LookPath(string(decoderDWebP)); err == nil {
			return decoderDWebP, nil
		}
		if _, err := exec.LookPath("go"); err == nil {
			return decoderXImage, nil
		}
		if _, err := exec.LookPath(string(decoderSIPS)); err == nil {
			return decoderSIPS, nil
		}
		return "", errors.New("no supported external decoder found; install dwebp, install Go for x/image/webp, or run on macOS with sips")
	case decoderDWebP, decoderSIPS:
		if _, err := exec.LookPath(string(kind)); err != nil {
			return "", fmt.Errorf("%s not found in PATH", kind)
		}
		return kind, nil
	case decoderXImage:
		if _, err := exec.LookPath("go"); err != nil {
			return "", errors.New("go not found in PATH")
		}
		return kind, nil
	default:
		return "", fmt.Errorf("unsupported decoder %q", kind)
	}
}

func verifyFixture(dir string, decoder decoderKind, f fixture) error {
	webpPath := filepath.Join(dir, f.name+".webp")
	pngPath := filepath.Join(dir, f.name+".png")

	var encoded bytes.Buffer
	if err := webp.Encode(&encoded, f.img, f.opts); err != nil {
		return fmt.Errorf("%s: encode failed: %w", f.name, err)
	}
	if err := os.WriteFile(webpPath, encoded.Bytes(), 0o600); err != nil {
		return fmt.Errorf("%s: write WebP failed: %w", f.name, err)
	}
	if err := decodeToPNG(decoder, webpPath, pngPath); err != nil {
		return fmt.Errorf("%s: external decode failed: %w", f.name, err)
	}
	got, err := readPNG(pngPath)
	if err != nil {
		return fmt.Errorf("%s: read decoded PNG failed: %w", f.name, err)
	}
	want := f.want
	if want == nil {
		want = f.img
	}
	if f.lossy {
		if err := compareLossyImage(got, want, f.maxRGBMAE, f.maxRGBMaxAbs, f.alphaExact); err != nil {
			return fmt.Errorf("%s: lossy image mismatch: %w", f.name, err)
		}
		return nil
	}
	if err := compareImage(got, want); err != nil {
		return fmt.Errorf("%s: pixel mismatch: %w", f.name, err)
	}
	return nil
}

func decodeToPNG(decoder decoderKind, webpPath string, pngPath string) error {
	var cmd *exec.Cmd
	switch decoder {
	case decoderDWebP:
		cmd = exec.Command("dwebp", "-quiet", webpPath, "-o", pngPath)
	case decoderXImage:
		return decodeWithXImage(webpPath, pngPath)
	case decoderSIPS:
		cmd = exec.Command("sips", "-s", "format", "png", webpPath, "--out", pngPath)
	default:
		return fmt.Errorf("unsupported decoder %q", decoder)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %w\n%s", cmd.Args, err, bytes.TrimSpace(out))
	}
	return nil
}

func decodeWithXImage(webpPath string, pngPath string) error {
	dir, err := os.MkdirTemp("", "go-webp-ximage-decoder-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module verify\n\ngo 1.25.0\n\nrequire golang.org/x/image v0.41.0\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(ximageDecoderProgram), 0o600); err != nil {
		return err
	}
	cmd := exec.Command("go", "run", "-mod=mod", ".", webpPath, pngPath)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %w\n%s", cmd.Args, err, bytes.TrimSpace(out))
	}
	return nil
}

const ximageDecoderProgram = `package main

import (
	"image/png"
	"os"

	"golang.org/x/image/webp"
)

func main() {
	if len(os.Args) != 3 {
		panic("usage: decoder input.webp output.png")
	}
	in, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer in.Close()
	img, err := webp.Decode(in)
	if err != nil {
		panic(err)
	}
	out, err := os.Create(os.Args[2])
	if err != nil {
		panic(err)
	}
	defer out.Close()
	if err := png.Encode(out, img); err != nil {
		panic(err)
	}
}
`

func readPNG(path string) (*image.NRGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			out.SetNRGBA(x, y, color.NRGBAModel.Convert(img.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA))
		}
	}
	return out, nil
}

func compareImage(got *image.NRGBA, want image.Image) error {
	if err := compareDimensions(got, want); err != nil {
		return err
	}
	bounds := want.Bounds()
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			gotPixel := got.NRGBAAt(x, y)
			wantPixel := color.NRGBAModel.Convert(want.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA)
			if gotPixel != wantPixel {
				return fmt.Errorf("pixel (%d,%d) = %#v, want %#v", x, y, gotPixel, wantPixel)
			}
		}
	}
	return nil
}

func compareLossyImage(got *image.NRGBA, want image.Image, maxRGBMAE float64, maxRGBMaxAbs int, alphaExact bool) error {
	if err := compareDimensions(got, want); err != nil {
		return err
	}
	bounds := want.Bounds()
	var totalAbs int64
	maxAbs := 0
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			gotPixel := got.NRGBAAt(x, y)
			wantPixel := color.NRGBAModel.Convert(want.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA)
			if alphaExact && gotPixel.A != wantPixel.A {
				return fmt.Errorf("alpha at (%d,%d) = %d, want %d", x, y, gotPixel.A, wantPixel.A)
			}
			for _, abs := range []int{
				absDiff8(gotPixel.R, wantPixel.R),
				absDiff8(gotPixel.G, wantPixel.G),
				absDiff8(gotPixel.B, wantPixel.B),
			} {
				totalAbs += int64(abs)
				if abs > maxAbs {
					maxAbs = abs
				}
			}
		}
	}
	samples := bounds.Dx() * bounds.Dy() * 3
	mae := float64(totalAbs) / float64(samples)
	if mae > maxRGBMAE || maxAbs > maxRGBMaxAbs {
		return fmt.Errorf("rgb_mae = %.4f, rgb_max_abs = %d, want <= %.4f and <= %d", mae, maxAbs, maxRGBMAE, maxRGBMaxAbs)
	}
	return nil
}

func compareDimensions(got *image.NRGBA, want image.Image) error {
	bounds := want.Bounds()
	if got.Rect.Dx() != bounds.Dx() || got.Rect.Dy() != bounds.Dy() {
		return fmt.Errorf("dimensions = %dx%d, want %dx%d", got.Rect.Dx(), got.Rect.Dy(), bounds.Dx(), bounds.Dy())
	}
	return nil
}

func absDiff8(a uint8, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

func fixtures() []fixture {
	return []fixture{
		{name: "flat", img: flatFixture()},
		{name: "gradient", img: gradientFixture()},
		{name: "gray-gradient-256", img: grayGradient256Fixture()},
		{name: "rgba-offset", img: rgbaOffsetFixture()},
		{name: "gray-offset", img: grayOffsetFixture()},
		{name: "predictor-lz77", img: predictorLZ77Fixture()},
		{name: "predictor-color-transform", img: predictorColorTransformFixture()},
		{name: "subtract-green", img: subtractGreenFixture()},
		{name: "color-transform", img: colorTransformFixture()},
		{name: "color-index-sorted-table", img: colorIndexSortedTableFixture()},
		{name: "ui-color-index", img: uiColorIndexFixture()},
		{name: "palette-alpha", img: paletteAlphaFixture()},
		{name: "recent-colors", img: recentColorsFixture()},
		{name: "wide-color-cache", img: wideColorCacheFixture()},
		{name: "lz77-color-cache", img: lz77ColorCacheFixture()},
		{name: "predictor-lz77-color-cache", img: predictorLZ77ColorCacheFixture()},
		{name: "color-transform-lz77-color-cache", img: colorTransformLZ77ColorCacheFixture()},
		{name: "lz77-runs", img: lz77RunsFixture()},
		{name: "meta-prefix", img: metaPrefixFixture()},
		{name: "meta-prefix-lz77", img: metaPrefixLZ77Fixture()},
		{
			name: "near-lossless",
			img:  nearLosslessFixture(),
			opts: &webp.Options{Mode: webp.ModeNearLossless, Quality: 50},
			want: nearLosslessWant(nearLosslessFixture(), 50),
		},
		{
			name:         "lossy-quality",
			img:          lossyQualityFixture(),
			opts:         &webp.Options{Compression: webp.CompressionLossless, Mode: webp.ModeLossyQuality, Quality: 75},
			lossy:        true,
			maxRGBMAE:    24,
			maxRGBMaxAbs: 96,
			alphaExact:   true,
		},
	}
}

func nearLosslessWant(src image.Image, quality int) image.Image {
	bounds := src.Bounds()
	out := image.NewNRGBA(bounds)
	width, height := bounds.Dx(), bounds.Dy()
	pixels := make([]color.NRGBA, width*height)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixels[(y-bounds.Min.Y)*width+x-bounds.Min.X] = color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
		}
	}
	bits := nearLosslessQuantizationBits(quality)
	if bits > 0 && !(width < 64 && height < 64) && height >= 3 {
		scratch := make([]color.NRGBA, width*3)
		for passBits := bits; passBits > 0; passBits-- {
			applyNearLosslessPass(pixels, width, height, passBits, scratch)
		}
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			out.SetNRGBA(bounds.Min.X+x, bounds.Min.Y+y, pixels[y*width+x])
		}
	}
	return out
}

func nearLosslessQuantizationBits(quality int) int {
	if quality >= 100 {
		return 0
	}
	if quality < 0 {
		quality = 0
	}
	return 5 - quality/20
}

func applyNearLosslessPass(pixels []color.NRGBA, width int, height int, bits int, scratch []color.NRGBA) {
	previous := scratch[:width]
	current := scratch[width : width*2]
	next := scratch[width*2 : width*3]
	copy(current, pixels[:width])
	if height > 1 {
		copy(next, pixels[width:width*2])
	}
	limit := 1 << bits
	step := uint8(limit)
	for y := 0; y < height; y++ {
		if y > 0 && y < height-1 {
			copy(next, pixels[(y+1)*width:(y+2)*width])
			for x := 1; x < width-1; x++ {
				pixel := current[x]
				if nearLosslessRGBSmooth(pixel, current[x-1], current[x+1], previous[x], next[x], limit) {
					continue
				}
				pixel.R = quantizeNearLosslessChannel(pixel.R, step)
				pixel.G = quantizeNearLosslessChannel(pixel.G, step)
				pixel.B = quantizeNearLosslessChannel(pixel.B, step)
				pixels[y*width+x] = pixel
			}
		}
		previous, current, next = current, next, previous
	}
}

func nearLosslessRGBSmooth(center color.NRGBA, left color.NRGBA, right color.NRGBA, above color.NRGBA, below color.NRGBA, limit int) bool {
	return nearLosslessRGBNear(center, left, limit) &&
		nearLosslessRGBNear(center, right, limit) &&
		nearLosslessRGBNear(center, above, limit) &&
		nearLosslessRGBNear(center, below, limit)
}

func nearLosslessRGBNear(a color.NRGBA, b color.NRGBA, limit int) bool {
	return absDiff8(a.R, b.R) < limit &&
		absDiff8(a.G, b.G) < limit &&
		absDiff8(a.B, b.B) < limit
}

func quantizeNearLosslessChannel(value uint8, step uint8) uint8 {
	mask := int(step) - 1
	biased := int(value) + mask/2 + ((int(value) / int(step)) & 1)
	if biased > 255 {
		return 255
	}
	return uint8(biased &^ mask)
}

func flatFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	fill(img, func(int, int) color.NRGBA {
		return color.NRGBA{R: 16, G: 32, B: 48, A: 255}
	})
	return img
}

func nearLosslessFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	fill(img, func(x int, y int) color.NRGBA {
		return color.NRGBA{
			R: uint8((x*7 + y*3 + x*y) & 0xff),
			G: uint8((x*5 + y*11 + 17) & 0xff),
			B: uint8((x*13 + y*2 + 29) & 0xff),
			A: uint8(96 + (x+y)%160),
		}
	})
	return img
}

func lossyQualityFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	fill(img, func(x int, y int) color.NRGBA {
		return color.NRGBA{
			R: uint8(32 + x*3),
			G: uint8(24 + y*3),
			B: uint8(16 + x + y*2),
			A: 255,
		}
	})
	return img
}

func gradientFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 128, 128))
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

func grayGradient256Fixture() *image.Gray {
	img := image.NewGray(image.Rect(0, 0, 256, 256))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			r := uint8(x*3 + y)
			g := uint8(y*5 + x/2)
			b := uint8((x+y)*2 + x*y/17)
			img.SetGray(x, y, color.Gray{Y: fixtureLuma(r, g, b)})
		}
	}
	return img
}

func fixtureLuma(r uint8, g uint8, b uint8) uint8 {
	r1 := int32(r)
	g1 := int32(g)
	b1 := int32(b)
	return uint8((19595*r1 + 38470*g1 + 7471*b1 + 1<<15) >> 16)
}

func rgbaOffsetFixture() *image.RGBA {
	img := image.NewRGBA(image.Rect(7, 9, 103, 73))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			if (x+y)%17 == 0 {
				img.SetRGBA(x, y, color.RGBA{
					R: uint8((x + y) % 96),
					G: uint8((x*2 + y) % 96),
					B: uint8((x + y*3) % 96),
					A: 128,
				})
				continue
			}
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(x*3 + y),
				G: uint8(x + y*5),
				B: uint8(x*7 + y*11),
				A: 255,
			})
		}
	}
	return img
}

func grayOffsetFixture() *image.Gray {
	img := image.NewGray(image.Rect(11, 13, 107, 77))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(x*3 + y*5 + x*y/17)})
		}
	}
	return img
}

func metaPrefixFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 512, 256))
	fill(img, func(x int, y int) color.NRGBA {
		v := uint32(x)*747796405 + uint32(y)*2891336453 + uint32(x*y)*3266489917 + 0x9e3779b9
		v ^= v >> 16
		v *= 2246822519
		v ^= v >> 13
		v *= 3266489917
		v ^= v >> 16
		a := uint8(255)
		if x >= 256 {
			a = uint8(32 + (v>>24)%224)
		}
		return color.NRGBA{
			R: uint8(v >> 16),
			G: uint8(v >> 8),
			B: uint8(v),
			A: a,
		}
	})
	return img
}

func metaPrefixLZ77Fixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 1))
	for x := 0; x < 16; x++ {
		c := color.NRGBA{
			R: uint8(17 + x*3),
			G: uint8(41 + x*5),
			B: uint8(83 + x*7),
			A: 255,
		}
		img.SetNRGBA(x, 0, c)
		img.SetNRGBA(16+x, 0, c)
		d := color.NRGBA{
			R: uint8(191 - x*3),
			G: uint8(149 - x*5),
			B: uint8(107 - x*7),
			A: 255,
		}
		img.SetNRGBA(32+x, 0, d)
		img.SetNRGBA(48+x, 0, d)
	}
	return img
}

func predictorLZ77Fixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 512, 2))
	residuals := []color.NRGBA{
		{R: 1, G: 3, B: 5},
		{R: 2, G: 5, B: 7},
		{R: 3, G: 7, B: 11},
		{R: 5, G: 11, B: 13},
		{R: 7, G: 13, B: 17},
		{R: 11, G: 17, B: 19},
		{R: 13, G: 19, B: 23},
		{R: 17, G: 23, B: 29},
	}
	for y := 0; y < img.Rect.Dy(); y++ {
		current := color.NRGBA{R: uint8(y * 7), G: uint8(y * 11), B: uint8(y * 13), A: 255}
		for x := 0; x < img.Rect.Dx(); x++ {
			r := residuals[(x+y)%len(residuals)]
			current.R += r.R
			current.G += r.G
			current.B += r.B
			img.SetNRGBA(x, y, current)
		}
	}
	return img
}

func predictorColorTransformFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 512, 1))
	current := color.NRGBA{A: 255}
	state := uint32(0x12345678)
	for x := 0; x < img.Rect.Dx(); x++ {
		state = state*1664525 + 1013904223
		g := uint8(state >> 24)
		state = state*1664525 + 1013904223
		b := uint8(state >> 24)
		delta := uint8(7)
		if x&1 != 0 {
			delta = 13
		}
		residual := color.NRGBA{
			R: g + delta,
			G: g,
			B: b,
		}
		current.R += residual.R
		current.G += residual.G
		current.B += residual.B
		img.SetNRGBA(x, 0, current)
	}
	return img
}

func subtractGreenFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	fill(img, func(x int, y int) color.NRGBA {
		g := uint8((x*73 + y*151 + x*y*199 + x*x*17 + y*y*29) & 0xff)
		return color.NRGBA{R: g + 7, G: g, B: g + 19, A: 255}
	})
	return img
}

func colorTransformFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	fill(img, func(x int, y int) color.NRGBA {
		r := uint8((x*83 + y*157 + x*y*197 + x*x*19 + y*y*31) & 0xff)
		g := uint8((x*29 + y*47 + x*y*211 + x*x*41 + y*y*23) & 0xff)
		return color.NRGBA{R: r, G: g, B: r + 23, A: 255}
	})
	return img
}

func paletteAlphaFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	palette := []color.NRGBA{
		{R: 10, G: 20, B: 30, A: 255},
		{R: 80, G: 20, B: 120, A: 192},
		{R: 10, G: 160, B: 90, A: 128},
		{R: 220, G: 180, B: 40, A: 64},
	}
	fill(img, func(x int, y int) color.NRGBA {
		return palette[(x/8+y/8)%len(palette)]
	})
	return img
}

func colorIndexSortedTableFixture() *image.NRGBA {
	sortedPalette := colorIndexSortedTablePalette()
	img := image.NewNRGBA(image.Rect(0, 0, len(sortedPalette)*8, 2))
	for x := range sortedPalette {
		paletteIndex := (x * 37) % len(sortedPalette)
		img.SetNRGBA(x, 0, sortedPalette[paletteIndex])
	}
	for x := len(sortedPalette); x < img.Rect.Dx(); x++ {
		img.SetNRGBA(x, 0, sortedPalette[x%len(sortedPalette)])
	}
	for x := 0; x < img.Rect.Dx(); x++ {
		img.SetNRGBA(x, 1, sortedPalette[x%len(sortedPalette)])
	}
	return img
}

func colorIndexSortedTablePalette() []color.NRGBA {
	const size = 64
	sortedPalette := make([]color.NRGBA, size)
	for i := range sortedPalette {
		v := uint8(i * 3)
		sortedPalette[i] = color.NRGBA{R: v, G: v, B: v, A: 255}
	}
	return sortedPalette
}

func uiColorIndexFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	fill(img, func(x int, y int) color.NRGBA {
		if y < 40 {
			return color.NRGBA{R: 31, G: 41, B: 55, A: 255}
		}
		if x < 72 {
			return color.NRGBA{R: 241, G: 245, B: 249, A: 255}
		}
		if x%96 < 2 || y%64 < 2 {
			return color.NRGBA{R: 203, G: 213, B: 225, A: 255}
		}
		if x%112 > 12 && x%112 < 64 && y%88 > 18 && y%88 < 30 {
			return color.NRGBA{R: 59, G: 130, B: 246, A: 255}
		}
		if x%112 > 12 && x%112 < 88 && y%88 > 44 && y%88 < 48 {
			return color.NRGBA{R: 100, G: 116, B: 139, A: 255}
		}
		return color.NRGBA{R: 248, G: 250, B: 252, A: 255}
	})
	return img
}

func recentColorsFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 1200, 1))
	fill(img, func(x int, _ int) color.NRGBA {
		group := x / 3
		first := colorCacheFixtureColor(group, 0)
		if x%3 == 0 || x%3 == 2 {
			return first
		}
		return colorCacheFixtureColor(group, 1)
	})
	return img
}

func colorCacheFixtureColor(group int, salt uint32) color.NRGBA {
	v := uint32(group)*747796405 + salt*2891336453 + 0x9e3779b9
	v ^= v >> 16
	v *= 2246822519
	v ^= v >> 13
	v *= 3266489917
	v ^= v >> 16
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

func wideColorCacheFixture() *image.NRGBA {
	const bits = 5
	size := 1 << bits
	img := image.NewNRGBA(image.Rect(0, 0, size*2, 20))
	for y := 0; y < img.Rect.Dy(); y++ {
		colors := colorCacheFixtureColorsByIndex(bits, uint32(17+y))
		for x, c := range colors {
			img.SetNRGBA(x, y, c)
		}
		for i := 0; i < size; i++ {
			index := i * 2
			if index >= size {
				index = (i-size/2)*2 + 1
			}
			img.SetNRGBA(size+i, y, colors[index])
		}
	}
	return img
}

func lz77ColorCacheFixture() *image.NRGBA {
	recent := recentColorsFixture()
	img := image.NewNRGBA(image.Rect(0, 0, recent.Rect.Dx()+96, 1))
	for x := 0; x < recent.Rect.Dx(); x++ {
		img.SetNRGBA(x, 0, recent.NRGBAAt(x, 0))
	}
	for x := 0; x < 96; x++ {
		img.SetNRGBA(recent.Rect.Dx()+x, 0, recent.NRGBAAt(x, 0))
	}
	return img
}

func predictorLZ77ColorCacheFixture() *image.NRGBA {
	residuals := lz77ColorCacheFixture()
	img := image.NewNRGBA(residuals.Rect)
	current := color.NRGBA{A: 255}
	for x := 0; x < residuals.Rect.Dx(); x++ {
		residual := residuals.NRGBAAt(x, 0)
		current.R += residual.R
		current.G += residual.G
		current.B += residual.B
		img.SetNRGBA(x, 0, current)
	}
	return img
}

func colorTransformLZ77ColorCacheFixture() *image.NRGBA {
	residuals := colorTransformLZ77ColorCacheResidualFixture()
	img := image.NewNRGBA(residuals.Rect)
	element := webpColorTransformElement{greenToRed: 32}
	for x := 0; x < residuals.Rect.Dx(); x++ {
		img.SetNRGBA(x, 0, inverseWebPColorTransform(residuals.NRGBAAt(x, 0), element))
	}
	return img
}

func colorTransformLZ77ColorCacheResidualFixture() *image.NRGBA {
	const groups = 800
	const repeatedPrefix = 192
	width := groups*3 + repeatedPrefix
	img := image.NewNRGBA(image.Rect(0, 0, width, 1))
	for x := 0; x < groups*3; x += 3 {
		group := x / 3
		first := colorTransformLZ77ColorCacheResidualColor(group, 0)
		second := colorTransformLZ77ColorCacheResidualColor(group, 1)
		img.SetNRGBA(x, 0, first)
		img.SetNRGBA(x+1, 0, second)
		img.SetNRGBA(x+2, 0, first)
	}
	for x := 0; x < repeatedPrefix; x++ {
		img.SetNRGBA(groups*3+x, 0, img.NRGBAAt(x, 0))
	}
	return img
}

func colorTransformLZ77ColorCacheResidualColor(group int, salt uint32) color.NRGBA {
	v := uint32(group)*747796405 + salt*2891336453 + 0x85ebca6b
	v ^= v >> 16
	v *= 2246822519
	v ^= v >> 13
	v *= 3266489917
	v ^= v >> 16
	return color.NRGBA{R: 7, G: uint8(v >> 8), B: uint8(v), A: 255}
}

type webpColorTransformElement struct {
	greenToRed  uint8
	greenToBlue uint8
	redToBlue   uint8
}

func inverseWebPColorTransform(c color.NRGBA, element webpColorTransformElement) color.NRGBA {
	red := c.R + webpColorTransformDelta(element.greenToRed, c.G)
	blue := c.B + webpColorTransformDelta(element.greenToBlue, c.G) + webpColorTransformDelta(element.redToBlue, red)
	return color.NRGBA{
		R: red,
		G: c.G,
		B: blue,
		A: c.A,
	}
}

func webpColorTransformDelta(t uint8, c uint8) uint8 {
	return uint8((int(int8(t)) * int(int8(c))) >> 5)
}

func colorCacheFixtureColorsByIndex(bits uint8, salt uint32) []color.NRGBA {
	size := 1 << bits
	colors := make([]color.NRGBA, size)
	seen := make([]bool, size)
	for seed, found := 0, 0; found < size; seed++ {
		c := colorCacheFixtureColor(seed, salt)
		index := webpColorCacheIndex(c, bits)
		if seen[index] {
			continue
		}
		seen[index] = true
		colors[index] = c
		found++
	}
	return colors
}

func webpColorCacheIndex(pixel color.NRGBA, bits uint8) int {
	colorValue := uint32(pixel.A)<<24 | uint32(pixel.R)<<16 | uint32(pixel.G)<<8 | uint32(pixel.B)
	return int((0x1e35a7bd * colorValue) >> (32 - bits))
}

func lz77RunsFixture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	fill(img, func(x int, y int) color.NRGBA {
		id := y*8 + x/8
		return color.NRGBA{
			R: uint8(id),
			G: uint8(id * 37),
			B: uint8(id * 73),
			A: 255,
		}
	})
	return img
}

func fill(img *image.NRGBA, pixel func(int, int) color.NRGBA) {
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, pixel(x, y))
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
