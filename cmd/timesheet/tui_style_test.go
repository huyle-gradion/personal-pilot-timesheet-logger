package main

import (
	"io"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// asciiRenderer pins the Ascii profile, where every pure-attribute style renders
// as identity; test frames are hermetic and host TERM/CLICOLOR_FORCE independent.
func asciiRenderer() *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.Ascii)
	return r
}

func testStyles() uiStyles { return newUIStyles(asciiRenderer()) }

// styledStyles pins ANSI so every role emits SGR; comparing a styled frame
// against the Ascii render of the same model proves the styles are text-neutral.
func styledStyles() uiStyles {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI)
	return newUIStyles(r)
}

func stripANSI(frame string) string { return ansi.Strip(frame) }
