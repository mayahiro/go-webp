package webp

type vp8DCDiffusion struct {
	top  [][2][2]int8
	left [2][2]int8
}

type vp8DCDiffusionMacroblock struct {
	owner   *vp8DCDiffusion
	mbx     int
	channel int
	top     [2]int8
	left    [2]int8
	errors  [4]int8
}

func newVP8DCDiffusion(mbw int) *vp8DCDiffusion {
	return &vp8DCDiffusion{top: make([][2][2]int8, mbw)}
}

func (d *vp8DCDiffusion) beginMacroblock(mbx int, cb bool) vp8DCDiffusionMacroblock {
	channel := 0
	if !cb {
		channel = 1
	}
	if mbx == 0 {
		d.left[channel] = [2]int8{}
	}
	return vp8DCDiffusionMacroblock{
		owner:   d,
		mbx:     mbx,
		channel: channel,
		top:     d.top[mbx][channel],
		left:    d.left[channel],
	}
}

func (d *vp8DCDiffusionMacroblock) correct(block int, value int, quantizer int) int {
	correction := 0
	switch block {
	case 0:
		correction = (7*int(d.top[0]) + 8*int(d.left[0])) >> 3
	case 1:
		correction = (7*int(d.top[1]) + 8*int(d.errors[0])) >> 3
	case 2:
		correction = (7*int(d.errors[0]) + 8*int(d.left[1])) >> 3
	default:
		correction = (7*int(d.errors[1]) + 8*int(d.errors[2])) >> 3
	}
	corrected := value + correction
	level := int(quantizeTransformCoeff(corrected, quantizer))
	errorValue := (corrected - level*quantizer) >> 1
	d.errors[block] = int8(clipInt(errorValue, -127, 127))
	return corrected
}

func (d *vp8DCDiffusionMacroblock) finish() {
	if d.owner == nil {
		return
	}
	err3Left := 3 * int(d.errors[3]) >> 2
	d.owner.left[d.channel][0] = d.errors[1]
	d.owner.left[d.channel][1] = int8(err3Left)
	d.owner.top[d.mbx][d.channel][0] = d.errors[2]
	d.owner.top[d.mbx][d.channel][1] = int8(int(d.errors[3]) - err3Left)
}
