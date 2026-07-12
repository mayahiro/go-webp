package webp

type vp8BoolEncoder struct {
	out          []byte
	range_       uint32
	bottom       uint32
	bitCount     int
	emittedBytes int
}

func newVP8BoolEncoder() *vp8BoolEncoder {
	return newVP8BoolEncoderWithCapacity(0)
}

func newVP8BoolEncoderWithCapacity(capacity int) *vp8BoolEncoder {
	return &vp8BoolEncoder{
		out:      make([]byte, 0, capacity),
		range_:   255,
		bitCount: 24,
	}
}

func (e *vp8BoolEncoder) writeBit(prob uint8, bit bool) {
	split := 1 + (((e.range_ - 1) * uint32(prob)) >> 8)
	if bit {
		e.bottom += split
		e.range_ -= split
	} else {
		e.range_ = split
	}

	for e.range_ < 128 {
		e.range_ <<= 1
		if e.bottom&(1<<31) != 0 {
			e.addOneToOutput()
		}
		e.bottom <<= 1
		e.bitCount--
		if e.bitCount == 0 {
			e.writeOutputByte(byte(e.bottom >> 24))
			e.bottom &= (1 << 24) - 1
			e.bitCount = 8
		}
	}
}

func (e *vp8BoolEncoder) writeBitEqualProb(bit bool) {
	split := (e.range_ + 1) >> 1
	if bit {
		e.bottom += split
		e.range_ -= split
	} else {
		e.range_ = split
	}

	for e.range_ < 128 {
		e.range_ <<= 1
		if e.bottom&(1<<31) != 0 {
			e.addOneToOutput()
		}
		e.bottom <<= 1
		e.bitCount--
		if e.bitCount == 0 {
			e.writeOutputByte(byte(e.bottom >> 24))
			e.bottom &= (1 << 24) - 1
			e.bitCount = 8
		}
	}
}

func (e *vp8BoolEncoder) writeOutputByte(value byte) {
	if e.out == nil {
		e.emittedBytes++
		return
	}
	e.out = append(e.out, value)
}

func (e *vp8BoolEncoder) addOneToOutput() {
	if e.out == nil {
		return
	}
	for i := len(e.out) - 1; i >= 0; i-- {
		if e.out[i] != 0xff {
			e.out[i]++
			return
		}
		e.out[i] = 0
	}
}

func (e *vp8BoolEncoder) bytes() []byte {
	c := e.bitCount
	v := e.bottom
	if v&(1<<uint(32-c)) != 0 {
		e.addOneToOutput()
	}
	v <<= uint(c & 7)

	for c >>= 3; c > 0; c-- {
		v <<= 8
	}
	for c := 0; c < 4; c++ {
		e.out = append(e.out, byte(v>>24))
		v <<= 8
	}
	return e.out
}
