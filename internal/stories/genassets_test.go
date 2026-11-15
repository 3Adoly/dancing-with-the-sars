package stories

import (
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateAssets writes the placeholder story images under data/stories.
// It is skipped unless DWTS_GEN_STORY_ASSETS=1, so a normal `go test` never
// touches the repository:
//
//	DWTS_GEN_STORY_ASSETS=1 go test ./internal/stories -run TestGenerateAssets
//
// The output is committed. The images are solid or gradient fills, so they
// stay at a few hundred bytes each.
func TestGenerateAssets(t *testing.T) {
	if os.Getenv("DWTS_GEN_STORY_ASSETS") != "1" {
		t.Skip("set DWTS_GEN_STORY_ASSETS=1 to regenerate data/stories placeholder images")
	}
	var (
		blue   = color.RGBA{0x21, 0x66, 0xac, 0xff}
		green  = color.RGBA{0x1a, 0x98, 0x50, 0xff}
		red    = color.RGBA{0xb2, 0x18, 0x2b, 0xff}
		orange = color.RGBA{0xf4, 0xa5, 0x82, 0xff}
		purple = color.RGBA{0x76, 0x2a, 0x83, 0xff}
	)
	placeholder := filepath.Join(repoStories, "demo-placeholder")
	twoSensors := filepath.Join(repoStories, "demo-two-sensors")

	writePNG(t, filepath.Join(placeholder, "before.png"), solid(256, 256, blue))
	writePNG(t, filepath.Join(placeholder, "after.png"), solid(256, 256, green))
	writePNG(t, filepath.Join(placeholder, "change.png"), gradient(256, 256, blue, red))
	writeJPEG(t, filepath.Join(placeholder, "thumb.jpg"), gradient(128, 72, blue, green))

	writePNG(t, filepath.Join(twoSensors, "before_saocom.png"), solid(256, 256, orange))
	writePNG(t, filepath.Join(twoSensors, "after_nisar.png"), solid(256, 256, purple))
	writeJPEG(t, filepath.Join(twoSensors, "thumb.jpg"), gradient(128, 72, orange, purple))
}

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// gradient blends a into b from left to right.
func gradient(w, h int, a, b color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	mix := func(p, q uint8, f float64) uint8 { return uint8(float64(p)*(1-f) + float64(q)*f + 0.5) }
	for x := 0; x < w; x++ {
		f := float64(x) / float64(w-1)
		c := color.RGBA{mix(a.R, b.R, f), mix(a.G, b.G, f), mix(a.B, b.B, f), 0xff}
		for y := 0; y < h; y++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f := create(t, path)
	defer f.Close()
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func writeJPEG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f := create(t, path)
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 70}); err != nil {
		t.Fatal(err)
	}
}

func create(t *testing.T, path string) *os.File {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
