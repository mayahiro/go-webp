package webp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestEncodeContextMatchesEncode(t *testing.T) {
	for kind := range uint8(5) {
		for _, size := range []image.Point{{X: 17, Y: 19}, {X: 65, Y: 63}} {
			img := fuzzImage(kind, image.Rect(-3, 5, size.X-3, size.Y+5), 7, false, []byte{7, 113, 251, 31, 67})
			for _, compression := range []Compression{CompressionLossless, CompressionLossy} {
				for mode := ModeDefault; mode <= ModeAuto; mode++ {
					t.Run(fmt.Sprintf("kind%d/%dx%d/compression%d/mode%d", kind, size.X, size.Y, compression, mode), func(t *testing.T) {
						opts := &Options{Compression: compression, Mode: mode, Quality: 75}
						var want, got bytes.Buffer
						if err := Encode(&want, img, opts); err != nil {
							t.Fatal(err)
						}
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						if err := EncodeContext(ctx, &got, img, opts); err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(got.Bytes(), want.Bytes()) {
							t.Fatal("cancellable encoding changed the output")
						}
					})
				}
			}
		}
	}
}

func TestEncodeContextStopsReading(t *testing.T) {
	for _, compression := range []Compression{CompressionLossless, CompressionLossy} {
		for mode := ModeDefault; mode <= ModeAuto; mode++ {
			t.Run(fmt.Sprintf("compression%d/mode%d", compression, mode), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				img := &contextReadImage{
					Image: image.NewNRGBA(image.Rect(-3, 5, 62, 70)),
					at:    100,
					onAt:  cancel,
				}
				var output bytes.Buffer
				err := EncodeContext(ctx, &output, img, &Options{Compression: compression, Mode: mode, Quality: 50})
				if err != context.Canceled {
					t.Fatalf("error = %v, want context.Canceled", err)
				}
				if img.reads != img.at || output.Len() != 0 {
					t.Fatalf("read %d pixels and wrote %d bytes after cancellation at pixel %d", img.reads, output.Len(), img.at)
				}
			})
		}
	}
}

func TestEncodeContextParallelOutput(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageAlpha, width: 256, height: 256})
	for _, compression := range []Compression{CompressionLossless, CompressionLossy} {
		for _, mode := range []Mode{ModeDefault, ModeBestCompression} {
			t.Run(fmt.Sprintf("compression%d/mode%d", compression, mode), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				opts := &Options{Compression: compression, Mode: mode, Quality: 75}
				var want, got bytes.Buffer
				if err := Encode(&want, img, opts); err != nil {
					t.Fatal(err)
				}
				if err := EncodeContext(ctx, &got, img, opts); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(want.Bytes(), got.Bytes()) {
					t.Fatal("cancellable parallel encoding changed the output")
				}
			})
		}
	}
}

func TestEncodeContextStopsAfterMaterialization(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	img := &contextReadImage{Image: image.NewNRGBA(image.Rect(0, 0, 65, 65)), at: 65 * 65, onAt: cancel}
	var output bytes.Buffer
	if err := EncodeContext(ctx, &output, img, &Options{Mode: ModeBestCompression}); err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if output.Len() != 0 || img.reads != img.at {
		t.Fatalf("read %d pixels and wrote %d bytes", img.reads, output.Len())
	}
}

