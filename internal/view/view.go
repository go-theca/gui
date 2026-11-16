// SPDX-License-Identifier: BSD-3-Clause

// Package view is the View of the Theca GUI: it builds the widgets from
// go-widgets/toolkit, lays them out, and binds every one of them to the
// ViewModel (package viewmodel) through go-widgets/mvvmtk. It never reads or
// writes a widget's state itself; the binders own that, which is what the
// go-widgets/mvvmlint gate checks.
//
// The View is the root widget handed to go-widgets/window: it drains the
// queue the ViewModel's background work posts to at the start of every frame,
// on the UI goroutine, before the layout.
package view

import (
	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/mvvmtk"
	"github.com/go-widgets/toolkit"

	"github.com/go-theca/gui/internal/viewmodel"
)

// The window's size, in logical points.
const (
	Width  = 640
	Height = 580
)

// Layout, in pixels.
const (
	margin   = 16
	gap      = 6
	rowH     = 28
	labelW   = 150
	buttonH  = 30
	titleH   = 34
	msgRows  = 4
	msgRowH  = 18
	progress = 22
)

// maskRune is what a secret is shown as.
const maskRune = '•'

// View is the root widget.
type View struct {
	*toolkit.VBox

	app *viewmodel.App
	q   *mvvm.Queue

	stack *toolkit.Stack

	// dirty is raised by every binding when a ViewModel change reaches a
	// widget: a host that draws on demand repaints when it is set.
	dirty  bool
	unbind []func()
}

// New builds the View of app. q is the queue app posts to; the View drains it.
func New(app *viewmodel.App, q *mvvm.Queue) *View {
	// Anti-aliased, shaped text; the toolkit keeps its bitmap default on the
	// (impossible) error.
	_ = toolkit.UseOpenTypeText()

	v := &View{VBox: toolkit.NewVBox(), app: app, q: q, stack: toolkit.NewStack()}

	v.stack.AddPage(viewmodel.ScreenHome, v.home())
	v.stack.AddPage(viewmodel.ScreenCreate, v.create())
	v.stack.AddPage(viewmodel.ScreenRecovery, v.recovery())
	v.stack.AddPage(viewmodel.ScreenUnlock, v.unlock())
	v.stack.AddPage(viewmodel.ScreenContainer, v.container())
	v.unbind = append(v.unbind, mvvm.BindTwoWay(app.Screen, v.stack.Visible(), v.touch))

	// The status area: every error, notice and result, under whatever screen.
	messages := toolkit.NewListBox(nil)
	messages.RowHeight = msgRowH
	v.unbind = append(v.unbind, mvvmtk.BindListItems(messages, app.Messages, func(s string) string { return s }, v.touch))

	content := page()
	content.AddFlex(v.stack, 1)
	content.AddFixed(messages, msgRows*msgRowH)

	// The margin is made of spacers, not of an offset: a window delivers events
	// in the root's own coordinates, so the root must fill the surface.
	middle := toolkit.NewHBox()
	middle.Spacing = 0
	middle.AddFixed(toolkit.NewLabel(""), margin)
	middle.AddFlex(content, 1)
	middle.AddFixed(toolkit.NewLabel(""), margin)
	v.Spacing = 0
	v.AddFixed(toolkit.NewLabel(""), margin)
	v.AddFlex(middle, 1)
	v.AddFixed(toolkit.NewLabel(""), margin)
	return v
}

// SetBounds drains what background work has posted, then lays out. Every
// window back-end calls it before drawing a frame.
func (v *View) SetBounds(r toolkit.Rect) {
	v.q.Drain()
	v.VBox.SetBounds(r)
}

// Dirty reports whether a ViewModel change reached a widget since the last
// call, and clears the flag.
func (v *View) Dirty() bool {
	d := v.dirty
	v.dirty = false
	return d
}

// Close detaches every binding.
func (v *View) Close() {
	for _, u := range v.unbind {
		u()
	}
	v.unbind = nil
}

