package metadata

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png" // yt-dlp embeds the thumbnail as PNG or JPEG
)

// YouTube's thumbnail of an album track centres the square cover in a 16:9 frame.
func squareCover(data []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decoding the thumbnail: %w", err)
	}

	b := img.Bounds()
	if b.Dx() == b.Dy() {
		return data, nil
	}
	side := min(b.Dx(), b.Dy())
	from := image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2)

	square := image.NewRGBA(image.Rect(0, 0, side, side))
	draw.Draw(square, square.Bounds(), img, from, draw.Src)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, square, &jpeg.Options{Quality: 90}); err != nil {
		return nil, fmt.Errorf("encoding the cover: %w", err)
	}
	return out.Bytes(), nil
}
