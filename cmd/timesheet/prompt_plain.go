package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// plainPrompter is the SAFE line-based interaction. It reads from an injected
// reader ONE line at a time and writes plain (no ANSI) prompts. It never uses
// huh's accessible mode: an EOF at any prompt is an ABORT (never a silent
// default), and it never installs its own signal handler, so an interrupt
// terminates the process without any pending write (all mutations happen only
// after the deliberate final confirmation).
//
// The bufio.Reader is created ONCE per session and reused for every prompt, so a
// line already buffered is never lost between prompts.
type plainPrompter struct {
	in  *bufio.Reader
	out io.Writer
}

func newPlainPrompter(in io.Reader, out io.Writer) *plainPrompter {
	return &plainPrompter{in: bufio.NewReader(in), out: out}
}

func (p *plainPrompter) Choose(ctx context.Context, title string, options []promptOption, def int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, errAborted
	}
	fmt.Fprintln(p.out, title)
	for i, o := range options {
		line := fmt.Sprintf("  %d) %s", i+1, o.Label)
		if o.Hint != "" {
			line += "  (" + o.Hint + ")"
		}
		fmt.Fprintln(p.out, line)
	}
	for {
		raw, err := p.readLine(ctx, fmt.Sprintf("choose [%d]: ", def+1))
		if err != nil {
			return 0, err
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return def, nil
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > len(options) {
			fmt.Fprintf(p.out, "enter a number between 1 and %d\n", len(options))
			continue
		}
		return n - 1, nil
	}
}

func (p *plainPrompter) MultiChoose(ctx context.Context, title string, options []promptOption) ([]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, errAborted
	}
	fmt.Fprintln(p.out, title)
	for i, o := range options {
		line := fmt.Sprintf("  %d) %s", i+1, o.Label)
		if o.Hint != "" {
			line += "  (" + o.Hint + ")"
		}
		fmt.Fprintln(p.out, line)
	}
	raw, err := p.readLine(ctx, "choose (comma-separated, blank for none): ")
	if err != nil {
		return nil, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	seen := map[int]bool{}
	var out []int
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > len(options) {
			fmt.Fprintf(p.out, "ignoring out-of-range choice %q\n", strings.TrimSpace(part))
			continue
		}
		if seen[n-1] {
			continue
		}
		seen[n-1] = true
		out = append(out, n-1)
	}
	return out, nil
}

func (p *plainPrompter) Input(ctx context.Context, title, placeholder, initial string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", errAborted
	}
	prompt := title
	if placeholder != "" {
		prompt += " (" + placeholder + ")"
	}
	if initial != "" {
		prompt += " [" + initial + "]"
	}
	raw, err := p.readLine(ctx, prompt+": ")
	if err != nil {
		return "", err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return initial, nil
	}
	return raw, nil
}

func (p *plainPrompter) Confirm(ctx context.Context, title string, def bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, errAborted
	}
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	for {
		raw, err := p.readLine(ctx, title+" "+hint+": ")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintln(p.out, "enter y or n")
		}
	}
}

// readLine writes the prompt and reads one line. An EOF with NO data is an
// ABORT, never a default; a final line that ends without a newline is still a
// valid answer.
func (p *plainPrompter) readLine(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", errAborted
	}
	fmt.Fprint(p.out, prompt)
	line, err := p.in.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && strings.TrimSpace(line) != "" {
			return strings.TrimRight(line, "\r\n"), nil
		}
		return "", errAborted
	}
	return strings.TrimRight(line, "\r\n"), nil
}
