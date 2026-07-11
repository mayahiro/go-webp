// Package benchmarkbitstream inspects encoded WebP structure for development benchmarks
package benchmarkbitstream

import (
	"encoding/binary"
	"fmt"
)

// LosslessLayout records the bit layout and token mix of a VP8L WebP bitstream
type LosslessLayout struct {
	FileBytes               int    `json:"file_bytes"`
	ContainerAndOtherBytes  int    `json:"container_and_other_bytes"`
	VP8LPayloadBytes        int    `json:"vp8l_payload_bytes"`
	Width                   int    `json:"width"`
	Height                  int    `json:"height"`
	CodedWidth              int    `json:"coded_width"`
	AlphaHint               bool   `json:"alpha_hint"`
	ImageHeaderBits         uint64 `json:"image_header_bits"`
	TransformHeaderBits     uint64 `json:"transform_header_bits"`
	TransformDataBits       uint64 `json:"transform_data_bits"`
	ColorCacheHeaderBits    uint64 `json:"color_cache_header_bits"`
	MetaPrefixHeaderBits    uint64 `json:"meta_prefix_header_bits"`
	MetaPrefixImageBits     uint64 `json:"meta_prefix_image_bits"`
	HuffmanTreeBits         uint64 `json:"huffman_tree_bits"`
	LiteralBits             uint64 `json:"literal_bits"`
	ColorCacheTokenBits     uint64 `json:"color_cache_token_bits"`
	CopyBits                uint64 `json:"copy_bits"`
	PaddingBits             uint64 `json:"padding_bits"`
	TransformCount          int    `json:"transform_count"`
	PredictorTransforms     int    `json:"predictor_transforms"`
	PredictorTransformBits  uint64 `json:"predictor_transform_bits"`
	ColorTransforms         int    `json:"color_transforms"`
	ColorTransformBits      uint64 `json:"color_transform_bits"`
	SubtractGreenTransforms int    `json:"subtract_green_transforms"`
	SubtractGreenBits       uint64 `json:"subtract_green_bits"`
	ColorIndexingTransforms int    `json:"color_indexing_transforms"`
	ColorIndexingBits       uint64 `json:"color_indexing_bits"`
	ColorCacheCodeBits      int    `json:"color_cache_code_bits"`
	PrefixBits              int    `json:"prefix_bits"`
	EntropyGroups           int    `json:"entropy_groups"`
	LiteralTokens           int    `json:"literal_tokens"`
	ColorCacheTokens        int    `json:"color_cache_tokens"`
	CopyTokens              int    `json:"copy_tokens"`
	CopyPixels              int    `json:"copy_pixels"`
	CodedPixels             int    `json:"coded_pixels"`
}

// ParseLossless returns the bit layout of the first VP8L chunk in data
func ParseLossless(data []byte) (LosslessLayout, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return LosslessLayout{}, fmt.Errorf("invalid WebP RIFF header")
	}
	riffEnd := int(binary.LittleEndian.Uint32(data[4:8])) + 8
	if riffEnd < 12 || riffEnd > len(data) {
		return LosslessLayout{}, fmt.Errorf("invalid WebP RIFF size")
	}
	for offset := 12; offset+8 <= riffEnd; {
		payloadBytes := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		payloadStart := offset + 8
		payloadEnd := payloadStart + payloadBytes
		if payloadBytes < 0 || payloadEnd < payloadStart || payloadEnd > riffEnd {
			return LosslessLayout{}, fmt.Errorf("invalid WebP chunk size")
		}
		if string(data[offset:offset+4]) == "VP8L" {
			return parseVP8LPayload(data[payloadStart:payloadEnd], len(data))
		}
		offset = payloadEnd + payloadBytes&1
	}
	return LosslessLayout{}, fmt.Errorf("WebP contains no VP8L chunk")
}

