package main

import (
	"math"
	"slices"
	"sort"
)

type pchip struct {
	x []float64
	y []float64
	d []float64
}

// newPCHIP uses shape-preserving derivatives so sparse rate-distortion points
// do not acquire the overshoot of an unconstrained cubic fit
func newPCHIP(x []float64, y []float64) (pchip, bool) {
	if len(x) != len(y) || len(x) < 2 {
		return pchip{}, false
	}
	x = slices.Clone(x)
	y = slices.Clone(y)
	h := make([]float64, len(x)-1)
	delta := make([]float64, len(x)-1)
	for i := range h {
		if !finiteFloat(x[i]) || !finiteFloat(y[i]) || !(x[i+1] > x[i]) {
			return pchip{}, false
		}
		h[i] = x[i+1] - x[i]
		delta[i] = (y[i+1] - y[i]) / h[i]
	}
	if !finiteFloat(x[len(x)-1]) || !finiteFloat(y[len(y)-1]) {
		return pchip{}, false
	}
	d := make([]float64, len(x))
	if len(x) == 2 {
		d[0] = delta[0]
		d[1] = delta[0]
		return pchip{x: x, y: y, d: d}, true
	}
	d[0] = pchipEndpointDerivative(h[0], h[1], delta[0], delta[1])
	for i := 1; i < len(x)-1; i++ {
		if delta[i-1] == 0 || delta[i] == 0 || math.Signbit(delta[i-1]) != math.Signbit(delta[i]) {
			d[i] = 0
			continue
		}
		w1 := 2*h[i] + h[i-1]
		w2 := h[i] + 2*h[i-1]
		d[i] = (w1 + w2) / (w1/delta[i-1] + w2/delta[i])
	}
	d[len(d)-1] = pchipEndpointDerivative(h[len(h)-1], h[len(h)-2], delta[len(delta)-1], delta[len(delta)-2])
	return pchip{x: x, y: y, d: d}, true
}

func pchipEndpointDerivative(h0 float64, h1 float64, delta0 float64, delta1 float64) float64 {
	derivative := ((2*h0+h1)*delta0 - h0*delta1) / (h0 + h1)
	if derivative == 0 || delta0 == 0 || math.Signbit(derivative) != math.Signbit(delta0) {
		return 0
	}
	if math.Signbit(delta0) != math.Signbit(delta1) && math.Abs(derivative) > math.Abs(3*delta0) {
		return 3 * delta0
	}
	return derivative
}

func (curve pchip) value(x float64) (float64, bool) {
	if len(curve.x) < 2 || x < curve.x[0] || x > curve.x[len(curve.x)-1] {
		return 0, false
	}
	if x == curve.x[len(curve.x)-1] {
		return curve.y[len(curve.y)-1], true
	}
	i := sort.Search(len(curve.x)-1, func(i int) bool { return curve.x[i+1] >= x })
	h := curve.x[i+1] - curve.x[i]
	t := (x - curve.x[i]) / h
	value := (2*t*t*t-3*t*t+1)*curve.y[i] +
		(t*t*t-2*t*t+t)*h*curve.d[i] +
		(-2*t*t*t+3*t*t)*curve.y[i+1] +
		(t*t*t-t*t)*h*curve.d[i+1]
	return value, finiteFloat(value)
}

func (curve pchip) integral(low float64, high float64) (float64, bool) {
	if len(curve.x) < 2 || low < curve.x[0] || high > curve.x[len(curve.x)-1] || high < low {
		return 0, false
	}
	if low == high {
		return 0, true
	}
	total := 0.0
	for i := 0; i < len(curve.x)-1 && low < high; i++ {
		segmentLow := math.Max(low, curve.x[i])
		segmentHigh := math.Min(high, curve.x[i+1])
		if segmentHigh <= segmentLow {
			continue
		}
		h := curve.x[i+1] - curve.x[i]
		a := (segmentLow - curve.x[i]) / h
		b := (segmentHigh - curve.x[i]) / h
		total += h * (pchipAntiderivative(curve.y[i], h*curve.d[i], curve.y[i+1], h*curve.d[i+1], b) -
			pchipAntiderivative(curve.y[i], h*curve.d[i], curve.y[i+1], h*curve.d[i+1], a))
	}
	return total, finiteFloat(total)
}

// pchipAntiderivative integrates one normalized cubic Hermite segment
func pchipAntiderivative(y0 float64, hd0 float64, y1 float64, hd1 float64, t float64) float64 {
	t2 := t * t
	t3 := t2 * t
	t4 := t3 * t
	return y0*(t-t3+t4/2) +
		hd0*(t2/2-2*t3/3+t4/4) +
		y1*(t3-t4/2) +
		hd1*(t4/4-t3/3)
}

func finiteFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
