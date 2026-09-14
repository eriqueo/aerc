package app

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"

	"git.sr.ht/~rjarry/aerc/config"
	"git.sr.ht/~rjarry/aerc/lib/ui"
	"git.sr.ht/~rockorager/vaxis"
)

type Selector struct {
	chooser  bool
	focused  bool
	focus    int
	options  []string
	uiConfig *config.UIConfig

	onChoose func(option string)
	onSelect func(option string)
}

func NewSelector(options []string, focus int, uiConfig *config.UIConfig) *Selector {
	return &Selector{
		focus:    focus,
		options:  options,
		uiConfig: uiConfig,
	}
}

func (sel *Selector) Chooser(chooser bool) *Selector {
	sel.chooser = chooser
	return sel
}

func (sel *Selector) Invalidate() {
	ui.Invalidate()
}

func (sel *Selector) Draw(ctx *ui.Context) {
	defaultSelectorStyle := sel.uiConfig.GetStyle(config.STYLE_SELECTOR_DEFAULT)
	w, h := ctx.Width(), ctx.Height()
	ctx.Fill(0, 0, w, h, ' ', defaultSelectorStyle)

	if w < 5 || h < 1 {
		// if width and height are that small, don't even try to draw
		// something
		return
	}

	y := 1
	if h == 1 {
		y = 0
	}

	format := "[%s]"

	calculateWidth := func(space int) int {
		neededWidth := 2
		for i, option := range sel.options {
			neededWidth += runewidth.StringWidth(fmt.Sprintf(format, option))
			if i < len(sel.options)-1 {
				neededWidth += space
			}
		}
		return neededWidth - space
	}

	space := 5
	for ; space > 0; space-- {
		if w > calculateWidth(space) {
			break
		}
	}

	x := 2
	for i, option := range sel.options {
		style := defaultSelectorStyle
		if sel.focus == i {
			if sel.focused {
				style = sel.uiConfig.GetStyle(config.STYLE_SELECTOR_FOCUSED)
			} else if sel.chooser {
				style = sel.uiConfig.GetStyle(config.STYLE_SELECTOR_CHOOSER)
			}
		}

		if space == 0 {
			if sel.focus == i {
				leftArrow, rightArrow := ' ', ' '
				if i > 0 {
					leftArrow = '❮'
				}
				if i < len(sel.options)-1 {
					rightArrow = '❯'
				}

				s := runewidth.Truncate(option,
					w-runewidth.RuneWidth(leftArrow)-runewidth.RuneWidth(rightArrow)-runewidth.StringWidth(fmt.Sprintf(format, "")),
					"…")

				nextPos := 0
				nextPos += ctx.Printf(nextPos, y, defaultSelectorStyle, "%c", leftArrow)
				nextPos += ctx.Printf(nextPos, y, style, format, s)
				ctx.Printf(nextPos, y, defaultSelectorStyle, "%c", rightArrow)
			}
		} else {
			x += ctx.Printf(x, y, style, format, option)
			x += space
		}
	}
}

func (sel *Selector) OnChoose(fn func(option string)) *Selector {
	sel.onChoose = fn
	return sel
}

func (sel *Selector) OnSelect(fn func(option string)) *Selector {
	sel.onSelect = fn
	return sel
}

func (sel *Selector) Select(option string) {
	for i, opt := range sel.options {
		if option == opt {
			sel.focus = i
			if sel.onSelect != nil {
				sel.onSelect(opt)
			}
			break
		}
	}
}

func (sel *Selector) Selected() string {
	return sel.options[sel.focus]
}

func (sel *Selector) Focus(focus bool) {
	sel.focused = focus
	sel.Invalidate()
}

func (sel *Selector) Event(event vaxis.Event) bool {
	if key, ok := event.(vaxis.Key); ok {
		switch {
		case key.Matches('h', vaxis.ModCtrl):
			fallthrough
		case key.Matches(vaxis.KeyLeft):
			if sel.focus > 0 {
				sel.focus--
				sel.Invalidate()
			}
			if sel.onSelect != nil {
				sel.onSelect(sel.Selected())
			}
		case key.Matches('l', vaxis.ModCtrl):
			fallthrough
		case key.Matches(vaxis.KeyRight):
			if sel.focus < len(sel.options)-1 {
				sel.focus++
				sel.Invalidate()
			}
			if sel.onSelect != nil {
				sel.onSelect(sel.Selected())
			}
		case key.Matches(vaxis.KeyEnter):
			if sel.onChoose != nil {
				sel.onChoose(sel.Selected())
			}
		}
	}
	return false
}

var ErrNoOptionSelected = fmt.Errorf("no option selected")

type SelectorDialog struct {
	callback func(string, error)
	title    string
	prompt   string
	uiConfig *config.UIConfig
	selector *Selector
}

