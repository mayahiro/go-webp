package benchmarkbitstream

import "fmt"

const (
	vp8lLiteralCodeCount  = 256
	vp8lLengthCodeCount   = 24
	vp8lDistanceCodeCount = 40
)

type vp8lPixel struct {
	r uint8
	g uint8
	b uint8
	a uint8
}

type vp8lPrefixGroup struct {
	green    vp8lTree
	red      vp8lTree
	blue     vp8lTree
	alpha    vp8lTree
	distance vp8lTree
}

type vp8lImageStats struct {
	ColorCacheHeaderBits uint64
	MetaPrefixHeaderBits uint64
	MetaPrefixImageBits  uint64
	HuffmanTreeBits      uint64
	LiteralBits          uint64
	ColorCacheTokenBits  uint64
	CopyBits             uint64
	ColorCacheCodeBits   int
	PrefixBits           int
	EntropyGroups        int
	LiteralTokens        int
	ColorCacheTokens     int
	CopyTokens           int
	CopyPixels           int
	CodedPixels          int
}

func decodeVP8LImageData(r *vp8lBitReader, width int, height int, allowMetaPrefix bool) ([]vp8lPixel, vp8lImageStats, error) {
	stats := vp8lImageStats{}
	if width <= 0 || height <= 0 || width > int(^uint(0)>>1)/height {
		return nil, stats, fmt.Errorf("invalid VP8L image dimensions %dx%d", width, height)
	}
	stats.CodedPixels = width * height

	colorCacheStart := r.position()
	colorCachePresent, err := r.read(1)
	if err != nil {
		return nil, stats, err
	}
	colorCacheSize := 0
	if colorCachePresent != 0 {
		bits, err := r.read(4)
		if err != nil {
			return nil, stats, err
		}
		if bits < 1 || bits > 11 {
			return nil, stats, fmt.Errorf("invalid VP8L color cache bits %d", bits)
		}
		stats.ColorCacheCodeBits = int(bits)
		colorCacheSize = 1 << bits
	}
	stats.ColorCacheHeaderBits = r.position() - colorCacheStart

	prefixBits := 0
	prefixImageWidth := 0
	var entropyImage []vp8lPixel
	groupCount := 1
	if allowMetaPrefix {
		metaStart := r.position()
		metaPrefixPresent, err := r.read(1)
		if err != nil {
			return nil, stats, err
		}
		if metaPrefixPresent != 0 {
			rawPrefixBits, err := r.read(3)
			if err != nil {
				return nil, stats, err
			}
			prefixBits = int(rawPrefixBits) + 2
			stats.PrefixBits = prefixBits
			stats.MetaPrefixHeaderBits = r.position() - metaStart
			prefixImageWidth = divideRoundUp(width, 1<<prefixBits)
			prefixImageHeight := divideRoundUp(height, 1<<prefixBits)
			imageStart := r.position()
			entropyImage, _, err = decodeVP8LImageData(r, prefixImageWidth, prefixImageHeight, false)
			if err != nil {
				return nil, stats, fmt.Errorf("VP8L entropy image: %w", err)
			}
			stats.MetaPrefixImageBits = r.position() - imageStart
			maximumCode := 0
			for _, pixel := range entropyImage {
				code := int(pixel.r)<<8 | int(pixel.g)
				maximumCode = max(maximumCode, code)
			}
			groupCount = maximumCode + 1
		} else {
			stats.MetaPrefixHeaderBits = r.position() - metaStart
		}
	}
	stats.EntropyGroups = groupCount

	groups := make([]vp8lPrefixGroup, groupCount)
	treeStart := r.position()
	for index := range groups {
		green, err := readVP8LTree(r, vp8lLiteralCodeCount+vp8lLengthCodeCount+colorCacheSize)
		if err != nil {
			return nil, stats, fmt.Errorf("VP8L green tree %d: %w", index, err)
		}
		red, err := readVP8LTree(r, vp8lLiteralCodeCount)
		if err != nil {
			return nil, stats, fmt.Errorf("VP8L red tree %d: %w", index, err)
		}
		blue, err := readVP8LTree(r, vp8lLiteralCodeCount)
		if err != nil {
			return nil, stats, fmt.Errorf("VP8L blue tree %d: %w", index, err)
		}
		alpha, err := readVP8LTree(r, vp8lLiteralCodeCount)
		if err != nil {
			return nil, stats, fmt.Errorf("VP8L alpha tree %d: %w", index, err)
		}
		distance, err := readVP8LTree(r, vp8lDistanceCodeCount)
		if err != nil {
			return nil, stats, fmt.Errorf("VP8L distance tree %d: %w", index, err)
		}
		groups[index] = vp8lPrefixGroup{green: green, red: red, blue: blue, alpha: alpha, distance: distance}
	}
	stats.HuffmanTreeBits = r.position() - treeStart

	pixels := make([]vp8lPixel, width*height)
	colorCache := make([]vp8lPixel, colorCacheSize)
	for position := 0; position < len(pixels); {
		groupIndex := 0
		if entropyImage != nil {
			x := position % width
			y := position / width
			entropyIndex := (y>>prefixBits)*prefixImageWidth + (x >> prefixBits)
			if entropyIndex < 0 || entropyIndex >= len(entropyImage) {
				return nil, stats, fmt.Errorf("VP8L entropy index %d out of range", entropyIndex)
			}
			pixel := entropyImage[entropyIndex]
			groupIndex = int(pixel.r)<<8 | int(pixel.g)
			if groupIndex >= len(groups) {
				return nil, stats, fmt.Errorf("VP8L prefix group %d out of range", groupIndex)
			}
		}
		group := &groups[groupIndex]
		tokenStart := r.position()
		greenSymbol, err := decodeVP8LTreeSymbol(r, &group.green)
		if err != nil {
			return nil, stats, fmt.Errorf("VP8L green symbol at pixel %d group %d: %w", position, groupIndex, err)
		}
		switch {
		case greenSymbol < vp8lLiteralCodeCount:
			red, err := decodeVP8LTreeSymbol(r, &group.red)
			if err != nil {
				return nil, stats, fmt.Errorf("VP8L red symbol at pixel %d group %d: %w", position, groupIndex, err)
			}
			blue, err := decodeVP8LTreeSymbol(r, &group.blue)
			if err != nil {
				return nil, stats, fmt.Errorf("VP8L blue symbol at pixel %d group %d: %w", position, groupIndex, err)
			}
			alpha, err := decodeVP8LTreeSymbol(r, &group.alpha)
			if err != nil {
				return nil, stats, fmt.Errorf("VP8L alpha symbol at pixel %d group %d: %w", position, groupIndex, err)
			}
			if red > 255 || blue > 255 || alpha > 255 {
				return nil, stats, fmt.Errorf("VP8L literal channel exceeds 255 at pixel %d", position)
			}
			pixel := vp8lPixel{r: uint8(red), g: uint8(greenSymbol), b: uint8(blue), a: uint8(alpha)}
			pixels[position] = pixel
			updateVP8LColorCache(colorCache, stats.ColorCacheCodeBits, pixel)
			position++
			stats.LiteralTokens++
			stats.LiteralBits += r.position() - tokenStart
		case greenSymbol < vp8lLiteralCodeCount+vp8lLengthCodeCount:
			lengthPrefix := greenSymbol - vp8lLiteralCodeCount
			length, err := readVP8LPrefixValue(r, lengthPrefix, vp8lLengthCodeCount)
			if err != nil {
				return nil, stats, fmt.Errorf("VP8L length at pixel %d: %w", position, err)
			}
			distancePrefix, err := decodeVP8LTreeSymbol(r, &group.distance)
			if err != nil {
				return nil, stats, fmt.Errorf("VP8L distance symbol at pixel %d: %w", position, err)
			}
			distanceCode, err := readVP8LPrefixValue(r, distancePrefix, vp8lDistanceCodeCount)
			if err != nil {
				return nil, stats, fmt.Errorf("VP8L distance at pixel %d: %w", position, err)
			}
			distance, err := vp8lDistanceCodeToDistance(distanceCode, width)
			if err != nil {
				return nil, stats, fmt.Errorf("VP8L distance at pixel %d: %w", position, err)
			}
			if distance > position {
				return nil, stats, fmt.Errorf("VP8L backward reference before pixel %d", position)
			}
			if position+length > len(pixels) {
				return nil, stats, fmt.Errorf("VP8L backward reference at pixel %d exceeds image", position)
			}
			for range length {
				pixel := pixels[position-distance]
				pixels[position] = pixel
				updateVP8LColorCache(colorCache, stats.ColorCacheCodeBits, pixel)
				position++
			}
			stats.CopyTokens++
			stats.CopyPixels += length
			stats.CopyBits += r.position() - tokenStart
		default:
			cacheIndex := greenSymbol - vp8lLiteralCodeCount - vp8lLengthCodeCount
			if cacheIndex < 0 || cacheIndex >= len(colorCache) {
				return nil, stats, fmt.Errorf("VP8L color cache index %d at pixel %d out of range", cacheIndex, position)
			}
			pixel := colorCache[cacheIndex]
			pixels[position] = pixel
			updateVP8LColorCache(colorCache, stats.ColorCacheCodeBits, pixel)
			position++
			stats.ColorCacheTokens++
			stats.ColorCacheTokenBits += r.position() - tokenStart
		}
	}
	return pixels, stats, nil
}

