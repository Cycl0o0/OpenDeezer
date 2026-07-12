// Command opendeezer is a terminal Deezer client: log in with your ARL, browse
// liked songs / playlists / search, and stream — decrypt + decode + play all
// locally. Your ARL never leaves your machine except in requests to Deezer.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/config"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/i18n"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/impex"
	odlog "github.com/Cycl0o0/OpenDeezer/v2/internal/log"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/mediacache"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/ui"
	version_ "github.com/Cycl0o0/OpenDeezer/v2/internal/version"

	tea "github.com/charmbracelet/bubbletea"
)

// version defaults to the release number in internal/version and can be
// overridden at build time via -ldflags "-X main.version=...".
var version = version_.Number

func main() {
	saveARL := flag.String("save-arl", "", "save this ARL to ~/.config/opendeezer/arl.txt and exit")
	showVer := flag.Bool("version", false, "print version and exit")
	exportPl := flag.String("export-playlist", "", "export the playlist with this id and exit (see -format, -o)")
	format := flag.String("format", "csv", "export format for -export-playlist: csv, m3u or json")
	outPath := flag.String("o", "", "output file for -export-playlist (default: stdout)")
	importPl := flag.String("import-playlist", "", "import this playlist file (CSV/M3U/plain text) into your Deezer account and exit")
	plTitle := flag.String("playlist-title", "", "title for the playlist created by -import-playlist (default: the file name)")
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

	if *exportPl != "" {
		if err := runExportPlaylist(*exportPl, *format, *outPath); err != nil {
			fmt.Fprintln(os.Stderr, "export-playlist:", err)
			os.Exit(1)
		}
		return
	}

	if *importPl != "" {
		if err := runImportPlaylist(*importPl, *plTitle); err != nil {
			fmt.Fprintln(os.Stderr, "import-playlist:", err)
			os.Exit(1)
		}
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

	// Optional on-disk raw-stream cache (media.json: mediaCacheMB > 0 enables).
	// Best-effort: a cache that fails to open just means uncached streaming.
	if mb := config.LoadMedia().MediaCacheMB; mb > 0 {
		if dir, derr := config.Dir(); derr == nil {
			if c, cerr := mediacache.New(filepath.Join(dir, "mediacache"), int64(mb)<<20); cerr == nil {
				player.SetStreamCache(c)
			} else {
				odlog.Warn("mediacache: %v (continuing without cache)", cerr)
			}
		} else {
			odlog.Warn("mediacache: no config dir: %v (continuing without cache)", derr)
		}
	}

	// Set the UI language before building the model (some strings, e.g. the
	// search placeholder, are captured at construction). The persisted language
	// wins; an empty setting means auto-detect from the locale environment.
	if lang := config.LoadLanguage(); lang != "" {
		i18n.SetLocale(lang)
	} else {
		i18n.SetLocale(i18n.DetectLocale())
	}

	ui.Version = version
	client := deezer.New(arl)
	client.SetQuality(ui.LoadQuality()) // apply persisted quality preference
	model := ui.New(client, player)

	// Mouse cell-motion: wheel scrolling, click-to-select/activate and
	// progress-bar seeking (see internal/ui/mouse.go).
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	model.StartMedia(p.Send) // OS media controls (MPRIS on Linux)
	if err := model.StartControl(p.Send); err != nil {
		odlog.Warn("control api: %v", err) // non-fatal: TUI still runs
	}
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// impexLogin logs in with the saved ARL exactly like the TUI does, with clear
// errors when there is no (working) ARL.
func impexLogin() (*deezer.Client, error) {
	arl := ui.LoadARL()
	if arl == "" {
		return nil, errors.New("not logged in — set $DEEZER_ARL or run: opendeezer -save-arl <your-arl>")
	}
	client := deezer.New(arl)
	if err := client.Login(); err != nil {
		if errors.Is(err, deezer.ErrARLExpired) {
			return nil, errors.New("ARL expired or invalid — refresh the 'arl' cookie from deezer.com, then `opendeezer -save-arl <arl>`")
		}
		return nil, fmt.Errorf("login: %w", err)
	}
	return client, nil
}

// runExportPlaylist implements -export-playlist: fetch the playlist and write
// it as CSV (Exportify style), extended M3U or JSON to -o (default stdout).
func runExportPlaylist(id, format, outPath string) error {
	client, err := impexLogin()
	if err != nil {
		return err
	}
	tracks, err := client.PlaylistTracks(id)
	if err != nil {
		return fmt.Errorf("fetch playlist %s: %w", id, err)
	}
	// The tracks endpoint doesn't return the title; look it up in the user's
	// own playlists (the common case) and fall back to a generic name.
	name := "Playlist " + id
	if ps, perr := client.Playlists(); perr == nil {
		for _, p := range ps {
			if p.ID == id {
				name = p.Name
				break
			}
		}
	}

	var w io.Writer = os.Stdout
	if outPath != "" {
		f, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	switch format {
	case "csv":
		err = impex.ExportCSV(w, name, tracks)
	case "m3u":
		err = impex.ExportM3U(w, name, tracks)
	case "json":
		err = impex.ExportJSON(w, name, tracks)
	default:
		return fmt.Errorf("unknown -format %q (want csv, m3u or json)", format)
	}
	if err != nil {
		return err
	}
	if outPath != "" {
		fmt.Printf("Exported %d tracks of %q to %s\n", len(tracks), name, outPath)
	}
	return nil
}

// runImportPlaylist implements -import-playlist: parse the file, resolve every
// row against Deezer (ISRC first, then fuzzy artist+title) with per-row
// progress, create the playlist, and summarize what didn't match.
func runImportPlaylist(path, title string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	rows, err := impex.ParseCSV(f)
	f.Close()
	if err != nil {
		return err
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	client, err := impexLogin()
	if err != nil {
		return err
	}

	fmt.Printf("Importing %d rows into %q…\n", len(rows), title)
	progress := func(i, total int, matched bool) {
		mark := "✓"
		if !matched {
			mark = "✗"
		}
		r := rows[i-1]
		label := r.Title
		if r.Artist != "" {
			label = r.Artist + " - " + r.Title
		}
		fmt.Printf("  [%d/%d] %s %s\n", i, total, mark, label)
	}
	res, err := impex.ImportPlaylist(context.Background(), impex.NewClientResolver(client), client, title, rows, progress)

	// Summary (also on failure: the partial result names what went wrong).
	if res != nil {
		fmt.Printf("\nMatched %d/%d rows.\n", len(res.Matched), len(rows))
		if len(res.Unmatched) > 0 {
			fmt.Println("Unmatched:")
			for _, r := range res.Unmatched {
				label := r.Title
				if r.Artist != "" {
					label = r.Artist + " - " + r.Title
				}
				fmt.Println("  - " + label)
			}
		}
	}
	if err != nil {
		return err
	}
	fmt.Printf("Created playlist %q (id %s) with %d tracks.\n", title, res.PlaylistID, len(res.Matched))
	return nil
}
