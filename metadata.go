package webp

import (
	"context"
	"image"
	"io"
)

// Metadata contains optional WebP metadata chunk payloads.
// Supply payloads without RIFF chunk headers or padding; the encoder writes
// them verbatim inside the corresponding chunks.
// A nil or empty slice omits that chunk. The encoder does not validate the
// payload formats, apply color profiles or orientation, or extract metadata
// from image.Image. Callers must supply metadata appropriate for the pixels
// being encoded and must not modify the payloads until encoding returns.
type Metadata struct {
	// ICCProfile is the ICC color profile stored in the ICCP chunk.
	ICCProfile []byte
	// EXIF is the Exif metadata stored in the EXIF chunk.
	EXIF []byte
	// XMP is the XMP metadata stored in the XMP chunk.
	XMP []byte
}

// EncodeWithMetadata writes m to w like Encode, with the supplied metadata.
// Nil or empty metadata produces the same bytes as Encode. Nonempty metadata
// uses the extended WebP container without changing the image bitstream.
// Write errors are returned unchanged, and errors may leave partial output.
func EncodeWithMetadata(w io.Writer, m image.Image, o *Options, metadata *Metadata) error {
	return encodeWithMetadata(w, m, o, nil, metadata)
}

// EncodeWithMetadataContext writes m and metadata to w with cooperative
// cancellation. It has the cancellation and error behavior of EncodeContext
// and the metadata behavior of EncodeWithMetadata.
func EncodeWithMetadataContext(ctx context.Context, w io.Writer, m image.Image, o *Options, metadata *Metadata) error {
	return encodeContext(ctx, w, m, o, metadata)
}

// EncodeWithMetadata writes m and metadata to w using the encoder's options.
// It has the same metadata and error behavior as EncodeWithMetadata.
func (enc *Encoder) EncodeWithMetadata(w io.Writer, m image.Image, metadata *Metadata) error {
	if enc == nil {
		return EncodeWithMetadata(w, m, nil, metadata)
	}
	return EncodeWithMetadata(w, m, enc.Options, metadata)
}

// EncodeWithMetadataContext writes m and metadata to w using the encoder's
// options and cooperative cancellation. It has the same behavior as
// EncodeWithMetadataContext.
func (enc *Encoder) EncodeWithMetadataContext(ctx context.Context, w io.Writer, m image.Image, metadata *Metadata) error {
	if enc == nil {
		return EncodeWithMetadataContext(ctx, w, m, nil, metadata)
	}
	return EncodeWithMetadataContext(ctx, w, m, enc.Options, metadata)
}
