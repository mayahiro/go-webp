package webp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	"io"
	"math"
	"slices"
	"testing"
)

func TestMetadataPublicModes(t *testing.T) {
	metadata := &Metadata{ICCProfile: []byte{0, 128, 255}, EXIF: []byte("Exif\x00\x00"), XMP: []byte("<x/>")}
	for kind := range uint8(5) {
		for _, opaque := range []bool{false, true} {
			img := fuzzImage(kind, image.Rect(-3, 5, 14, 24), 7, opaque, []byte{7, 113, 251, 31, 67})
			for _, compression := range []Compression{CompressionLossless, CompressionLossy} {
				for mode := ModeDefault; mode <= ModeAuto; mode++ {
					t.Run(fmt.Sprintf("kind%d/opaque%v/compression%d/mode%d", kind, opaque, compression, mode), func(t *testing.T) {
						opts := &Options{Compression: compression, Mode: mode, Quality: 50}
						plain := encodeFuzzImage(t, img, opts)
						var got, cancellable bytes.Buffer
						if err := EncodeWithMetadata(&got, img, opts, metadata); err != nil {
							t.Fatal(err)
						}
						assertMetadataEncoding(t, plain, got.Bytes(), metadata, img.Bounds().Dx(), img.Bounds().Dy())
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						if err := EncodeWithMetadataContext(ctx, &cancellable, img, opts, metadata); err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(got.Bytes(), cancellable.Bytes()) {
							t.Fatal("context changed metadata output")
						}
					})
				}
			}
		}
	}
}

func TestMetadataCombinationsAndNearLossless(t *testing.T) {
	img := fuzzImage(0, image.Rect(3, 5, 68, 70), 7, false, []byte{1, 37, 89, 173, 251})
	for _, opts := range []*Options{{Mode: ModeFast}, {Mode: ModeNearLossless, Quality: 50}, {Mode: ModeNearLossless, Quality: 100}, {Mode: ModeLossyQuality, Quality: 75}} {
		plain := encodeFuzzImage(t, img, opts)
		for mask := range 8 {
			metadata := &Metadata{}
			if mask&1 != 0 {
				metadata.ICCProfile = []byte{1}
			}
			if mask&2 != 0 {
				metadata.EXIF = []byte{2, 3}
			}
			if mask&4 != 0 {
				metadata.XMP = []byte{4, 5, 6}
			}
			var got bytes.Buffer
			if err := EncodeWithMetadata(&got, img, opts, metadata); err != nil {
				t.Fatal(err)
			}
			assertMetadataEncoding(t, plain, got.Bytes(), metadata, 65, 65)
		}
	}
}

func TestMetadataEmptyAndEncoderMethods(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for _, enc := range []*Encoder{nil, {}, {Options: &Options{Mode: ModeFast}}, {Options: &Options{Mode: ModeLossyQuality}}} {
		var plain bytes.Buffer
		if err := enc.Encode(&plain, img); err != nil {
			t.Fatal(err)
		}
		for _, metadata := range []*Metadata{nil, {}, {ICCProfile: []byte{}, EXIF: []byte{}, XMP: []byte{}}, {XMP: []byte("<x/>")}} {
			var got, withContext bytes.Buffer
			if err := enc.EncodeWithMetadata(&got, img, metadata); err != nil {
				t.Fatal(err)
			}
			if err := enc.EncodeWithMetadataContext(context.Background(), &withContext, img, metadata); err != nil {
				t.Fatal(err)
			}
			assertMetadataEncoding(t, plain.Bytes(), got.Bytes(), metadata, 8, 8)
			if !bytes.Equal(got.Bytes(), withContext.Bytes()) {
				t.Fatal("Encoder context method changed output")
			}
		}
	}
}