const (
	selectorDialogMargin   = 4
	selectorDialogMinWidth = 44
	selectorDialogMaxWidth = 84
	selectorDialogPadX     = 2
	selectorDialogLegend   = "←/→ choose · enter confirm · esc cancel"
)

func NewSelectorDialog(title string, prompt string, options []string, focus int,
	uiConfig *config.UIConfig, cb func(string, error),
) *SelectorDialog {
	sd := &SelectorDialog{
		callback: cb,
		title:    title,
		prompt:   strings.TrimSpace(prompt),
		uiConfig: uiConfig,
		selector: NewSelector(options, focus, uiConfig).Chooser(true),
	}
	sd.selector.Focus(true)
	return sd
}

func (gp *SelectorDialog) Draw(ctx *ui.Context) {
	defaultStyle := gp.uiConfig.GetStyle(config.STYLE_SELECTOR_DEFAULT)
	borderStyle := gp.uiConfig.GetStyle(config.STYLE_SELECTOR_BORDER)
	titleStyle := gp.uiConfig.GetStyle(config.STYLE_SELECTOR_TITLE)
	hintStyle := gp.uiConfig.GetStyle(config.STYLE_SELECTOR_HINT)
	w, h := ctx.Width(), ctx.Height()

	ctx.Fill(0, 0, w, h, ' ', defaultStyle)
	if w < 2 || h < 2 {
		return
	}

	ctx.Fill(0, 0, 1, h, '║', borderStyle)
	ctx.Fill(w-1, 0, 1, h, '║', borderStyle)
	ctx.Printf(0, 0, borderStyle, "╔%s╗", strings.Repeat("═", w-2))
	ctx.Printf(0, h-1, borderStyle, "╚%s╝", strings.Repeat("═", w-2))
	if gp.title != "" && w > 6 {
		title := runewidth.Truncate(gp.title, w-6, "…")
		ctx.Printf(2, 0, titleStyle, " %s ", title)
	}
	legendWidth := runewidth.StringWidth(selectorDialogLegend)
	if w > legendWidth+4 {
		ctx.Printf((w-legendWidth-2)/2, h-1, hintStyle,
			" %s ", selectorDialogLegend)
	}

	lines := strings.Split(gp.prompt, "\n")
	for i, line := range lines {
		y := 2 + i
		if y >= h-3 {
			break
		}
		contentWidth := max(w-2*(selectorDialogPadX+1), 0)
		line = runewidth.Truncate(line, contentWidth, "…")
		ctx.Printf(selectorDialogPadX+1, y, defaultStyle, "%s", line)
	}
	selectorY := h - 3
	if selectorY > 0 {
		gp.selector.Draw(ctx.Subcontext(1, selectorY, w-2, 1))
	}
}

func (gp *SelectorDialog) desiredWidth() int {
	width := selectorDialogMinWidth
	width = max(width, runewidth.StringWidth(gp.title)+6)
	for _, line := range strings.Split(gp.prompt, "\n") {
		width = max(width, runewidth.StringWidth(line)+2*(selectorDialogPadX+1))
	}

	optionsWidth := 5
	for i, option := range gp.selector.options {
		optionsWidth += runewidth.StringWidth(option) + 2
		if i < len(gp.selector.options)-1 {
			optionsWidth += 5
		}
	}
	width = max(width, optionsWidth)
	width = max(width, runewidth.StringWidth(selectorDialogLegend)+4)
	return min(width, selectorDialogMaxWidth)
}

func (gp *SelectorDialog) widthFor(available int) int {
	if available <= 2*selectorDialogMargin {
		return max(available, 0)
	}
	return min(gp.desiredWidth(), available-2*selectorDialogMargin)
}

func (gp *SelectorDialog) ContextWidth() (func(int) int, func(int) int) {
	start := func(available int) int {
		return max((available-gp.widthFor(available))/2, 0)
	}
	width := func(available int) int { return gp.widthFor(available) }
	return start, width
}

func (gp *SelectorDialog) ContextHeight() (func(int) int, func(int) int) {
	totalHeight := strings.Count(gp.prompt, "\n") + 7
	start := func(available int) int {
		return max((available-min(totalHeight, available))/2, 0)
	}
	height := func(available int) int {
		return min(totalHeight, available)
	}
	return start, height
}

func (gp *SelectorDialog) Invalidate() {
	ui.Invalidate()
}

func (gp *SelectorDialog) Event(event vaxis.Event) bool {
	switch event := event.(type) {
	case vaxis.Key:
		switch {
		case event.Matches(vaxis.KeyEnter):
			gp.selector.Focus(false)
			gp.callback(gp.selector.Selected(), nil)
		case event.Matches(vaxis.KeyEsc):
			gp.selector.Focus(false)
			gp.callback("", ErrNoOptionSelected)
		default:
			gp.selector.Event(event)
		}
	default:
		gp.selector.Event(event)
	}
	return true
}

func (gp *SelectorDialog) Focus(f bool) {
	gp.selector.Focus(f)
}
