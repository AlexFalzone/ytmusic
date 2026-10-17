package metadata

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// A w×h PNG with black bars around a red h×h square, like YouTube's thumbnail of an album track.
func letterboxed(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	left := (w - h) / 2
	for y := range h {
		for x := range w {
			c := color.RGBA{A: 255}
			if x >= left && x < left+h {
				c.R = 255
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSquareCoverCropsTheBars(t *testing.T) {
	out, err := squareCover(letterboxed(t, 160, 90))
	if err != nil {
		t.Fatalf("squareCover: %v", err)
	}

	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("result is not a JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 90 || b.Dy() != 90 {
		t.Fatalf("size = %dx%d, want 90x90", b.Dx(), b.Dy())
	}
	for _, p := range []image.Point{{1, 1}, {88, 1}, {45, 45}, {1, 88}, {88, 88}} {
		if r, _, _, _ := img.At(p.X, p.Y).RGBA(); r < 0xc000 {
			t.Errorf("pixel %v is not red: a bar survived the crop", p)
		}
	}
}

func TestSquareCoverKeepsASquareImage(t *testing.T) {
	in := letterboxed(t, 50, 50)
	out, err := squareCover(in)
	if err != nil {
		t.Fatalf("squareCover: %v", err)
	}
	if !bytes.Equal(out, in) {
		t.Error("a square image must come back untouched")
	}
}

func TestSquareCoverRejectsWhatItCannotDecode(t *testing.T) {
	if _, err := squareCover([]byte("not an image")); err == nil {
		t.Error("expected an error")
	}
}