func TestEncodeContextErrors(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("request stopped")
	cancel(cause)
	if err := EncodeContext(ctx, io.Discard, contextPanicBoundsImage{}, nil); err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	deadline, stop := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer stop()
	if err := EncodeContext(deadline, io.Discard, contextPanicBoundsImage{}, nil); err != context.DeadlineExceeded {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if err := EncodeContext(nil, io.Discard, nil, nil); err == nil || err.Error() != "webp: nil context" {
		t.Fatalf("nil context error = %v", err)
	}
	live, stopLive := context.WithCancel(context.Background())
	defer stopLive()
	if err := EncodeContext(live, nil, nil, nil); err == nil || err.Error() != "webp: nil writer" {
		t.Fatalf("nil writer error = %v", err)
	}
	if err := EncodeContext(live, io.Discard, nil, nil); err == nil || err.Error() != "webp: nil image" {
		t.Fatalf("nil image error = %v", err)
	}
}

func TestEncodeContextPreservesUserPanics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	img := &contextReadImage{Image: image.NewNRGBA(image.Rect(0, 0, 1, 1)), at: 1, onAt: func() { panic(context.Canceled) }}
	defer func() {
		if value := recover(); value != context.Canceled {
			t.Errorf("panic = %v, want the original user panic", value)
		}
	}()
	EncodeContext(ctx, io.Discard, img, nil)
	t.Fatal("user panic was swallowed")
}