func parseVP8LPayload(payload []byte, fileBytes int) (LosslessLayout, error) {
	layout := LosslessLayout{
		FileBytes:              fileBytes,
		ContainerAndOtherBytes: fileBytes - len(payload),
		VP8LPayloadBytes:       len(payload),
	}
	r := vp8lBitReader{data: payload}
	headerStart := r.position()
	signature, err := r.read(8)
	if err != nil {
		return LosslessLayout{}, err
	}
	if signature != 0x2f {
		return LosslessLayout{}, fmt.Errorf("invalid VP8L signature %#x", signature)
	}
	widthMinusOne, err := r.read(14)
	if err != nil {
		return LosslessLayout{}, err
	}
	heightMinusOne, err := r.read(14)
	if err != nil {
		return LosslessLayout{}, err
	}
	alphaHint, err := r.read(1)
	if err != nil {
		return LosslessLayout{}, err
	}
	version, err := r.read(3)
	if err != nil {
		return LosslessLayout{}, err
	}
	if version != 0 {
		return LosslessLayout{}, fmt.Errorf("unsupported VP8L version %d", version)
	}
	layout.ImageHeaderBits = r.position() - headerStart
	layout.Width = int(widthMinusOne) + 1
	layout.Height = int(heightMinusOne) + 1
	layout.AlphaHint = alphaHint != 0

	currentWidth := layout.Width
	var seenTransforms [4]bool
	for {
		transformStart := r.position()
		present, err := r.read(1)
		if err != nil {
			return LosslessLayout{}, err
		}
		if present == 0 {
			layout.TransformHeaderBits += r.position() - transformStart
			break
		}
		transformType, err := r.read(2)
		if err != nil {
			return LosslessLayout{}, err
		}
		if seenTransforms[transformType] {
			return LosslessLayout{}, fmt.Errorf("duplicate VP8L transform %d", transformType)
		}
		seenTransforms[transformType] = true
		layout.TransformCount++
		switch transformType {
		case 0:
			layout.PredictorTransforms++
			sizeBitsMinusTwo, err := r.read(3)
			if err != nil {
				return LosslessLayout{}, err
			}
			layout.TransformHeaderBits += r.position() - transformStart
			sizeBits := int(sizeBitsMinusTwo) + 2
			transformWidth := divideRoundUp(currentWidth, 1<<sizeBits)
			transformHeight := divideRoundUp(layout.Height, 1<<sizeBits)
			dataStart := r.position()
			if _, _, err := decodeVP8LImageData(&r, transformWidth, transformHeight, false); err != nil {
				return LosslessLayout{}, fmt.Errorf("VP8L predictor transform: %w", err)
			}
			layout.TransformDataBits += r.position() - dataStart
			layout.PredictorTransformBits += r.position() - transformStart
		case 1:
			layout.ColorTransforms++
			sizeBitsMinusTwo, err := r.read(3)
			if err != nil {
				return LosslessLayout{}, err
			}
			layout.TransformHeaderBits += r.position() - transformStart
			sizeBits := int(sizeBitsMinusTwo) + 2
			transformWidth := divideRoundUp(currentWidth, 1<<sizeBits)
			transformHeight := divideRoundUp(layout.Height, 1<<sizeBits)
			dataStart := r.position()
			if _, _, err := decodeVP8LImageData(&r, transformWidth, transformHeight, false); err != nil {
				return LosslessLayout{}, fmt.Errorf("VP8L color transform: %w", err)
			}
			layout.TransformDataBits += r.position() - dataStart
			layout.ColorTransformBits += r.position() - transformStart
		case 2:
			layout.SubtractGreenTransforms++
			layout.TransformHeaderBits += r.position() - transformStart
			layout.SubtractGreenBits += r.position() - transformStart
		case 3:
			layout.ColorIndexingTransforms++
			colorTableSizeMinusOne, err := r.read(8)
			if err != nil {
				return LosslessLayout{}, err
			}
			layout.TransformHeaderBits += r.position() - transformStart
			colorTableSize := int(colorTableSizeMinusOne) + 1
			dataStart := r.position()
			if _, _, err := decodeVP8LImageData(&r, colorTableSize, 1, false); err != nil {
				return LosslessLayout{}, fmt.Errorf("VP8L color indexing transform: %w", err)
			}
			layout.TransformDataBits += r.position() - dataStart
			layout.ColorIndexingBits += r.position() - transformStart
			currentWidth = divideRoundUp(currentWidth, 1<<colorIndexWidthBits(colorTableSize))
		default:
			return LosslessLayout{}, fmt.Errorf("invalid VP8L transform %d", transformType)
		}
	}

	layout.CodedWidth = currentWidth
	_, stats, err := decodeVP8LImageData(&r, currentWidth, layout.Height, true)
	if err != nil {
		return LosslessLayout{}, fmt.Errorf("VP8L main image: %w", err)
	}
	layout.ColorCacheHeaderBits = stats.ColorCacheHeaderBits
	layout.MetaPrefixHeaderBits = stats.MetaPrefixHeaderBits
	layout.MetaPrefixImageBits = stats.MetaPrefixImageBits
	layout.HuffmanTreeBits = stats.HuffmanTreeBits
	layout.LiteralBits = stats.LiteralBits
	layout.ColorCacheTokenBits = stats.ColorCacheTokenBits
	layout.CopyBits = stats.CopyBits
	layout.ColorCacheCodeBits = stats.ColorCacheCodeBits
	layout.PrefixBits = stats.PrefixBits
	layout.EntropyGroups = stats.EntropyGroups
	layout.LiteralTokens = stats.LiteralTokens
	layout.ColorCacheTokens = stats.ColorCacheTokens
	layout.CopyTokens = stats.CopyTokens
	layout.CopyPixels = stats.CopyPixels
	layout.CodedPixels = stats.CodedPixels

	payloadBits := uint64(len(payload)) * 8
	consumedBits := r.position()
	if consumedBits > payloadBits {
		return LosslessLayout{}, fmt.Errorf("VP8L consumed %d bits from %d-bit payload", consumedBits, payloadBits)
	}
	layout.PaddingBits = payloadBits - consumedBits
	if layout.PaddingBits > 7 {
		return LosslessLayout{}, fmt.Errorf("VP8L has %d unconsumed bits", layout.PaddingBits)
	}
	if layout.ClassifiedBits() != payloadBits {
		return LosslessLayout{}, fmt.Errorf("VP8L classified %d of %d payload bits", layout.ClassifiedBits(), payloadBits)
	}
	return layout, nil
}

