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
	uiConfig *config.UIConfig
	title    string
	entries  []whichKeyEntry
	keyWidth int

	// Grid shape, computed by computeLayout: column count, row count, and each
	// column's rendered width (columns are sized to their own widest cell).
	cols      int
	rows      int
	colWidths []int
}

type whichKeyEntry struct {
	key   string // display for the next keystroke, e.g. "g" or "<C-r>"
	desc  string // annotation / cleaned command, or a group label
	group bool   // true if this key only opens deeper chords
}

const (
	whichKeyArrow   = " → " // between key and label, nvim which-key style
	whichKeyColGap  = 4      // between grid columns
	whichKeyDescCap = 30

	// Interior padding (inside the border) and a floor on the box size, so the
	// popover reads as a generous raised card rather than a tight label.
	whichKeyPadX = 3 // blank columns each side, inside the border
	whichKeyPadY = 1 // blank rows top and bottom, inside the border
	whichKeyMinW = 44
	whichKeyMinH = 9
)

var whichKeyArrowWidth = runewidth.StringWidth(whichKeyArrow)

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
		// Otherwise this key only opens deeper chords — label it with its
		// domain name plus how many bindings live under it, e.g. "buffer +7"
		// (press b to drill into the buffer hotkeys). The domain name comes
		// from which-key-groups; fall back to a bare count if unnamed.
		if desc == "" {
			isGroup = true
			if label, ok := uiConfig.WhichKeyGroups[key]; ok && label != "" {
				desc = fmt.Sprintf("%s +%d", label, len(g.bindings))
			} else {
				desc = fmt.Sprintf("+%d", len(g.bindings))
			}
		}
		wk.entries = append(wk.entries, whichKeyEntry{key: key, desc: desc, group: isGroup})
		wk.keyWidth = max(wk.keyWidth, runewidth.StringWidth(key))
	}
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

// cellWidth is the rendered width of one "key → label" cell.
func (wk *WhichKey) cellWidth(e whichKeyEntry) int {
	d := runewidth.StringWidth(e.desc)
	if d > whichKeyDescCap {
		d = whichKeyDescCap
	}
	return wk.keyWidth + whichKeyArrowWidth + d
}

// computeLayout chooses the fewest columns that keep the grid within maxRows
// rows (so the box stays short on a short pane, todui-style), then sizes each
// column to its own widest cell. Entries fill column-major (top to bottom, then
// the next column) like nvim's which-key, so each column is a contiguous sorted
// run. Results are stored on wk for both box sizing and drawing.
func (wk *WhichKey) computeLayout(maxRows int) {
	n := len(wk.entries)
	if maxRows < 1 {
		maxRows = 1
	}
	cols := (n + maxRows - 1) / maxRows
	if cols < 1 {
		cols = 1
	}
	rows := (n + cols - 1) / cols
	colWidths := make([]int, cols)
	for c := 0; c < cols; c++ {
		for r := 0; r < rows; r++ {
			i := c*rows + r
			if i >= n {
				break
			}
			if w := wk.cellWidth(wk.entries[i]); w > colWidths[c] {
				colWidths[c] = w
			}
		}
	}
	wk.cols, wk.rows, wk.colWidths = cols, rows, colWidths
}

// gridWidth is the total inner width of the laid-out grid.
func (wk *WhichKey) gridWidth() int {
	w := 0
	for _, cw := range wk.colWidths {
		w += cw
	}
	if len(wk.colWidths) > 1 {
		w += whichKeyColGap * (len(wk.colWidths) - 1)
	}
	return w
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

	gx, gy := 1+whichKeyPadX, 1+whichKeyPadY
	gw, gh := w-2-2*whichKeyPadX, h-2-2*whichKeyPadY
	if gw < 1 || gh < 1 {
		return
	}
	wk.drawGrid(ctx.Subcontext(gx, gy, gw, gh))
}

func (wk *WhichKey) drawGrid(ctx *ui.Context) {
	def := wk.uiConfig.GetStyle(config.STYLE_WHICHKEY_DEFAULT)
	keyStyle := wk.uiConfig.GetStyle(config.STYLE_WHICHKEY_KEY)
	groupStyle := wk.uiConfig.GetStyle(config.STYLE_WHICHKEY_GROUP)

	x := 0
	for c := 0; c < wk.cols; c++ {
		for r := 0; r < wk.rows; r++ {
			i := c*wk.rows + r
			if i >= len(wk.entries) || r >= ctx.Height() {
				break
			}
			e := wk.entries[i]
			// Right-align the key (todui's "{key:>N}") so arrows line up.
			key := runewidth.FillLeft(e.key, wk.keyWidth)
			desc := e.desc
			if runewidth.StringWidth(desc) > whichKeyDescCap {
				desc = runewidth.Truncate(desc, whichKeyDescCap, "…")
			}
			descStyle := def
			if e.group {
				descStyle = groupStyle
			}
			n := ctx.Printf(x, r, keyStyle, "%s", key)
			n = ctx.Printf(n, r, def, "%s", whichKeyArrow)
			ctx.Printf(n, r, descStyle, "%s", desc)
		}
		if c < len(wk.colWidths) {
			x += wk.colWidths[c] + whichKeyColGap
		}
	}
}

func (wk *WhichKey) Invalidate() {
	ui.Invalidate()
}
