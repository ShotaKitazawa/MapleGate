package icon

import (
	"bytes"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var green = color.RGBA{40, 170, 70, 255}

func decode(t *testing.T, b []byte) (w int, at func(x, y int) color.RGBA) {
	img, err := png.Decode(bytes.NewReader(b))
	require.NoError(t, err)
	return img.Bounds().Dx(), func(x, y int) color.RGBA { return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA) }
}

func TestLeafIsFilledAtTheCentreAndEmptyAtTheCorners(t *testing.T) {
	w, at := decode(t, Leaf(44, green))

	assert.Equal(t, 44, w)
	assert.Equal(t, uint8(255), at(22, 22).A)
	assert.Equal(t, uint8(0), at(0, 0).A)
	assert.Equal(t, uint8(0), at(43, 0).A)
	assert.Equal(t, uint8(0), at(0, 43).A)
}

func TestLeafIsSymmetric(t *testing.T) {
	_, at := decode(t, Leaf(44, green))

	for y := 0; y < 44; y++ {
		assert.Equal(t, at(10, y), at(43-10, y), "row %d", y)
	}
}

func TestAppIconIsALeafOnAColouredSquare(t *testing.T) {
	_, at := decode(t, App(256))

	assert.Equal(t, uint8(255), at(128, 128).A)
	assert.Equal(t, uint8(255), at(20, 128).A, "the square, not the leaf")
	assert.Equal(t, uint8(0), at(0, 0).A, "rounded corner")
}
