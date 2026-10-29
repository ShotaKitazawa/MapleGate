package tunnel

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTheRouteIsGivenUpOnAfterConsecutiveFailures(t *testing.T) {
	var w routeWatch
	boom := errors.New("boom")

	assert.False(t, w.failed(boom))
	assert.False(t, w.failed(boom))
	assert.True(t, w.failed(boom))
}

func TestASuccessStartsTheCountAgain(t *testing.T) {
	var w routeWatch
	boom := errors.New("boom")
	w.failed(boom)
	w.failed(boom)

	assert.False(t, w.failed(nil))
	assert.False(t, w.failed(boom))
	assert.False(t, w.failed(boom))
	assert.True(t, w.failed(boom))
}