func readVP8LPrefixValue(r *vp8lBitReader, prefixCode int, prefixCount int) (int, error) {
	if prefixCode < 0 || prefixCode >= prefixCount {
		return 0, fmt.Errorf("VP8L prefix code %d exceeds %d", prefixCode, prefixCount)
	}
	if prefixCode < 4 {
		return prefixCode + 1, nil
	}
	extraBits := uint8((prefixCode - 2) >> 1)
	extra, err := r.read(extraBits)
	if err != nil {
		return 0, err
	}
	offset := (2 + prefixCode&1) << extraBits
	return offset + int(extra) + 1, nil
}

func updateVP8LColorCache(cache []vp8lPixel, bits int, pixel vp8lPixel) {
	if len(cache) == 0 {
		return
	}
	value := uint32(pixel.a)<<24 | uint32(pixel.r)<<16 | uint32(pixel.g)<<8 | uint32(pixel.b)
	index := int((0x1e35a7bd * value) >> (32 - bits))
	cache[index] = pixel
}

func vp8lDistanceCodeToDistance(distanceCode int, width int) (int, error) {
	if distanceCode > len(vp8lDistanceOffsets) {
		return distanceCode - len(vp8lDistanceOffsets), nil
	}
	if distanceCode < 1 {
		return 0, fmt.Errorf("invalid VP8L distance code %d", distanceCode)
	}
	offset := vp8lDistanceOffsets[distanceCode-1]
	distance := offset.x + offset.y*width
	if distance < 1 {
		distance = 1
	}
	return distance, nil
}

