package app

import (
	"fmt"
	"sort"

	"github.com/mattn/go-runewidth"

	"git.sr.ht/~rjarry/aerc/config"
	"git.sr.ht/~rjarry/aerc/lib/ui"
)

// WhichKey is a non-interactive popover, shown while a key chord is pending,
// that lists the possible next keystrokes and their annotations — narrowing as
// more of the chord is typed. It mirrors the completion popover primitive
// (lib/ui/popover.go) and reuses the completion_* styleset entries so it looks
// consistent with tab-completion. Enabled via the [ui] which-key option.
type WhichKey struct {
	uiConfig *config.UIConfig
	entries  []whichKeyEntry
	keyWidth int
}

type whichKeyEntry struct {
	key  string // display for the next keystroke, e.g. "g" or "<C-r>"
	desc string // annotation, resolved command, or "+N" for a deeper sub-chord
}

// newWhichKey groups the matching bindings by their next keystroke (the one
// immediately following the already-typed prefix) and builds a sorted,
// renderable entry per distinct next key. prefixLen is len(pendingKeys).
func newWhichKey(uiConfig *config.UIConfig, matches []*config.Binding, prefixLen int) *WhichKey {
	type group struct {
		stroke   config.KeyStroke
		bindings []*config.Binding
	}
	order := []string{}
	groups := map[string]*group{}

	for _, b := range matches {
		if len(b.Input) <= prefixLen {
			continue
		}
		stroke := b.Input[prefixLen]
		key := config.FormatKeyStrokes([]config.KeyStroke{stroke})
		g, ok := groups[key]
		if !ok {
			g = &group{stroke: stroke}
			groups[key] = g
			order = append(order, key)
		}
		g.bindings = append(g.bindings, b)
	}

	sort.Strings(order)

	wk := &WhichKey{uiConfig: uiConfig}
	for _, key := range order {
		g := groups[key]
		desc := ""
		// Prefer a binding that terminates right after this keystroke: its
		// annotation (or, lacking one, the command it maps to) is the label.
		for _, b := range g.bindings {
			if len(b.Input) == prefixLen+1 {
				if b.Annotation != "" {
					desc = b.Annotation
				} else {
					desc = config.FormatKeyStrokes(b.Output)
				}
				break
			}
		}
		// Otherwise this key only opens deeper chords — show it as a sub-menu.
		if desc == "" {
			desc = fmt.Sprintf("+%d", len(g.bindings))
		}
		wk.entries = append(wk.entries, whichKeyEntry{key: key, desc: desc})
		wk.keyWidth = max(wk.keyWidth, runewidth.StringWidth(key))
	}
	return wk
}

// width returns the desired popover width for the current entries.
func (wk *WhichKey) width() int {
	descWidth := 0
	for _, e := range wk.entries {
		descWidth = max(descWidth, runewidth.StringWidth(e.desc))
	}
	descWidth = min(descWidth, 60)
	// " key " pill + " desc" + trailing pad
	return wk.keyWidth + 2 + 1 + descWidth + 2
}

func (wk *WhichKey) Draw(ctx *ui.Context) {
	bg := wk.uiConfig.GetStyle(config.STYLE_COMPLETION_DEFAULT)
	keyStyle := wk.uiConfig.GetStyle(config.STYLE_COMPLETION_PILL)
	descStyle := wk.uiConfig.GetStyle(config.STYLE_COMPLETION_DESCRIPTION)

	ctx.Fill(0, 0, ctx.Width(), ctx.Height(), ' ', bg)

	for i, e := range wk.entries {
		if i >= ctx.Height() {
			break
		}
		key := runewidth.FillRight(e.key, wk.keyWidth)
		x := ctx.Printf(0, i, keyStyle, " %s ", key)
		ctx.Printf(x, i, descStyle, " %s", e.desc)
	}
}

func (wk *WhichKey) Invalidate() {
	ui.Invalidate()
}
