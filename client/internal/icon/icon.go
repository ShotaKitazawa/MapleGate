// Package icon draws the menu bar dot and the app's maple leaf, so there is no image file to keep in step with the code.
package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
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

// Dot is a filled circle of one colour on a transparent square, for the menu bar: a state is all it has to say.
func Dot(size int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	r, mid := float64(size)*0.36, float64(size)/2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			n := 0
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					dx, dy := float64(x)+(float64(sx)+0.5)/4-mid, float64(y)+(float64(sy)+0.5)/4-mid
					if dx*dx+dy*dy <= r*r {
						n++
					}
				}
			}
			blend(img, x, y, c, float64(n)/16)
		}
	}
	return encode(img)
}

// smooth cuts every corner once, so the tips of the leaf are round instead of sharp.
func smooth(pts [][2]float64) [][2]float64 {
	var out [][2]float64
	for i := range pts {
		a, b := pts[i], pts[(i+1)%len(pts)]
		out = append(out,
			[2]float64{a[0]*0.8 + b[0]*0.2, a[1]*0.8 + b[1]*0.2},
			[2]float64{a[0]*0.2 + b[0]*0.8, a[1]*0.2 + b[1]*0.8})
	}
	return out
}

func rotate(pts [][2]float64, cx, cy, deg float64) [][2]float64 {
	sin, cos := math.Sincos(deg * math.Pi / 180)
	out := make([][2]float64, len(pts))
	for i, p := range pts {
		dx, dy := p[0]-cx, p[1]-cy
		out[i] = [2]float64{cx + dx*cos - dy*sin, cy + dx*sin + dy*cos}
	}
	return out
}

// App is a red maple leaf, tilted, on a transparent square, for the app bundle. It is not on a coloured square: the leaf is the whole icon.
func App(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	red := color.RGBA{205, 30, 40, 255}
	s := float64(size)
	pts := rotate(smooth(leaf(s, 0.1)), s/2, s/2, 28)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			blend(img, x, y, red, coverage(pts, x, y))
		}
	}
	return encode(img)
}
