package main

import (
	"math"
	"testing"
)

func TestMeasurePlaneSSIMReportsExactPlanes(t *testing.T) {
	plane := []byte{0, 10, 20, 30, 40, 50}
	got, err := measurePlaneSSIM(plane, plane, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 || ssimDB(got) != nil {
		t.Fatalf("exact SSIM = %v/%v, want 1/nil", got, ssimDB(got))
	}
}

func TestMeasurePlaneSSIMMatchesUniformPlaneFormula(t *testing.T) {
	source := make([]byte, 64)
	target := make([]byte, 64)
	for i := range source {
		source[i] = 100
		target[i] = 110
	}
	got, err := measurePlaneSSIM(source, target, 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	want := float64(2*100*110+20) / float64(100*100+110*110+20)
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("uniform-plane SSIM = %.15f, want %.15f", got, want)
	}
}

func TestMeasurePlaneSSIMRejectsInvalidDimensions(t *testing.T) {
	if _, err := measurePlaneSSIM([]byte{1}, []byte{1}, 2, 1); err == nil {
		t.Fatal("measurePlaneSSIM accepted invalid dimensions")
	}
}
