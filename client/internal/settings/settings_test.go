package settings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t *testing.T, body string) string {
	d := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(d, "config.json"), []byte(body), 0o600))
	return d
}

func TestLoadFillsTheRoleNameWhenItIsLeftOut(t *testing.T) {
	s, err := Load(write(t, `{"start_url":"https://s","region":"r","account_id":"1","api_url":"https://a"}`))

	require.NoError(t, err)
	assert.Equal(t, "gamevpn-player", s.RoleName)
}

func TestLoadNamesTheMissingField(t *testing.T) {
	_, err := Load(write(t, `{"start_url":"https://s","region":"r","api_url":"https://a"}`))

	assert.ErrorContains(t, err, "account_id")
}

func TestLoadSaysWhereTheFileShouldBe(t *testing.T) {
	_, err := Load(t.TempDir())

	assert.ErrorContains(t, err, "config.json does not exist")
}

func TestLoadReadsTheGameProcessesAndTheyAreOptional(t *testing.T) {
	base := `"start_url":"https://s","region":"r","account_id":"1","api_url":"https://a"`

	with, err := Load(write(t, `{`+base+`,"game_processes":["A","B C"]}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "B C"}, with.GameProcesses)

	without, err := Load(write(t, `{`+base+`}`))
	require.NoError(t, err)
	assert.Empty(t, without.GameProcesses)
}

func TestLoadFallsBackToTheSharedFileWhenThePersonHasNone(t *testing.T) {
	shared := filepath.Join(write(t, `{"start_url":"https://s","region":"r","account_id":"1","api_url":"https://a"}`), "config.json")
	old := Shared
	Shared = shared
	t.Cleanup(func() { Shared = old })

	s, err := Load(t.TempDir())

	require.NoError(t, err)
	assert.Equal(t, "https://s", s.StartURL)
}

func TestLoadPrefersThePersonsOwnFile(t *testing.T) {
	shared := filepath.Join(write(t, `{"start_url":"https://shared","region":"r","account_id":"1","api_url":"https://a"}`), "config.json")
	old := Shared
	Shared = shared
	t.Cleanup(func() { Shared = old })

	s, err := Load(write(t, `{"start_url":"https://own","region":"r","account_id":"1","api_url":"https://a"}`))

	require.NoError(t, err)
	assert.Equal(t, "https://own", s.StartURL)
}
