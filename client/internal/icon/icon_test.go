package icon

import (
	"bytes"
	"encoding/binary"
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

func TestAppIconIsARedLeafOnNothing(t *testing.T) {
	_, at := decode(t, App(256))

	c := at(128, 128)
	assert.Equal(t, uint8(255), c.A)
	assert.Greater(t, c.R, c.G, "red")
	assert.Equal(t, uint8(0), at(0, 0).A)
	assert.Equal(t, uint8(0), at(255, 255).A)
}

func TestICOWrapsThePNGForTheWindowsTray(t *testing.T) {
	png := Dot(32, color.RGBA{1, 2, 3, 255})
	ico := ICO(png, 32)

	assert.Equal(t, []byte{0, 0, 1, 0, 1, 0}, ico[:6], "reserved, type icon, one image")
	assert.Equal(t, byte(32), ico[6], "width")
	assert.Equal(t, uint32(22), binary.LittleEndian.Uint32(ico[18:22]), "the image starts after the 6 byte header and the 16 byte entry")
	assert.Equal(t, uint32(len(png)), binary.LittleEndian.Uint32(ico[14:18]))
	assert.Equal(t, png, ico[22:])
}
