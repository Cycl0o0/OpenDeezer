// Command opendeezer is a terminal Deezer client: log in with your ARL, browse
// liked songs / playlists / search, and stream — decrypt + decode + play all
// locally. Your ARL never leaves your machine except in requests to Deezer.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Cycl0o0/OpenDeezer/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
	odlog "github.com/Cycl0o0/OpenDeezer/internal/log"
	"github.com/Cycl0o0/OpenDeezer/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "1.0.0"

func main() {
	saveARL := flag.String("save-arl", "", "save this ARL to ~/.config/opendeezer/arl.txt and exit")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVer {
		fmt.Println("opendeezer", version)
		return
	}

	if *saveARL != "" {
		if err := ui.SaveARL(*saveARL); err != nil {
			fmt.Fprintln(os.Stderr, "save-arl:", err)
			os.Exit(1)
		}
		fmt.Println("ARL saved.")
		return
	}

	// File logging (level via $OPENDEEZER_LOG); never writes to stdout, so the
	// TUI is unaffected. Best-effort: discards on failure. The log file is held
	// open for the process lifetime and released by the OS on exit (some paths
	// below call os.Exit, which would skip a deferred Close anyway).
	if base, err := os.UserConfigDir(); err == nil {
		_, _ = odlog.OpenFile(filepath.Join(base, "opendeezer"))
	}
	odlog.Info("opendeezer %s starting", version)

	arl := ui.LoadARL()
	if arl == "" {
		fmt.Fprintln(os.Stderr, "No ARL found. Set $DEEZER_ARL or run:")
		fmt.Fprintln(os.Stderr, "  opendeezer -save-arl <your-arl>")
		fmt.Fprintln(os.Stderr, "\nYour ARL is the 'arl' cookie from an authenticated deezer.com session.")
		os.Exit(1)
	}

	player, err := audio.NewPlayer()
	if err != nil {
		fmt.Fprintln(os.Stderr, "audio:", err)
		os.Exit(1)
	}

	ui.Version = version
	client := deezer.New(arl)
	client.SetQuality(ui.LoadQuality()) // apply persisted quality preference
	model := ui.New(client, player)

	p := tea.NewProgram(model, tea.WithAltScreen())
	model.StartMedia(p.Send) // OS media controls (MPRIS on Linux)
	if err := model.StartControl(p.Send); err != nil {
		odlog.Warn("control api: %v", err) // non-fatal: TUI still runs
	}
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