func TestMetadataSizeLimits(t *testing.T) {
	for _, tc := range []struct {
		icc, exif, xmp uint64
		want           uint64
		fail           bool
	}{
		{0, 0, 0, 0, false},
		{1, 2, 3, 32, false},
		{math.MaxUint64, 0, 0, 0, true},
		{0, math.MaxUint32, 0, 0, true},
		{0, 0, maxWebPRIFFSize, 0, true},
		{1 << 31, 1 << 31, 0, 0, true},
		{0, 0, maxWebPRIFFSize - 30, maxWebPRIFFSize - 22, false},
	} {
		got, err := metadataChunkBytes(tc.icc, tc.exif, tc.xmp)
		if (err != nil) != tc.fail || err == nil && got != tc.want {
			t.Errorf("sizes %d/%d/%d: got %d, %v, want %d, failure %v", tc.icc, tc.exif, tc.xmp, got, err, tc.want, tc.fail)
		}
	}
	metadata, err := prepareMetadata(&Metadata{XMP: []byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, extended := range []bool{false, true} {
		extra := uint64(10)
		if !extended {
			extra += 18
		}
		if got, err := metadataRIFFSize(maxWebPRIFFSize-extra, extended, metadata); err != nil || got != maxWebPRIFFSize {
			t.Fatalf("last valid RIFF size = %d, %v", got, err)
		}
		if _, err := metadataRIFFSize(maxWebPRIFFSize-extra+2, extended, metadata); err == nil {
			t.Fatal("accepted oversized RIFF")
		}
	}
	var output bytes.Buffer
	plan := &vp8lPlan{width: 1, height: 1, payloadBits: (maxWebPRIFFSize - 12) * 8}
	if err := writeLosslessVP8LMetadata(&output, plan, metadata); err == nil || output.Len() != 0 {
		t.Fatalf("oversized output error = %v, wrote %d bytes", err, output.Len())
	}
}

func TestMetadataWriterErrors(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	metadata := &Metadata{ICCProfile: bytes.Repeat([]byte{17}, 40001), EXIF: bytes.Repeat([]byte{23}, 40002), XMP: bytes.Repeat([]byte{31}, 40003)}
	for _, opts := range []*Options{{Mode: ModeFast}, {Mode: ModeLossyQuality, Quality: 75}} {
		var full bytes.Buffer
		if err := EncodeWithMetadata(&full, img, opts, metadata); err != nil {
			t.Fatal(err)
		}
		for _, limit := range []int{0, 30, 32768, 40039, 60000, 90000, full.Len() - 1} {
			for _, buffered := range []bool{false, true} {
				for _, cancellable := range []bool{false, true} {
					var writer io.Writer = &boundedErrorWriter{remaining: limit}
					if buffered {
						writer = bufio.NewWriterSize(writer, 8192)
					}
					var err error
					if cancellable {
						ctx, cancel := context.WithCancel(context.Background())
						err = EncodeWithMetadataContext(ctx, writer, img, opts, metadata)
						cancel()
					} else {
						err = EncodeWithMetadata(writer, img, opts, metadata)
					}
					if err != errBoundedWriter {
						t.Errorf("limit %d, buffered %v, context %v: error = %v", limit, buffered, cancellable, err)
					}
				}
			}
		}
	}
}

func TestMetadataBufferedWriter(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for _, opts := range []*Options{{Mode: ModeFast}, {Mode: ModeLossyQuality}} {
		for _, size := range []int{3, 40001} {
			metadata := &Metadata{ICCProfile: bytes.Repeat([]byte{11}, size), XMP: bytes.Repeat([]byte{31}, size)}
			var plain bytes.Buffer
			if err := EncodeWithMetadata(&plain, img, opts, metadata); err != nil {
				t.Fatal(err)
			}
			for _, cancellable := range []bool{false, true} {
				var output bytes.Buffer
				writer := bufio.NewWriterSize(&output, 8192)
				var err error
				if cancellable {
					ctx, cancel := context.WithCancel(context.Background())
					err = EncodeWithMetadataContext(ctx, writer, img, opts, metadata)
					cancel()
				} else {
					err = EncodeWithMetadata(writer, img, opts, metadata)
				}
				if err != nil || writer.Buffered() != 0 || !bytes.Equal(output.Bytes(), plain.Bytes()) {
					t.Fatalf("buffered encoding: error = %v, pending bytes = %d, output size = %d, want %d", err, writer.Buffered(), output.Len(), plain.Len())
				}
			}
		}
	}
}

func TestMetadataCancellationDuringWrite(t *testing.T) {
	large := bytes.Repeat([]byte{73}, 3*metadataBlockSize)
	for _, metadata := range []*Metadata{{ICCProfile: large}, {EXIF: large}, {XMP: large}} {
		for _, writerErr := range []error{nil, errBoundedWriter} {
			ctx, cancel := context.WithCancel(context.Background())
			writes := 0
			writer := contextTestWriter(func(data []byte) (int, error) {
				writes++
				if len(data) > metadataBlockSize {
					t.Errorf("unbounded metadata write of %d bytes", len(data))
				}
				cancel()
				if writerErr != nil {
					return 0, writerErr
				}
				return len(data), nil
			})
			err := EncodeWithMetadataContext(ctx, writer, image.NewNRGBA(image.Rect(0, 0, 8, 8)), nil, metadata)
			cancel()
			want := writerErr
			if want == nil {
				want = context.Canceled
			}
			if err != want || writes != 1 {
				t.Errorf("error = %v, writes = %d, want %v and one write", err, writes, want)
			}
		}
	}
}

func TestMetadataContextStopsReading(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	img := &contextReadImage{Image: image.NewNRGBA(image.Rect(0, 0, 65, 65)), at: 100, onAt: cancel}
	var output bytes.Buffer
	metadata := &Metadata{ICCProfile: []byte{1}}
	if err := EncodeWithMetadataContext(ctx, &output, img, nil, metadata); err != context.Canceled || output.Len() != 0 || img.reads != 100 {
		t.Fatalf("error = %v, wrote %d bytes and read %d pixels", err, output.Len(), img.reads)
	}
	if err := EncodeWithMetadataContext(ctx, io.Discard, contextPanicBoundsImage{}, nil, metadata); err != context.Canceled {
		t.Fatalf("cancelled error = %v", err)
	}
	if err := EncodeWithMetadataContext(nil, io.Discard, img, nil, metadata); err == nil || err.Error() != "webp: nil context" {
		t.Fatalf("nil context error = %v", err)
	}
}

func assertMetadataEncoding(t *testing.T, plain, encoded []byte, metadata *Metadata, width, height int) {
	t.Helper()
	if metadata == nil || len(metadata.ICCProfile)+len(metadata.EXIF)+len(metadata.XMP) == 0 {
		if !bytes.Equal(plain, encoded) {
			t.Fatal("empty metadata changed the output")
		}
		return
	}
	chunks := readWebPChunks(t, encoded)
	wantNames := []string{"VP8X"}
	var flags byte
	if len(metadata.ICCProfile) != 0 {
		wantNames = append(wantNames, "ICCP")
		flags |= 0x20
	}
	wantPayloads := map[string][]byte{"ICCP": metadata.ICCProfile, "EXIF": metadata.EXIF, "XMP ": metadata.XMP}
	for _, chunk := range readWebPChunks(t, plain) {
		if chunk.name == "VP8X" {
			flags |= chunk.payload[0] & 0x10
			continue
		}
		if chunk.name == "VP8L" {
			flags |= chunk.payload[4] & 0x10
		}
		wantNames = append(wantNames, chunk.name)
		wantPayloads[chunk.name] = chunk.payload
	}
	if len(metadata.EXIF) != 0 {
		wantNames = append(wantNames, "EXIF")
		flags |= 0x08
	}
	if len(metadata.XMP) != 0 {
		wantNames = append(wantNames, "XMP ")
		flags |= 0x04
	}
	var names []string
	for _, chunk := range chunks {
		names = append(names, chunk.name)
		if chunk.name != "VP8X" && !bytes.Equal(chunk.payload, wantPayloads[chunk.name]) {
			t.Fatalf("%s payload changed", chunk.name)
		}
	}
	if !slices.Equal(names, wantNames) {
		t.Fatalf("chunks = %q, want %q", names, wantNames)
	}
	header := chunks[0].payload
	if len(header) != 10 || header[0] != flags || header[1] != 0 || header[2] != 0 || header[3] != 0 {
		t.Fatalf("VP8X header = %x, want flags %02x", header, flags)
	}
	if readUint24LE(header[4:7])+1 != width || readUint24LE(header[7:10])+1 != height {
		t.Fatalf("VP8X dimensions differ from %dx%d", width, height)
	}
}
