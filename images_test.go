package main

import (
	"image"
	"image/color"
	"testing"
)

func TestBlankImage(t *testing.T) {
	flat := image.NewGray(image.Rect(0, 0, 555, 312))
	if !blankImage(flat) {
		t.Error("a plain black picture is a placeholder")
	}
	// Like ze_sele's preview: a light sky over a dark ground, which compresses to 6 KB.
	scene := image.NewGray(image.Rect(0, 0, 555, 312))
	for y := 0; y < 312; y++ {
		for x := 0; x < 555; x++ {
			v := uint8(180)
			if y > 175 {
				v = 15
			}
			scene.SetGray(x, y, color.Gray{v})
		}
	}
	if blankImage(scene) {
		t.Error("a simple two-tone scene is a real picture")
	}
}
