// Package icon draws the maple leaf of the tray and of the app, so there is no image file to keep in step with the code.
package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// half is the right half of the leaf as (distance from the centre line, y) in a unit square, from the tip down to the stem.
var half = [][2]float64{
	{0.00, 0.02}, {0.07, 0.19}, {0.13, 0.14}, {0.12, 0.31}, {0.27, 0.20}, {0.25, 0.40},
	{0.45, 0.42}, {0.31, 0.57}, {0.35, 0.68}, {0.09, 0.64}, {0.025, 0.74}, {0.025, 0.98},
}

func leaf(size, pad float64) [][2]float64 {
	var pts [][2]float64
	scale := size * (1 - 2*pad)
	at := func(dx, y float64) [2]float64 { return [2]float64{size/2 + dx*scale, pad*size + y*scale} }
	for _, p := range half {
		pts = append(pts, at(p[0], p[1]))
	}
	for i := len(half) - 1; i >= 1; i-- {
		pts = append(pts, at(-half[i][0], half[i][1]))
	}
	return pts
}

func inside(pts [][2]float64, x, y float64) bool {
	in := false
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		a, b := pts[i], pts[j]
		if (a[1] > y) != (b[1] > y) && x < (b[0]-a[0])*(y-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

// coverage is how much of the pixel the polygon covers, by 4x4 samples.
func coverage(pts [][2]float64, px, py int) float64 {
	n := 0
	for sy := 0; sy < 4; sy++ {
		for sx := 0; sx < 4; sx++ {
			if inside(pts, float64(px)+(float64(sx)+0.5)/4, float64(py)+(float64(sy)+0.5)/4) {
				n++
			}
		}
	}
	return float64(n) / 16
}

func encode(img image.Image) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func blend(dst *image.RGBA, x, y int, c color.RGBA, a float64) {
	if a <= 0 {
		return
	}
	dst.SetRGBA(x, y, color.RGBA{uint8(float64(c.R) * a), uint8(float64(c.G) * a), uint8(float64(c.B) * a), uint8(255 * a)})
}

// Leaf is a maple leaf of one colour on a transparent square, for the menu bar.
func Leaf(size int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	pts := leaf(float64(size), 0.04)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			blend(img, x, y, c, coverage(pts, x, y))
		}
	}
	return encode(img)
}

// App is a white maple leaf on a red rounded square, for the app bundle.
func App(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	red, white := color.RGBA{200, 40, 40, 255}, color.RGBA{255, 255, 255, 255}
	s := float64(size)
	r := s * 0.22
	pts := leaf(s, 0.2)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if !roundedInside(float64(x)+0.5, float64(y)+0.5, s, r) {
				continue
			}
			img.SetRGBA(x, y, red)
			if a := coverage(pts, x, y); a > 0 {
				img.SetRGBA(x, y, color.RGBA{
					uint8(float64(red.R)*(1-a) + float64(white.R)*a),
					uint8(float64(red.G)*(1-a) + float64(white.G)*a),
					uint8(float64(red.B)*(1-a) + float64(white.B)*a), 255})
			}
		}
	}
	return encode(img)
}

func roundedInside(x, y, s, r float64) bool {
	cx, cy := x, y
	if cx < r {
		cx = r
	} else if cx > s-r {
		cx = s - r
	}
	if cy < r {
		cy = r
	} else if cy > s-r {
		cy = s - r
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}
