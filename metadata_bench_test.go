package webp

import (
	"bytes"
	"testing"
)

func BenchmarkEncodeMetadata(b *testing.B) {
	img := newLosslessBenchmarkFixtureImage(losslessBenchmarkCase{kind: benchmarkImageUI, width: 128, height: 128})
	for _, profile := range []struct {
		name string
		opts Options
	}{
		{"LosslessFast", Options{Mode: ModeFast}},
		{"LossyDefault", Options{Compression: CompressionLossy, Quality: 75}},
	} {
		b.Run(profile.name, func(b *testing.B) {
			for _, tc := range []struct {
				name     string
				metadata *Metadata
			}{
				{"Nil", nil},
				{"Empty", &Metadata{}},
				{"Small", &Metadata{ICCProfile: make([]byte, 512), EXIF: make([]byte, 256), XMP: make([]byte, 255)}},
				{"Large", &Metadata{XMP: make([]byte, 64<<10)}},
			} {
				b.Run(tc.name, func(b *testing.B) {
					var output bytes.Buffer
					b.ReportAllocs()
					for b.Loop() {
						output.Reset()
						if err := EncodeWithMetadata(&output, img, &profile.opts, tc.metadata); err != nil {
							b.Fatal(err)
						}
					}
					b.ReportMetric(float64(output.Len()), "encoded_B")
				})
			}
		})
	}
}
