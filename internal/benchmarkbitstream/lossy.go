// Package benchmarkbitstream inspects encoded WebP structure for development benchmarks
package benchmarkbitstream

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// LossyLayout records the byte layout of a VP8 lossy WebP bitstream
type LossyLayout struct {
	FileBytes              int `json:"file_bytes"`
	ContainerAndOtherBytes int `json:"container_and_other_bytes"`
	VP8PayloadBytes        int `json:"vp8_payload_bytes"`
	FrameHeaderBytes       int `json:"frame_header_bytes"`
	FirstPartitionBytes    int `json:"first_partition_bytes"`
	ResidualPartitionBytes int `json:"residual_partition_bytes"`
}

// ParseLossy returns the byte layout of the first VP8 chunk in data
func ParseLossy(data []byte) (LossyLayout, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return LossyLayout{}, errors.New("invalid WebP RIFF header")
	}
	riffEnd := int(binary.LittleEndian.Uint32(data[4:8])) + 8
	if riffEnd < 12 || riffEnd > len(data) {
		return LossyLayout{}, errors.New("invalid WebP RIFF size")
	}
	for offset := 12; offset+8 <= riffEnd; {
		payloadBytes := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		payloadStart := offset + 8
		payloadEnd := payloadStart + payloadBytes
		if payloadBytes < 0 || payloadEnd < payloadStart || payloadEnd > riffEnd {
			return LossyLayout{}, errors.New("invalid WebP chunk size")
		}
		if string(data[offset:offset+4]) == "VP8 " {
			return parseVP8Payload(data[payloadStart:payloadEnd], len(data))
		}
		offset = payloadEnd + payloadBytes&1
	}
	return LossyLayout{}, errors.New("WebP contains no VP8 chunk")
}

func parseVP8Payload(payload []byte, fileBytes int) (LossyLayout, error) {
	const frameHeaderBytes = 10
	if len(payload) < frameHeaderBytes {
		return LossyLayout{}, errors.New("VP8 payload is shorter than its frame header")
	}
	tag := uint32(payload[0]) | uint32(payload[1])<<8 | uint32(payload[2])<<16
	firstPartitionBytes := int(tag >> 5)
	if firstPartitionBytes > len(payload)-frameHeaderBytes {
		return LossyLayout{}, fmt.Errorf("VP8 first partition = %d bytes, payload remainder = %d", firstPartitionBytes, len(payload)-frameHeaderBytes)
	}
	return LossyLayout{
		FileBytes:              fileBytes,
		ContainerAndOtherBytes: fileBytes - len(payload),
		VP8PayloadBytes:        len(payload),
		FrameHeaderBytes:       frameHeaderBytes,
		FirstPartitionBytes:    firstPartitionBytes,
		ResidualPartitionBytes: len(payload) - frameHeaderBytes - firstPartitionBytes,
	}, nil
}

// Add accumulates another layout into layout
func (layout *LossyLayout) Add(other LossyLayout) {
	layout.FileBytes += other.FileBytes
	layout.ContainerAndOtherBytes += other.ContainerAndOtherBytes
	layout.VP8PayloadBytes += other.VP8PayloadBytes
	layout.FrameHeaderBytes += other.FrameHeaderBytes
	layout.FirstPartitionBytes += other.FirstPartitionBytes
	layout.ResidualPartitionBytes += other.ResidualPartitionBytes
}