// ClassifiedBits returns the VP8L payload bits assigned to layout categories
func (layout LosslessLayout) ClassifiedBits() uint64 {
	return layout.ImageHeaderBits +
		layout.TransformHeaderBits +
		layout.TransformDataBits +
		layout.ColorCacheHeaderBits +
		layout.MetaPrefixHeaderBits +
		layout.MetaPrefixImageBits +
		layout.HuffmanTreeBits +
		layout.LiteralBits +
		layout.ColorCacheTokenBits +
		layout.CopyBits +
		layout.PaddingBits
}

// Add accumulates another layout into layout
func (layout *LosslessLayout) Add(other LosslessLayout) {
	layout.FileBytes += other.FileBytes
	layout.ContainerAndOtherBytes += other.ContainerAndOtherBytes
	layout.VP8LPayloadBytes += other.VP8LPayloadBytes
	layout.Width += other.Width
	layout.Height += other.Height
	layout.CodedWidth += other.CodedWidth
	layout.AlphaHint = layout.AlphaHint || other.AlphaHint
	layout.ImageHeaderBits += other.ImageHeaderBits
	layout.TransformHeaderBits += other.TransformHeaderBits
	layout.TransformDataBits += other.TransformDataBits
	layout.ColorCacheHeaderBits += other.ColorCacheHeaderBits
	layout.MetaPrefixHeaderBits += other.MetaPrefixHeaderBits
	layout.MetaPrefixImageBits += other.MetaPrefixImageBits
	layout.HuffmanTreeBits += other.HuffmanTreeBits
	layout.LiteralBits += other.LiteralBits
	layout.ColorCacheTokenBits += other.ColorCacheTokenBits
	layout.CopyBits += other.CopyBits
	layout.PaddingBits += other.PaddingBits
	layout.TransformCount += other.TransformCount
	layout.PredictorTransforms += other.PredictorTransforms
	layout.PredictorTransformBits += other.PredictorTransformBits
	layout.ColorTransforms += other.ColorTransforms
	layout.ColorTransformBits += other.ColorTransformBits
	layout.SubtractGreenTransforms += other.SubtractGreenTransforms
	layout.SubtractGreenBits += other.SubtractGreenBits
	layout.ColorIndexingTransforms += other.ColorIndexingTransforms
	layout.ColorIndexingBits += other.ColorIndexingBits
	layout.ColorCacheCodeBits += other.ColorCacheCodeBits
	layout.PrefixBits += other.PrefixBits
	layout.EntropyGroups += other.EntropyGroups
	layout.LiteralTokens += other.LiteralTokens
	layout.ColorCacheTokens += other.ColorCacheTokens
	layout.CopyTokens += other.CopyTokens
	layout.CopyPixels += other.CopyPixels
	layout.CodedPixels += other.CodedPixels
}

func colorIndexWidthBits(colorTableSize int) int {
	switch {
	case colorTableSize <= 2:
		return 3
	case colorTableSize <= 4:
		return 2
	case colorTableSize <= 16:
		return 1
	default:
		return 0
	}
}