func (v *View) touch() { v.dirty = true }

// --- widget helpers ---------------------------------------------------------

// page is a vertical stack of rows.
func page() *toolkit.VBox {
	p := toolkit.NewVBox()
	p.Spacing = gap
	return p
}

func (v *View) title(text string) *toolkit.Label {
	return toolkit.NewLabel(text).SetFontSize(22)
}

func (v *View) text(text string) *toolkit.Label { return toolkit.NewLabel(text) }

// shown is a label that follows an observable.
func (v *View) shown(obs *mvvm.Observable[string]) *toolkit.Label {
	l := toolkit.NewLabel("")
	v.unbind = append(v.unbind, mvvmtk.BindLabel(l, obs, v.touch))
	return l
}

// field is an entry bound to an observable; mask hides what is typed.
func (v *View) field(obs *mvvm.Observable[string], placeholder string, mask rune) *toolkit.Entry {
	e := toolkit.NewEntry("")
	e.Placeholder = placeholder
	e.Mask = mask
	v.unbind = append(v.unbind, mvvmtk.BindEntryText(e, obs, v.touch))
	return e
}

// button is a button that runs a command, and is disabled when it cannot run.
func (v *View) button(label string, cmd *mvvm.Command) *toolkit.Button {
	b := toolkit.NewButton(label, nil)
	v.unbind = append(v.unbind, mvvmtk.BindCommand(b, cmd, v.touch))
	return b
}

// row is a label and a widget that takes the rest of the width.
func row(label string, w toolkit.Widget) *toolkit.HBox {
	h := toolkit.NewHBox()
	h.Spacing = gap
	h.AddFixed(toolkit.NewLabel(label), labelW)
	h.AddFlex(w, 1)
	return h
}

// buttons is a row of equal buttons.
func buttons(bs ...*toolkit.Button) *toolkit.HBox {
	h := toolkit.NewHBox()
	h.Spacing = gap
	for _, b := range bs {
		h.AddFlex(b, 1)
	}
	return h
}

// pinRow is the security key's PIN: a masked entry and its button, which is
// disabled until a key asks for the PIN.
func (v *View) pinRow() *toolkit.HBox {
	h := toolkit.NewHBox()
	h.Spacing = gap
	h.AddFixed(toolkit.NewLabel("Key PIN (if asked):"), labelW)
	h.AddFlex(v.field(v.app.PIN.Value, "only if the key asks for it", maskRune), 1)
	h.AddFixed(v.button("Submit PIN", v.app.PIN.Submit), 110)
	return h
}

// --- screens ----------------------------------------------------------------

func (v *View) home() toolkit.Widget {
	p := page()
	p.AddFixed(v.title("Theca"), titleH)
	p.AddFixed(v.text("Encrypted containers. Open one, or create a new one."), rowH)
	p.AddFixed(row("Container file:", v.field(v.app.Path, "/path/to/container.theca", 0)), rowH)
	p.AddFixed(buttons(
		v.button("Unlock container", v.app.GoUnlock),
		v.button("Create new container", v.app.GoCreate),
	), buttonH)
	return p
}

func (v *View) create() toolkit.Widget {
	c := v.app.Create
	p := page()
	p.AddFixed(v.title("Create a container"), titleH)
	p.AddFixed(row("File to create:", v.field(c.Path, "/path/to/new.theca", 0)), rowH)
	p.AddFixed(row("Size:", v.field(c.Size, "16M, 1G, or a multiple of 4096 bytes", 0)), rowH)

	// The group reports a click to the ViewModel; but it does not tell its
	// members which one the ViewModel's value selects, so each member's Checked
	// observable is kept in step with it here.
	group := toolkit.NewRadioGroup()
	for _, label := range viewmodel.PolicyLabels {
		r := toolkit.NewRadioButton(label)
		group.Add(r)
		p.AddFixed(row("", r), rowH)
	}
	v.unbind = append(v.unbind, mvvmtk.BindRadioGroup(group, c.Policy, v.touch))
	check := func(active int) {
		for i, r := range group.Members {
			r.Checked().Set(i == active)
		}
	}
	check(c.Policy.Get())
	v.unbind = append(v.unbind, c.Policy.Subscribe(check))

	p.AddFixed(row("Password:", v.field(c.Password, "", maskRune)), rowH)
	p.AddFixed(row("Repeat password:", v.field(c.Confirm, "", maskRune)), rowH)
	p.AddFixed(buttons(v.button("Create", c.Submit), v.button("Back", c.Back)), buttonH)
	return p
}

