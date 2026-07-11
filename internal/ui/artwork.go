package ui

import (
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// artworkSupported reports whether the terminal has enough color depth to make
// half-block cover rendering worthwhile (truecolor or 256-color).
func artworkSupported() bool {
	switch lipgloss.ColorProfile() {
	case termenv.TrueColor, termenv.ANSI256:
		return true
	default:
		return false
	}
}

// fetchCover downloads and decodes an artwork image.
func fetchCover(url string) (image.Image, error) {
	cl := &http.Client{Timeout: 10 * time.Second}
	resp, err := cl.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover %s", resp.Status)
	}
	img, _, err := image.Decode(resp.Body)
	return img, err
}

// sample returns the source pixel mapped onto a cols×rows grid (nearest).
func sampleAt(img image.Image, gx, gy, cols, rows int) (uint8, uint8, uint8) {
	b := img.Bounds()
	sx := b.Min.X + gx*b.Dx()/cols
	sy := b.Min.Y + gy*b.Dy()/rows
	r, g, bl, _ := img.At(sx, sy).RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8)
}

// refreshCover (re)renders the current track's cover sized to the window.
func (m *Model) refreshCover() {
	if m.curImg == nil || m.width < 8 || m.height < 8 {
		m.curCover = ""
		return
	}
	rows := availableBodyHeight(m.height, m.footer()) - 4
	if rows > 18 {
		rows = 18
	}
	if rows < 4 {
		rows = 4
	}
	cols := rows * 2 // cells are ~half as tall as wide → keep the cover square
	if cols > m.width-2 {
		cols = m.width - 2
		rows = cols / 2
	}
	m.curCover = renderCover(m.curImg, cols, rows)
}

// renderCover draws an image as a block of half-height cells: each cell packs
// two vertical pixels using "▀" with foreground = top pixel, background =
// bottom pixel. cols cells wide, rows cells tall (=> 2*rows source rows).
func renderCover(img image.Image, cols, rows int) string {
	var sb strings.Builder
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			tr, tg, tb := sampleAt(img, x, 2*y, cols, 2*rows)
			br, bg, bb := sampleAt(img, x, 2*y+1, cols, 2*rows)
			top := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", tr, tg, tb))
			bot := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", br, bg, bb))
			sb.WriteString(lipgloss.NewStyle().Foreground(top).Background(bot).Render("▀"))
		}
		if y < rows-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
