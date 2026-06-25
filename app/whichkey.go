package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattn/go-runewidth"

	"git.sr.ht/~rjarry/aerc/config"
	"git.sr.ht/~rjarry/aerc/lib/ui"
)

// WhichKey is a non-interactive popover, shown while a key chord is pending,
// that lists the possible next keystrokes and their annotations — narrowing as
// more of the chord is typed. Entries are laid out as a multi-column grid of
// "key → description" cells that fills the available width, like Neovim's
// which-key. It reuses the completion_* styleset so it tracks the user's theme.
// Enabled via the [ui] which-key option.
type WhichKey struct {
	uiConfig *config.UIConfig
	entries  []whichKeyEntry
	keyWidth int
	descCap  int
}

type whichKeyEntry struct {
	key  string // display for the next keystroke, e.g. "g" or "<C-r>"
	desc string // annotation, cleaned command, or "+N" for a deeper sub-chord
}

const (
	whichKeySep     = "  " // between key and label, todui-style "  {key}  {label}"
	whichKeyColGap  = 3     // between grid columns
	whichKeyDescCap = 28
)

// newWhichKey groups the matching bindings by their next keystroke (the one
// immediately following the already-typed prefix) and builds a sorted,
// renderable entry per distinct next key. prefixLen is len(pendingKeys).
func newWhichKey(uiConfig *config.UIConfig, matches []*config.Binding, prefixLen int) *WhichKey {
	type group struct {
		bindings []*config.Binding
	}
	order := []string{}
	groups := map[string]*group{}

	for _, b := range matches {
		if len(b.Input) <= prefixLen {
			continue
		}
		key := config.FormatKeyStrokes(b.Input[prefixLen : prefixLen+1])
		g, ok := groups[key]
		if !ok {
			g = &group{}
			groups[key] = g
			order = append(order, key)
		}
		g.bindings = append(g.bindings, b)
	}

	sort.Strings(order)

	wk := &WhichKey{uiConfig: uiConfig, descCap: whichKeyDescCap}
	for _, key := range order {
		g := groups[key]
		desc := ""
		// Prefer a binding that terminates right after this keystroke: its
		// annotation (or, lacking one, a cleaned form of the command) is the
		// label.
		for _, b := range g.bindings {
			if len(b.Input) == prefixLen+1 {
				if b.Annotation != "" {
					desc = b.Annotation
				} else {
					desc = cleanCommand(config.FormatKeyStrokes(b.Output))
				}
				break
			}
		}
		// Otherwise this key only opens deeper chords — show it as a sub-menu,
		// matching Neovim which-key's "+N" group hint.
		if desc == "" {
			desc = fmt.Sprintf("+%d", len(g.bindings))
		}
		wk.entries = append(wk.entries, whichKeyEntry{key: key, desc: desc})
		wk.keyWidth = max(wk.keyWidth, runewidth.StringWidth(key))
	}
	return wk
}

// cleanCommand turns a raw command output into a compact label for bindings
// that carry no annotation: drop the leading ":" and trailing "<Enter>", and
// truncate. Annotated bindings never reach this.
func cleanCommand(out string) string {
	out = strings.TrimSpace(out)
	out = strings.TrimSuffix(out, "<Enter>")
	out = strings.TrimPrefix(out, ":")
	out = strings.TrimSpace(out)
	if runewidth.StringWidth(out) > whichKeyDescCap {
		out = runewidth.Truncate(out, whichKeyDescCap, "…")
	}
	return out
}

// cellWidth is the width of one "key  desc" cell.
func (wk *WhichKey) cellWidth() int {
	descWidth := 0
	for _, e := range wk.entries {
		descWidth = max(descWidth, runewidth.StringWidth(e.desc))
	}
	descWidth = min(descWidth, wk.descCap)
	return wk.keyWidth + runewidth.StringWidth(whichKeySep) + descWidth
}

// layout returns the column count and row count for the given total width.
func (wk *WhichKey) layout(totalWidth int) (cols, rows int) {
	cell := wk.cellWidth()
	cols = (totalWidth + whichKeyColGap) / (cell + whichKeyColGap)
	if cols < 1 {
		cols = 1
	}
	rows = (len(wk.entries) + cols - 1) / cols
	return cols, rows
}

func (wk *WhichKey) Draw(ctx *ui.Context) {
	bg := wk.uiConfig.GetStyle(config.STYLE_COMPLETION_DEFAULT)
	keyStyle := wk.uiConfig.GetStyle(config.STYLE_COMPLETION_PILL)
	descStyle := wk.uiConfig.GetStyle(config.STYLE_COMPLETION_DESCRIPTION)

	ctx.Fill(0, 0, ctx.Width(), ctx.Height(), ' ', bg)

	cols, _ := wk.layout(ctx.Width())
	cell := wk.cellWidth()
	for i, e := range wk.entries {
		row := i / cols
		col := i % cols
		if row >= ctx.Height() {
			break
		}
		x := col * (cell + whichKeyColGap)
		// Right-align the key (todui's "{key:>N}") so labels line up.
		key := runewidth.FillLeft(e.key, wk.keyWidth)
		desc := e.desc
		if runewidth.StringWidth(desc) > wk.descCap {
			desc = runewidth.Truncate(desc, wk.descCap, "…")
		}
		n := ctx.Printf(x, row, keyStyle, "%s", key)
		n = ctx.Printf(n, row, bg, "%s", whichKeySep)
		ctx.Printf(n, row, descStyle, "%s", desc)
	}
}

func (wk *WhichKey) Invalidate() {
	ui.Invalidate()
}
