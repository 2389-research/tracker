// ABOUTME: Core image loading and ASCII conversion logic for jpg2ascii CLI.
// ABOUTME: Decodes JPEG files and maps pixel luminance to ASCII characters.
package main

import (
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"strings"
)

// LoadAndConvert loads a JPEG from path, converts it to ASCII art at the given
// width (height is adjusted with a 2:1 aspect ratio), using chars as the
// luminance palette from dark to light.
func LoadAndConvert(path string, width int, chars string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	img, err := jpeg.Decode(f)
	if err != nil {
		return "", fmt.Errorf("decode jpeg %s: %w", path, err)
	}

	return convertImage(img, width, chars), nil
}

// convertImage converts an image.Image to ASCII art.
func convertImage(img image.Image, width int, chars string) string {
	bounds := img.Bounds()
	srcW := bounds.Max.X - bounds.Min.X
	srcH := bounds.Max.Y - bounds.Min.Y

	// Height adjusted with 2:1 aspect ratio (terminal chars are ~2x taller than wide)
	height := srcH * width / srcW / 2
	if height < 1 {
		height = 1
	}

	lastIdx := len(chars) - 1
	var sb strings.Builder

	for y := 0; y < height; y++ {
		lineEnd := -1
		row := make([]byte, width)
		for x := 0; x < width; x++ {
			// Map output pixel to source pixel
			srcX := bounds.Min.X + x*srcW/width
			srcY := bounds.Min.Y + y*srcH/height
			r, g, b, _ := img.At(srcX, srcY).RGBA()

			// RGBA returns 16-bit values; shift to 8-bit
			r8 := float64(r >> 8)
			g8 := float64(g >> 8)
			b8 := float64(b >> 8)

			luminance := 0.299*r8 + 0.587*g8 + 0.114*b8
			idx := int(luminance*float64(lastIdx)/255.0 + 0.5)
			if idx > lastIdx {
				idx = lastIdx
			}
			ch := chars[idx]
			row[x] = ch
			if ch != ' ' {
				lineEnd = x
			}
		}
		// Write row without trailing spaces
		sb.Write(row[:lineEnd+1])
		sb.WriteByte('\n')
	}

	return sb.String()
}