type vp8lDistanceOffset struct {
	x int
	y int
}

var vp8lDistanceOffsets = [...]vp8lDistanceOffset{
	{0, 1}, {1, 0}, {1, 1}, {-1, 1}, {0, 2}, {2, 0}, {1, 2}, {-1, 2},
	{2, 1}, {-2, 1}, {2, 2}, {-2, 2}, {0, 3}, {3, 0}, {1, 3}, {-1, 3},
	{3, 1}, {-3, 1}, {2, 3}, {-2, 3}, {3, 2}, {-3, 2}, {0, 4}, {4, 0},
	{1, 4}, {-1, 4}, {4, 1}, {-4, 1}, {3, 3}, {-3, 3}, {2, 4}, {-2, 4},
	{4, 2}, {-4, 2}, {0, 5}, {3, 4}, {-3, 4}, {4, 3}, {-4, 3}, {5, 0},
	{1, 5}, {-1, 5}, {5, 1}, {-5, 1}, {2, 5}, {-2, 5}, {5, 2}, {-5, 2},
	{4, 4}, {-4, 4}, {3, 5}, {-3, 5}, {5, 3}, {-5, 3}, {0, 6}, {6, 0},
	{1, 6}, {-1, 6}, {6, 1}, {-6, 1}, {2, 6}, {-2, 6}, {6, 2}, {-6, 2},
	{4, 5}, {-4, 5}, {5, 4}, {-5, 4}, {3, 6}, {-3, 6}, {6, 3}, {-6, 3},
	{0, 7}, {7, 0}, {1, 7}, {-1, 7}, {5, 5}, {-5, 5}, {7, 1}, {-7, 1},
	{4, 6}, {-4, 6}, {6, 4}, {-6, 4}, {2, 7}, {-2, 7}, {7, 2}, {-7, 2},
	{3, 7}, {-3, 7}, {7, 3}, {-7, 3}, {5, 6}, {-5, 6}, {6, 5}, {-6, 5},
	{8, 0}, {4, 7}, {-4, 7}, {7, 4}, {-7, 4}, {8, 1}, {8, 2}, {6, 6},
	{-6, 6}, {8, 3}, {5, 7}, {-5, 7}, {7, 5}, {-7, 5}, {8, 4}, {6, 7},
	{-6, 7}, {7, 6}, {-7, 6}, {8, 5}, {7, 7}, {-7, 7}, {8, 6}, {8, 7},
}

func divideRoundUp(value int, divisor int) int {
	return (value + divisor - 1) / divisor
}
