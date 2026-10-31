package main

// The TUI's one style owner: the only production file that imports lipgloss.

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

// uiStyles owns every lipgloss style the TUI renders with, bound to one
// renderer so the color-profile decision happens once. Styles are SGR-only
// (attributes and ANSI-16 colors, never width, padding or borders): a style
// that changed text would break the goldens' strip-equality contract, while a
// pure-attribute style renders as identity under the Ascii profile.
type uiStyles struct {
	title  lipgloss.Style
	label  lipgloss.Style
	hint   lipgloss.Style
	errSty lipgloss.Style
	gate   lipgloss.Style
	caret  lipgloss.Style
}

func newUIStyles(r *lipgloss.Renderer) uiStyles {
	return uiStyles{
		title:  r.NewStyle().Bold(true),
		label:  r.NewStyle().Faint(true),
		hint:   r.NewStyle().Faint(true),
		errSty: r.NewStyle().Foreground(lipgloss.Color("1")),
		gate:   r.NewStyle().Bold(true),
		caret:  r.NewStyle().Reverse(true),
	}
}

// stdoutStyles binds the production styles to os.Stdout, the writer bubbletea's
// default output uses, so profile detection matches the frame's terminal.
func stdoutStyles() uiStyles {
	return newUIStyles(lipgloss.NewRenderer(os.Stdout))
}
