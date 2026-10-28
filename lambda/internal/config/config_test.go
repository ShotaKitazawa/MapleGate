package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoadDefaultsAndOverride(t *testing.T) {
	t.Setenv("TABLE_NAME", "gamevpn")
	t.Setenv("INSTANCE_ID", "i-1")
	t.Setenv("SESSION_TTL", "2h")

	c, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, 2*time.Hour, c.SessionTTL)
	assert.Equal(t, 51820, c.WGPort)
	assert.Equal(t, []string{"0.0.0.0/0", "::/0"}, c.AllowedIPs)
}

func TestLoadRequiresTable(t *testing.T) {
	t.Setenv("TABLE_NAME", "")
	_, err := Load()
	assert.Error(t, err)
}
