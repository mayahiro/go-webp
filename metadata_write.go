package webp

import (
	"bufio"
	"fmt"
)

const (
	vp8xICCFlag       = 0x20
	vp8xEXIFFlag      = 0x08
	vp8xXMPFlag       = 0x04
	maxWebPRIFFSize   = uint64(1<<32 - 10) // RFC 9649: file size is at most 2^32 - 2.
	metadataBlockSize = 32 << 10
)

type webpMetadata struct {
	Metadata
	chunkBytes uint64
	flags      byte
}

func prepareMetadata(metadata *Metadata) (*webpMetadata, error) {
	if metadata == nil || len(metadata.ICCProfile) == 0 && len(metadata.EXIF) == 0 && len(metadata.XMP) == 0 {
		return nil, nil
	}
	copy := *metadata
	size, err := metadataChunkBytes(uint64(len(copy.ICCProfile)), uint64(len(copy.EXIF)), uint64(len(copy.XMP)))
	if err != nil {
		return nil, err
	}
	result := &webpMetadata{Metadata: copy, chunkBytes: size}
	if len(copy.ICCProfile) != 0 {
		result.flags |= vp8xICCFlag
	}
	if len(copy.EXIF) != 0 {
		result.flags |= vp8xEXIFFlag
	}
	if len(copy.XMP) != 0 {
		result.flags |= vp8xXMPFlag
	}
	return result, nil
}

func metadataChunkBytes(icc, exif, xmp uint64) (uint64, error) {
	var total uint64
	limit := maxWebPRIFFSize - 4 - riffChunkSize(vp8xPayloadSize)
	for _, size := range [...]uint64{icc, exif, xmp} {
		if size == 0 {
			continue
		}
		if size > limit || riffChunkSize(size) > limit-total {
			return 0, fmt.Errorf("webp: metadata is too large")
		}
		total += riffChunkSize(size)
	}
	return total, nil
}

func metadataRIFFSize(base uint64, extended bool, metadata *webpMetadata) (uint64, error) {
	if metadata == nil {
		return base, nil
	}
	extra := metadata.chunkBytes
	if !extended {
		extra += riffChunkSize(vp8xPayloadSize)
	}
	if base > maxWebPRIFFSize || extra > maxWebPRIFFSize-base {
		return 0, fmt.Errorf("webp: encoded image is too large")
	}
	return base + extra, nil
}

func writeExtendedWebPHeader(w *bufio.Writer, width, height int, alpha bool, riffSize uint32, metadata *webpMetadata) error {
	if err := writeRIFFHeader(w, riffSize); err != nil {
		return err
	}
	var flags byte
	if alpha {
		flags |= vp8xAlphaFlag
	}
	if metadata != nil {
		flags |= metadata.flags
	}
	if err := writeVP8XChunk(w, width, height, flags); err != nil {
		return err
	}
	if metadata != nil {
		return writeMetadataChunk(w, "ICCP", metadata.ICCProfile)
	}
	return nil
}

func writeMetadataTrailer(w *bufio.Writer, metadata *webpMetadata) error {
	if metadata == nil {
		return nil
	}
	if err := writeMetadataChunk(w, "EXIF", metadata.EXIF); err != nil {
		return err
	}
	return writeMetadataChunk(w, "XMP ", metadata.XMP)
}

func writeMetadataChunk(w *bufio.Writer, kind string, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	if err := writeChunkHeader(w, kind, uint32(len(payload))); err != nil {
		return err
	}
	// Bounded writes let a cancellable writer stop between metadata blocks.
	for rest := payload; len(rest) != 0; {
		n := min(len(rest), metadataBlockSize)
		if _, err := w.Write(rest[:n]); err != nil {
			return err
		}
		rest = rest[n:]
	}
	return writeChunkPadding(w, uint64(len(payload)))
}
