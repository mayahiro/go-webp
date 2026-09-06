package webp

import (
	"bytes"
	"context"
	"testing"
)

func BenchmarkEncodeContext(b *testing.B) {
	img := newLosslessBenchmarkFixtureImage(losslessBenchmarkCase{kind: benchmarkImageUI, width: 128, height: 128})
	for _, profile := range []struct {
		name string
		opts Options
	}{
		{"LosslessFast", Options{Mode: ModeFast}},
		{"LosslessBest", Options{Mode: ModeBestCompression}},
		{"LossyDefault", Options{Compression: CompressionLossy, Quality: 75}},
		{"LossyBest", Options{Compression: CompressionLossy, Mode: ModeBestCompression, Quality: 75}},
	} {
		b.Run(profile.name, func(b *testing.B) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			for _, api := range []string{"Encode", "Background", "Cancellable"} {
				b.Run(api, func(b *testing.B) {
					var output bytes.Buffer
					b.ReportAllocs()
					for b.Loop() {
						output.Reset()
						var err error
						switch api {
						case "Encode":
							err = Encode(&output, img, &profile.opts)
						case "Background":
							err = EncodeContext(context.Background(), &output, img, &profile.opts)
						case "Cancellable":
							err = EncodeContext(ctx, &output, img, &profile.opts)
						}
						if err != nil {
							b.Fatal(err)
						}
					}
					b.ReportMetric(float64(output.Len()), "encoded_B")
				})
			}
		})
	}
}
