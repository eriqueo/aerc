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
// that lists the possible next keystrokes and their labels — narrowing as more
// of the chord is typed. It draws its own bordered, titled box (the todui
// leader-menu look) and themes via the whichkey_* styleset objects. Enabled via
// the [ui] which-key option.
type WhichKey struct {
	uiConfig  *config.UIConfig
	title     string
	entries   []whichKeyEntry
	keyWidth  int
	descWidth int
}

type whichKeyEntry struct {
	key   string // display for the next keystroke, e.g. "g" or "<C-r>"
	desc  string // annotation / cleaned command, or a group label
	group bool   // true if this key only opens deeper chords
}

const (
	whichKeySep     = "  " // between key and label, todui-style "{key}  {label}"
	whichKeyColGap  = 3     // between grid columns
	whichKeyDescCap = 26
)

// newWhichKey groups the matching bindings by their next keystroke (the one
// immediately following the already-typed prefix) and builds a sorted,
// renderable entry per distinct next key. prefixLen is len(pendingKeys).
func newWhichKey(uiConfig *config.UIConfig, matches []*config.Binding, prefixLen int) *WhichKey {
	type group struct{ bindings []*config.Binding }
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

	wk := &WhichKey{uiConfig: uiConfig}
	for _, key := range order {
		g := groups[key]
		desc, isGroup := "", false
		// Prefer a binding that terminates right after this keystroke: its
		// annotation (or a cleaned command) is the label.
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
		// Otherwise this key only opens deeper chords — label it from
		// which-key-groups if we have a name, else fall back to a count.
		if desc == "" {
			isGroup = true
			if label, ok := uiConfig.WhichKeyGroups[key]; ok && label != "" {
				desc = label
			} else {
				desc = fmt.Sprintf("+%d", len(g.bindings))
			}
		}
		wk.entries = append(wk.entries, whichKeyEntry{key: key, desc: desc, group: isGroup})
		wk.keyWidth = max(wk.keyWidth, runewidth.StringWidth(key))
		wk.descWidth = max(wk.descWidth, runewidth.StringWidth(desc))
	}
	wk.descWidth = min(wk.descWidth, whichKeyDescCap)
	return wk
}

// cleanCommand turns a raw command into a compact label for bindings that carry
// no annotation: drop the leading ":" and trailing "<Enter>", collapse store
// paths to their basename, and truncate. Annotated bindings never reach this.
func cleanCommand(out string) string {
	out = strings.TrimSpace(out)
	out = strings.TrimSuffix(out, "<Enter>")
	out = strings.TrimSpace(out)
	out = strings.TrimPrefix(out, ":")
	// ":term /nix/store/…/bin/foo …" reads better as just "foo".
	if fields := strings.Fields(out); len(fields) >= 2 && strings.Contains(fields[1], "/") {
		seg := strings.Split(fields[1], "/")
		out = fields[0] + " " + seg[len(seg)-1]
	}
	if runewidth.StringWidth(out) > whichKeyDescCap {
		out = runewidth.Truncate(out, whichKeyDescCap, "…")
	}
	return out
}

// cellWidth is the width of one "key  label" cell.
func (wk *WhichKey) cellWidth() int {
	return wk.keyWidth + runewidth.StringWidth(whichKeySep) + wk.descWidth
}

// layout returns the column count and row count for the given inner width.
func (wk *WhichKey) layout(innerWidth int) (cols, rows int) {
	cell := wk.cellWidth()
	cols = (innerWidth + whichKeyColGap) / (cell + whichKeyColGap)
	if cols < 1 {
		cols = 1
	}
	if cols > len(wk.entries) {
		cols = len(wk.entries)
	}
	rows = (len(wk.entries) + cols - 1) / cols
	return cols, rows
}

// Draw renders the full bordered box into ctx (sized to the box by the caller).
func (wk *WhichKey) Draw(ctx *ui.Context) {
	border := wk.uiConfig.GetStyle(config.STYLE_WHICHKEY_BORDER)
	titleStyle := wk.uiConfig.GetStyle(config.STYLE_WHICHKEY_TITLE)
	def := wk.uiConfig.GetStyle(config.STYLE_WHICHKEY_DEFAULT)

	w, h := ctx.Width(), ctx.Height()
	ctx.Fill(0, 0, w, h, ' ', def)
	if w < 2 || h < 2 {
		return
	}
	// Rounded border.
	ctx.Fill(0, 0, 1, h, '│', border)
	ctx.Fill(w-1, 0, 1, h, '│', border)
	ctx.Printf(0, 0, border, "╭%s╮", strings.Repeat("─", w-2))
	ctx.Printf(0, h-1, border, "╰%s╯", strings.Repeat("─", w-2))
	if wk.title != "" && w > 6 {
		ctx.Printf(2, 0, titleStyle, " %s ", runewidth.Truncate(wk.title, w-6, "…"))
	}

	wk.drawGrid(ctx.Subcontext(1, 1, w-2, h-2))
}

func (wk *WhichKey) drawGrid(ctx *ui.Context) {
	def := wk.uiConfig.GetStyle(config.STYLE_WHICHKEY_DEFAULT)
	keyStyle := wk.uiConfig.GetStyle(config.STYLE_WHICHKEY_KEY)
	groupStyle := wk.uiConfig.GetStyle(config.STYLE_WHICHKEY_GROUP)

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
		if runewidth.StringWidth(desc) > wk.descWidth {
			desc = runewidth.Truncate(desc, wk.descWidth, "…")
		}
		descStyle := def
		if e.group {
			descStyle = groupStyle
		}
		n := ctx.Printf(x, row, keyStyle, "%s", key)
		n = ctx.Printf(n, row, def, "%s", whichKeySep)
		ctx.Printf(n, row, descStyle, "%s", desc)
	}
}

func (wk *WhichKey) Invalidate() {
	ui.Invalidate()
}
