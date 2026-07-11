package webp

type vp8lByteWriter interface {
	WriteByte(byte) error
}

type vp8lBitSink struct {
	writer  vp8lByteWriter
	buffer  uint64
	pending uint8
	bitLen  uint64
	err     error
}

func vp8lBitCounter() *vp8lBitSink {
	return &vp8lBitSink{}
}

func vp8lBitWriter(writer vp8lByteWriter) *vp8lBitSink {
	return &vp8lBitSink{writer: writer}
}

func (s *vp8lBitSink) writeBits(value uint32, n uint8) {
	if s.err != nil || n == 0 {
		return
	}
	s.bitLen += uint64(n)
	if s.writer == nil {
		return
	}
	s.buffer |= uint64(value&vp8lLowBitsMask(n)) << s.pending
	s.pending += n
	for s.pending >= 8 {
		s.err = s.writer.WriteByte(byte(s.buffer))
		if s.err != nil {
			return
		}
		s.buffer >>= 8
		s.pending -= 8
	}
}

func (s *vp8lBitSink) flush() error {
	if s.err != nil {
		return s.err
	}
	if s.writer == nil || s.pending == 0 {
		return nil
	}
	if err := s.writer.WriteByte(byte(s.buffer)); err != nil {
		return err
	}
	s.buffer = 0
	s.pending = 0
	return nil
}

func vp8lLowBitsMask(n uint8) uint32 {
	if n >= 32 {
		return ^uint32(0)
	}
	return 1<<n - 1
}

func vp8lReverse8(value uint8) uint8 {
	value = value&0xf0>>4 | value&0x0f<<4
	value = value&0xcc>>2 | value&0x33<<2
	return value&0xaa>>1 | value&0x55<<1
}