func (v *View) recovery() toolkit.Widget {
	c := v.app.Create
	p := page()
	p.AddFixed(v.title("Your recovery code"), titleH)
	p.AddFixed(v.shown(c.Recovery).SetFontSize(24), 40)
	p.AddFixed(v.text("Write it down now, on paper. It is shown once and never stored:"), rowH)
	p.AddFixed(v.text("nobody, including this program, can show it to you again."), rowH)

	ack := toolkit.NewCheckButton("I have written it down", false)
	v.unbind = append(v.unbind, mvvmtk.BindChecked(ack, c.Acked, v.touch))
	p.AddFixed(ack, rowH)
	p.AddFixed(buttons(v.button("Done", c.Done)), buttonH)
	return p
}

func (v *View) unlock() toolkit.Widget {
	u := v.app.Unlock
	p := page()
	p.AddFixed(v.title("Unlock"), titleH)
	p.AddFixed(row("Container:", v.shown(v.app.Path)), rowH)
	p.AddFixed(row("Password:", v.field(u.Password, "", maskRune)), rowH)
	p.AddFixed(row("Recovery code:", v.field(u.Recovery, "", maskRune)), rowH)

	key := toolkit.NewCheckButton("Use security key (FIDO2)", false)
	v.unbind = append(v.unbind, mvvmtk.BindChecked(key, u.UseKey, v.touch))
	p.AddFixed(row("", key), rowH)
	p.AddFixed(v.pinRow(), rowH)
	p.AddFixed(buttons(
		v.button("Unlock", u.Submit),
		v.button("Cancel", u.Cancel),
		v.button("Back", u.Back),
	), buttonH)
	return p
}

func (v *View) container() toolkit.Widget {
	c := v.app.Container
	p := page()
	p.AddFixed(v.title("Unlocked"), titleH)
	for _, info := range []*mvvm.Observable[string]{c.Size, c.Epoch, c.Slots, c.Journal, c.Rollback} {
		p.AddFixed(v.shown(info), msgRowH+2)
	}
	p.AddFixed(buttons(
		v.button("Verify", c.Verify),
		v.button("Enroll security key", c.EnrollKey),
		v.button("Close container", c.Close),
	), buttonH)

	bar := toolkit.NewProgressBar()
	v.unbind = append(v.unbind, mvvmtk.BindProgress(bar, c.Progress, v.touch))
	p.AddFixed(bar, progress)
	p.AddFixed(v.shown(c.Activity), msgRowH+2)

	imp := toolkit.NewHBox()
	imp.Spacing = gap
	imp.AddFlex(v.field(c.ImportPath, "file to import at offset 0", 0), 1)
	imp.AddFixed(v.button("Import", c.Import), 100)
	p.AddFixed(imp, rowH)

	exp := toolkit.NewHBox()
	exp.Spacing = gap
	exp.AddFlex(v.field(c.ExportPath, "new file for the decrypted volume", 0), 1)
	exp.AddFixed(v.button("Export", c.Export), 100)
	p.AddFixed(exp, rowH)

	p.AddFixed(v.text("Export never overwrites a file; it holds the volume in the clear."), msgRowH+2)
	p.AddFixed(v.pinRow(), rowH)
	return p
}
