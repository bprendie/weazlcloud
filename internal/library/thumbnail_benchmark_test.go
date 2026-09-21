package library

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func BenchmarkThumbnailDecode100(b *testing.B) { benchmarkThumbnailDecode(b, 100) }

func BenchmarkThumbnailDecode500(b *testing.B) { benchmarkThumbnailDecode(b, 500) }

func benchmarkThumbnailDecode(b *testing.B, count int) {
	var source bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: uint8((x + y) % 255), A: 255})
		}
	}
	if err := png.Encode(&source, img); err != nil {
		b.Fatal(err)
	}
	data := source.Bytes()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < count; j++ {
			if _, _, err := makeThumbnail(data, 320); err != nil {
				b.Fatal(err)
			}
		}
	}
}
