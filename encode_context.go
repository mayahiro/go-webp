package webp

import (
	"bufio"
	"context"
	"errors"
	"image"
	"image/color"
	"io"
)

// EncodeContext writes m to w like Encode, with cooperative cancellation.
// It returns ctx.Err() when cancellation interrupts encoding. Cancellation can
// leave a partial image in w. Write errors from w are returned unchanged.
// A nil context returns an error.
//
// Cancellation is checked during image processing and before and after writes.
// It cannot interrupt a blocked image method or Write call. All encoding workers
// finish before EncodeContext returns.
func EncodeContext(ctx context.Context, w io.Writer, m image.Image, o *Options) error {
	return encodeContext(ctx, w, m, o, nil)
}

func encodeContext(ctx context.Context, w io.Writer, m image.Image, o *Options, metadata *Metadata) error {
	if ctx == nil {
		return errors.New("webp: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if ctx.Done() == nil {
		return EncodeWithMetadata(w, m, o, metadata)
	}
	cancel := &encodeCancellation{done: ctx.Done()}
	var err error
	if !cancel.run(func() {
		err = encodeWithMetadata(w, m, o, cancel, metadata)
		if err == nil {
			cancel.check()
			// The cancellation wrapper hides an existing buffer from the codec.
			// Preserve Encode's flush behavior when NewWriter would reuse it.
			if buffered, ok := w.(*bufio.Writer); ok && bufio.NewWriter(buffered) == buffered {
				err = buffered.Flush()
			}
			if err == nil {
				cancel.check()
			}
		}
	}) {
		return ctx.Err()
	}
	return err
}

// EncodeContext writes m to w with the encoder's options and cooperative
// cancellation. It has the same cancellation behavior as EncodeContext.
func (enc *Encoder) EncodeContext(ctx context.Context, w io.Writer, m image.Image) error {
	if enc == nil {
		return EncodeContext(ctx, w, m, nil)
	}
	return EncodeContext(ctx, w, m, enc.Options)
}

// A private per-call value unwinds callbacks that cannot return errors.
// Only this exact value is recovered at the call and worker boundaries.
type encodeCancellation struct {
	done <-chan struct{}
}

func (c *encodeCancellation) check() {
	if c == nil {
		return
	}
	select {
	case <-c.done:
		panic(c)
	default:
	}
}

func (c *encodeCancellation) run(fn func()) (completed bool) {
	if c == nil {
		fn()
		return true
	}
	cancelled := false
	func() {
		defer func() {
			if value := recover(); value == c {
				cancelled = true
			} else if value != nil {
				panic(value)
			}
		}()
		fn()
		completed = true
	}()
	// Preserve panic(nil) with GODEBUG=panicnil=1. Goexit never reaches here.
	if !completed && !cancelled {
		panic(nil)
	}
	return completed
}

func (c *encodeCancellation) pixels(read pixelReader) pixelReader {
	if c == nil {
		return read
	}
	return func(x, y int) color.NRGBA {
		c.check()
		return read(x, y)
	}
}

func (c *encodeCancellation) luma(read lumaReader) lumaReader {
	if c == nil {
		return read
	}
	return func(x, y int) uint8 {
		c.check()
		return read(x, y)
	}
}

func (c *encodeCancellation) chroma(read chromaReader) chromaReader {
	if c == nil {
		return read
	}
	return func(x, y int) (uint8, uint8) {
		c.check()
		return read(x, y)
	}
}

type cancellingWriter struct {
	writer io.Writer
	cancel *encodeCancellation
}

func (w cancellingWriter) Write(data []byte) (int, error) {
	w.cancel.check()
	n, err := w.writer.Write(data)
	if err == nil {
		w.cancel.check()
	}
	return n, err
}
