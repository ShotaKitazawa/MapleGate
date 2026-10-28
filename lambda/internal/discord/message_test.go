package discord

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResultMessageNamesTheApproverWhenThereIsOne(t *testing.T) {
	m := ResultMessage("s1", "alice", "approved", "bob", "T")

	assert.Equal(t, "VPN access for **alice** (session `s1`): **approved** by bob at T", m.Content)
	assert.Empty(t, m.Components, "the buttons are gone")
}

func TestResultMessageLeavesOutTheApproverWhenNobodyDecided(t *testing.T) {
	m := ResultMessage("s1", "alice", "closed", "", "T")

	assert.Equal(t, "VPN access for **alice** (session `s1`): **closed** at T", m.Content)
}
