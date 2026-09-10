package audio

import (
	"path/filepath"
	"testing"
)

// TestVolumePersistsAcrossPlayers checks that the level a client sets is where
// the next player starts, and that restoring it doesn't schedule a write.
func TestVolumePersistsAcrossPlayers(t *testing.T) {
	tmp := t.TempDir()
	// Redirect the config dir on every platform (darwin/linux/windows).
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("AppData", filepath.Join(tmp, "AppData"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))

	first := &Player{}
	first.loadVolume()
	if v := first.Volume(); v != 1 {
		t.Fatalf("volume with nothing saved = %v, want 1 (full)", v)
	}
	if first.volSaveTimer != nil {
		t.Fatal("restoring the volume scheduled a save")
	}

	first.SetVolume(0.25)
	if got := first.AddVolume(0.05); got != 0.3 {
		t.Fatalf("AddVolume = %v, want 0.3", got)
	}
	first.flushVolumeSave() // what Close does on quit

	next := &Player{}
	next.loadVolume()
	if v := next.Volume(); v != 0.3 {
		t.Fatalf("next player's volume = %v, want the saved 0.3", v)
	}
}
