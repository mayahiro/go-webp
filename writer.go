package webp

import "bufio"

type bitWriter struct {
	w     *bufio.Writer
	bits  uint64
	nBits uint8
	err   error
}

func newBitWriter(w *bufio.Writer) *bitWriter {
	return &bitWriter{w: w}
}

func (w *bitWriter) writeBits(value uint32, n uint8) {
	if w.err != nil {
		return
	}
	if n == 0 {
		return
	}
	w.bits |= uint64(value&((1<<n)-1)) << w.nBits
	w.nBits += n
	for w.nBits >= 8 {
		w.err = w.w.WriteByte(byte(w.bits))
		if w.err != nil {
			return
		}
		w.bits >>= 8
		w.nBits -= 8
	}
}

func (w *bitWriter) flush() error {
	if w.err != nil {
		return w.err
	}
	if w.nBits > 0 {
		if err := w.w.WriteByte(byte(w.bits)); err != nil {
			return err
		}
		w.bits = 0
		w.nBits = 0
	}
	return nil
}

func reverse8(v uint8) uint8 {
	v = (v&0xf0)>>4 | (v&0x0f)<<4
	v = (v&0xcc)>>2 | (v&0x33)<<2
	return (v&0xaa)>>1 | (v&0x55)<<1
}
