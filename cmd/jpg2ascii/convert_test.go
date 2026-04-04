// ABOUTME: Tests for JPEG-to-ASCII conversion logic.
// ABOUTME: Uses synthetic images to verify luminance mapping and output dimensions.
package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"strings"
	"testing"
)

const defaultChars = " .:-=+*#%@"

// encodeJPEG writes an image.Image as JPEG to a temp file and returns the path.
func encodeJPEG(t *testing.T, img image.Image) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "test*.jpg")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("jpeg encode: %v", err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		t.Fatalf("write jpeg: %v", err)
	}
	return f.Name()
}

// makeRGBA returns a width×height RGBA image filled with the given color.
func makeRGBA(width, height int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestAllBlack(t *testing.T) {
	img := makeRGBA(4, 4, color.RGBA{0, 0, 0, 255})
	path := encodeJPEG(t, img)

	result, err := LoadAndConvert(path, 4, defaultChars)
	if err != nil {
		t.Fatalf("LoadAndConvert: %v", err)
	}

	darkest := string(defaultChars[0])
	for _, line := range strings.Split(strings.TrimRight(result, "\n"), "\n") {
		for _, ch := range line {
			if string(ch) != darkest {
				t.Errorf("expected only darkest char %q, got %q in line %q", darkest, string(ch), line)
			}
		}
	}
}

func TestAllWhite(t *testing.T) {
	img := makeRGBA(4, 4, color.RGBA{255, 255, 255, 255})
	path := encodeJPEG(t, img)

	result, err := LoadAndConvert(path, 4, defaultChars)
	if err != nil {
		t.Fatalf("LoadAndConvert: %v", err)
	}

	lightest := string(defaultChars[len(defaultChars)-1])
	for _, line := range strings.Split(strings.TrimRight(result, "\n"), "\n") {
		for _, ch := range line {
			if string(ch) != lightest {
				t.Errorf("expected only lightest char %q, got %q in line %q", lightest, string(ch), line)
			}
		}
	}
}

func TestOutputWidth(t *testing.T) {
	// Square image so aspect ratio is predictable
	img := makeRGBA(8, 8, color.RGBA{128, 128, 128, 255})
	path := encodeJPEG(t, img)

	const targetWidth = 4
	result, err := LoadAndConvert(path, targetWidth, defaultChars)
	if err != nil {
		t.Fatalf("LoadAndConvert: %v", err)
	}

	for _, line := range strings.Split(strings.TrimRight(result, "\n"), "\n") {
		if len(line) > targetWidth {
			t.Errorf("line width %d exceeds target %d: %q", len(line), targetWidth, line)
		}
	}
}
