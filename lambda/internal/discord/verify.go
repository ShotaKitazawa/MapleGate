package discord

import (
	"crypto/ed25519"
	"encoding/hex"
)

// Verify checks Discord's Ed25519 signature over timestamp+body.
func Verify(publicKey ed25519.PublicKey, sigHex, timestamp string, body []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	msg := append([]byte(timestamp), body...)
	return ed25519.Verify(publicKey, msg, sig)
}