func TestEncodeContextCancellationDuringWrite(t *testing.T) {
	for _, buffered := range []bool{false, true} {
		for _, writerErr := range []error{nil, errBoundedWriter} {
			ctx, cancel := context.WithCancel(context.Background())
			writes := 0
			var writer io.Writer = contextTestWriter(func(data []byte) (int, error) {
				writes++
				cancel()
				if writerErr != nil {
					return 0, writerErr
				}
				return len(data), nil
			})
			if buffered {
				writer = bufio.NewWriterSize(writer, 8192)
			}
			err := EncodeContext(ctx, writer, image.NewNRGBA(image.Rect(0, 0, 8, 8)), nil)
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

func TestEncodeContextBufferedWriter(t *testing.T) {
	for _, size := range []int{1024, 4096, 8192} {
		for _, writeErr := range []error{nil, errBoundedWriter} {
			t.Run(fmt.Sprintf("size%d/error%v", size, writeErr), func(t *testing.T) {
				img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
				var want, got bytes.Buffer
				writer := func(output *bytes.Buffer) *bufio.Writer {
					return bufio.NewWriterSize(contextTestWriter(func(data []byte) (int, error) {
						if writeErr != nil {
							return 0, writeErr
						}
						return output.Write(data)
					}), size)
				}
				ordinary, cancellable := writer(&want), writer(&got)
				if _, err := ordinary.WriteString("prefix"); err != nil {
					t.Fatal(err)
				}
				if _, err := cancellable.WriteString("prefix"); err != nil {
					t.Fatal(err)
				}
				wantErr := Encode(ordinary, img, nil)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				gotErr := EncodeContext(ctx, cancellable, img, nil)
				if gotErr != wantErr || !bytes.Equal(want.Bytes(), got.Bytes()) || ordinary.Buffered() != cancellable.Buffered() {
					t.Fatalf("error = %v, want %v; buffered = %d, want %d; written bytes = %d, want %d", gotErr, wantErr, cancellable.Buffered(), ordinary.Buffered(), got.Len(), want.Len())
				}
			})
		}
	}
}

func TestEncodeContextPreservesNilPanic(t *testing.T) {
	for _, setting := range []string{"panicnil=0", "panicnil=1"} {
		t.Run(setting, func(t *testing.T) {
			t.Setenv("GODEBUG", setting)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			img := &contextReadImage{Image: image.NewNRGBA(image.Rect(0, 0, 1, 1)), at: 1, onAt: func() { panic(nil) }}
			returned := false
			func() {
				defer func() { recover() }()
				EncodeContext(ctx, io.Discard, img, nil)
				returned = true
			}()
			if returned {
				t.Fatal("panic(nil) was swallowed")
			}
		})
	}
}

func TestEncodeContextPreservesGoexit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	img := &contextReadImage{Image: image.NewNRGBA(image.Rect(0, 0, 1, 1)), at: 1, onAt: runtime.Goexit}
	done := make(chan struct{})
	go func() {
		defer close(done)
		EncodeContext(ctx, io.Discard, img, nil)
		t.Error("Goexit returned")
	}()
	<-done
}

func TestEncoderEncodeContext(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for _, enc := range []*Encoder{nil, {}, {Options: &Options{Mode: ModeFast}}} {
		var want, got bytes.Buffer
		if err := enc.Encode(&want, img); err != nil {
			t.Fatal(err)
		}
		if err := enc.EncodeContext(context.Background(), &got, img); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want.Bytes(), got.Bytes()) {
			t.Fatal("Encoder.EncodeContext changed the output")
		}
	}
}

func TestVP8LCancellationWaitsForWorkers(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("requires two workers")
	}
	done := make(chan struct{})
	cancel := &encodeCancellation{done: done}
	budget := vp8lBudgetForMode(ModeBestCompression)
	budget.cancel, budget.maxWorkers, budget.maxParallelBytes = cancel, 2, ^uint64(0)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var running atomic.Int32
	candidate := vp8lTransformCandidate{width: 256, height: 256, materialize: func() []uint32 {
		running.Add(1)
		defer running.Add(-1)
		started <- struct{}{}
		<-release
		cancel.check()
		return nil
	}}
	result := make(chan bool, 1)
	go func() {
		result <- cancel.run(func() {
			vp8lBuildFinalistPlans(256, 256, false, []vp8lTransformCandidate{candidate, candidate}, budget, &vp8lSearchWorkspace{cancel: cancel})
		})
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for range 2 {
		select {
		case <-started:
		case <-timer.C:
			close(done)
			close(release)
			t.Fatal("workers did not start")
		}
	}
	close(done)
	close(release)
	select {
	case completed := <-result:
		if completed || running.Load() != 0 {
			t.Fatalf("completed = %v, running workers = %d", completed, running.Load())
		}
	case <-timer.C:
		t.Fatal("cancelled workers did not finish")
	}
}

func TestVP8LCancellationInterruptsOptimalParsing(t *testing.T) {
	done := make(chan struct{})
	cancel := &encodeCancellation{done: done}
	pixels := make([]uint32, 5000)
	graph := vp8lMatchGraph{starts: make([]uint32, len(pixels))}
	group, _ := vp8lLiteralCodeGroupAndDataBits(pixels)
	visited := 0
	completed := cancel.run(func() {
		vp8lOptimalTokensWithGroups(pixels, graph, nil, func(int) *vp8lCodeGroup {
			visited++
			if visited == 100 {
				close(done)
			}
			return &group
		}, &vp8lSearchWorkspace{cancel: cancel})
	})
	if completed || visited < 100 || visited > 4096 {
		t.Fatalf("completed = %v, visited = %d", completed, visited)
	}
}

func TestVP8LCancellationStopsBetweenRows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := newEncoderSource(image.NewNRGBA(image.Rect(0, 0, 65, 2)))
	source.cancel = &encodeCancellation{done: ctx.Done()}
	reads := 0
	rows := newVP8LSource(source, func(int, int) color.NRGBA {
		reads++
		if reads == source.width {
			cancel()
		}
		return color.NRGBA{}
	})
	completed := source.cancel.run(func() {
		if _, _, err := rows.materialize(vp8lMaxSourceBytes); err != nil {
			t.Fatal(err)
		}
	})
	if completed || reads != source.width {
		t.Fatalf("completed = %v, read %d pixels after cancellation at the end of the first row", completed, reads)
	}
}

type contextReadImage struct {
	image.Image
	reads int
	at    int
	onAt  func()
}

func (m *contextReadImage) At(x, y int) color.Color {
	m.reads++
	if m.reads == m.at {
		m.onAt()
	}
	return m.Image.At(x, y)
}

type contextPanicBoundsImage struct{ image.Image }

func (contextPanicBoundsImage) Bounds() image.Rectangle { panic("unexpected image read") }

type contextTestWriter func([]byte) (int, error)

func (w contextTestWriter) Write(data []byte) (int, error) { return w(data) }
