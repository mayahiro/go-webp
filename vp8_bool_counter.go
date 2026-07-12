package webp

func newVP8BoolCounter() *vp8BoolEncoder {
	return &vp8BoolEncoder{
		range_:   255,
		bitCount: 24,
	}
}

func (e *vp8BoolEncoder) size() int {
	return e.emittedBytes + 4
}
