package main

import (
	"context"
	"io"

	"charm.land/huh/v2"
)

// tuiPrompter is the huh-backed terminal interaction. It is only ever constructed
// when BOTH stdin and stdout are real terminals and the environment is not CI.
//
// Safety rules (the reasons huh's accessible mode is NOT used as the plain path):
//   - Accessible mode reads stdin and writes stdout and silently accepts a default
//     on EOF. The plain path is therefore hand-written (see plainPrompter), not a
//     huh accessible form.
//   - huh ignores the context while a read is blocking; a cancelled context is
//     handled by RunWithContext returning an error, which we map to ABORT.
//   - A field error or any form error is mapped to ABORT, never a silent default.
type tuiPrompter struct {
	in  io.Reader
	out io.Writer
}

func newTUIPrompter(in io.Reader, out io.Writer) *tuiPrompter {
	return &tuiPrompter{in: in, out: out}
}

func (t *tuiPrompter) run(ctx context.Context, field huh.Field) error {
	form := huh.NewForm(huh.NewGroup(field)).WithInput(t.in).WithOutput(t.out)
	if err := form.RunWithContext(ctx); err != nil {
		// ErrUserAborted (ctrl-c/escape) and ErrTimeout (context cancel) both
		// mean "no deliberate answer": abort with no write.
		return errAborted
	}
	return nil
}

func (t *tuiPrompter) Choose(ctx context.Context, title string, options []promptOption, def int) (int, error) {
	opts := make([]huh.Option[int], 0, len(options))
	for i, o := range options {
		label := o.Label
		if o.Hint != "" {
			label += "  (" + o.Hint + ")"
		}
		opts = append(opts, huh.NewOption(label, i))
	}
	choice := def
	if err := t.run(ctx, huh.NewSelect[int]().Title(title).Options(opts...).Value(&choice)); err != nil {
		return 0, err
	}
	return choice, nil
}

func (t *tuiPrompter) MultiChoose(ctx context.Context, title string, options []promptOption) ([]int, error) {
	opts := make([]huh.Option[int], 0, len(options))
	for i, o := range options {
		label := o.Label
		if o.Hint != "" {
			label += "  (" + o.Hint + ")"
		}
		opts = append(opts, huh.NewOption(label, i))
	}
	var choice []int
	if err := t.run(ctx, huh.NewMultiSelect[int]().Title(title).Options(opts...).Value(&choice)); err != nil {
		return nil, err
	}
	return choice, nil
}

func (t *tuiPrompter) Input(ctx context.Context, title, placeholder, initial string) (string, error) {
	value := initial
	field := huh.NewInput().Title(title).Value(&value)
	if placeholder != "" {
		field = field.Placeholder(placeholder)
	}
	if err := t.run(ctx, field); err != nil {
		return "", err
	}
	return value, nil
}

func (t *tuiPrompter) Confirm(ctx context.Context, title string, def bool) (bool, error) {
	value := def
	if err := t.run(ctx, huh.NewConfirm().Title(title).Value(&value)); err != nil {
		return false, err
	}
	return value, nil
}
