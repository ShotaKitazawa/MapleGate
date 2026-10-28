package discord

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	body := []byte(`{"type":1}`)
	sig := hex.EncodeToString(ed25519.Sign(priv, append([]byte("123"), body...)))

	assert.True(t, Verify(pub, sig, "123", body))
	assert.False(t, Verify(pub, sig, "124", body))
	assert.False(t, Verify(pub, sig, "123", []byte(`{"type":2}`)))
	assert.False(t, Verify(pub, "zz", "123", body))
	assert.False(t, Verify(nil, sig, "123", body))
}
