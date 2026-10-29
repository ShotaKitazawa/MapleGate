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

func TestDotIsFilledAtTheCentreAndEmptyAtTheCorners(t *testing.T) {
	w, at := decode(t, Dot(32, green))

	assert.Equal(t, 32, w)
	assert.Equal(t, green, at(16, 16))
	assert.Equal(t, uint8(0), at(0, 0).A)
	assert.Equal(t, uint8(0), at(31, 31).A)
}

func TestAppIconIsALeafOnAColouredSquare(t *testing.T) {
	_, at := decode(t, App(256))

	assert.Equal(t, uint8(255), at(128, 128).A)
	assert.Equal(t, uint8(255), at(20, 128).A, "the square, not the leaf")
	assert.Equal(t, uint8(0), at(0, 0).A, "rounded corner")
}
