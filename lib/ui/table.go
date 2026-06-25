package ui

import (
	"math"
	"regexp"

	"git.sr.ht/~rjarry/aerc/config"
	"git.sr.ht/~rockorager/vaxis"
	"github.com/mattn/go-runewidth"
)

type Table struct {
	Columns []Column
	Rows    []Row
	Height  int
	// Optional callback that allows customizing the default drawing routine
	// of table rows. If true is returned, the default routine is skipped.
	CustomDraw func(t *Table, row int, c *Context) bool
	// Optional callback that allows returning a custom style for the row.
	GetRowStyle func(t *Table, row int) vaxis.Style

	// true if at least one column has WIDTH_FIT
	autoFitWidths bool
	// if false, widths need to be computed before drawing
	widthsComputed bool
}

type Column struct {
	Offset    int
	Width     int
	Def       *config.ColumnDef
	Separator string
}

type Row struct {
	Cells []string
	Priv  any
}

func NewTable(
	height int,
	columnDefs []*config.ColumnDef, separator string,
	customDraw func(*Table, int, *Context) bool,
	getRowStyle func(*Table, int) vaxis.Style,
) Table {
	if customDraw == nil {
		customDraw = func(*Table, int, *Context) bool { return false }
	}
	if getRowStyle == nil {
		getRowStyle = func(*Table, int) vaxis.Style {
			return vaxis.Style{}
		}
	}
	columns := make([]Column, len(columnDefs))
	autoFitWidths := false
	for c, col := range columnDefs {
		if col.Flags.Has(config.WIDTH_FIT) {
			autoFitWidths = true
		}
		columns[c] = Column{Def: col}
		if c != len(columns)-1 {
			// set separator for all columns except the last one
			columns[c].Separator = separator
		}
	}
	return Table{
		Columns:       columns,
		Height:        height,
		CustomDraw:    customDraw,
		GetRowStyle:   getRowStyle,
		autoFitWidths: autoFitWidths,
	}
}

// add a row to the table, returns true when the table is full
func (t *Table) AddRow(cells []string, priv any) bool {
	if len(cells) != len(t.Columns) {
		panic("invalid number of cells")
	}
	if len(t.Rows) >= t.Height {
		return true
	}
	t.Rows = append(t.Rows, Row{Cells: cells, Priv: priv})
	if t.autoFitWidths {
		t.widthsComputed = false
	}
	return len(t.Rows) >= t.Height
}

func (t *Table) computeWidths(width int) {
	contentMaxWidths := make([]int, len(t.Columns))
	if t.autoFitWidths {
		for _, row := range t.Rows {
			for c := range t.Columns {
				buf := StyledString(row.Cells[c])
				if buf.Len() > contentMaxWidths[c] {
					contentMaxWidths[c] = buf.Len()
				}
			}
		}
	}

	nonFixed := width
	autoWidthCount := 0
	for c := range t.Columns {
		col := &t.Columns[c]
		switch {
		case col.Def.Flags.Has(config.WIDTH_FIT):
			col.Width = contentMaxWidths[c]
			// compensate for exact width columns
			col.Width += runewidth.StringWidth(col.Separator)
		case col.Def.Flags.Has(config.WIDTH_EXACT):
			col.Width = int(math.Round(col.Def.Width))
			// compensate for exact width columns
			col.Width += runewidth.StringWidth(col.Separator)
		case col.Def.Flags.Has(config.WIDTH_AUTO):
			col.Width = 0
			autoWidthCount += 1
		case col.Def.Flags.Has(config.WIDTH_FRACTION):
			col.Width = int(math.Round(float64(width) * col.Def.Width))
		}
		nonFixed -= col.Width
	}

	autoWidth := 0
	if autoWidthCount > 0 && nonFixed > 0 {
		autoWidth = nonFixed / autoWidthCount
		if autoWidth == 0 {
			autoWidth = 1
		}
	}

	offset := 0
	remain := width
	for c := range t.Columns {
		col := &t.Columns[c]
		if col.Def.Flags.Has(config.WIDTH_AUTO) && autoWidth > 0 {
			col.Width = autoWidth
			if nonFixed >= 2*autoWidth {
				nonFixed -= autoWidth
			}
		}
		if remain == 0 {
			// column is outside of screen
			col.Width = -1
		} else if col.Width > remain {
			// limit width to avoid overflow
			col.Width = remain
		}
		remain -= col.Width
		col.Offset = offset
		offset += col.Width
		// reserve room for separator
		col.Width -= runewidth.StringWidth(col.Separator)
	}
}

// centerCell centers plain text within width, truncating with an ellipsis if it
// doesn't fit. Used for column headers.
func centerCell(s string, width int) string {
	w := runewidth.StringWidth(s)
	switch {
	case w > width:
		return runewidth.Truncate(s, width, "…")
	case w < width:
		left := (width - w) / 2
		s = runewidth.FillLeft(s, w+left)
		return runewidth.FillRight(s, width)
	default:
		return s
	}
}

var metaCharsRegexp = regexp.MustCompile(`[\t\r\f\n\v]`)

func (col *Column) alignCell(cell string) string {
	cell = metaCharsRegexp.ReplaceAllString(cell, " ")
	buf := StyledString(cell)
	width := buf.Len()

	switch {
	case col.Def.Flags.Has(config.ALIGN_LEFT):
		if width < col.Width {
			PadRight(buf, col.Width)
			cell = buf.Encode()
		} else if width > col.Width {
			Truncate(buf, col.Width)
			cell = buf.Encode()
		}
	case col.Def.Flags.Has(config.ALIGN_CENTER):
		if width < col.Width {
			pad := col.Width - width
			PadLeft(buf, col.Width-(pad/2))
			PadRight(buf, col.Width)
			cell = buf.Encode()
		} else if width > col.Width {
			Truncate(buf, col.Width)
			cell = buf.Encode()
		}
	case col.Def.Flags.Has(config.ALIGN_RIGHT):
		if width < col.Width {
			PadLeft(buf, col.Width)
			cell = buf.Encode()
		} else if width > col.Width {
			TruncateHead(buf, col.Width)
			cell = buf.Encode()
		}
	}

	return cell
}

// DrawHeader draws a single row labelling each column with its Def.Name, using
// the exact column geometry (offsets, widths, separators) the body rows use, so
// the header lines up with the data beneath it. Labels are centered within
// their column regardless of the column's own data alignment. The caller
// positions the row (typically a 1-high subcontext directly above the body) and
// supplies the style. Column widths are computed lazily and shared with the
// body Draw, so rows must already be added before calling this.
func (t *Table) DrawHeader(ctx *Context, style vaxis.Style) {
	if !t.widthsComputed {
		t.computeWidths(ctx.Width())
		t.widthsComputed = true
	}
	ctx.Fill(0, 0, ctx.Width(), 1, ' ', style)
	for _, col := range t.Columns {
		if col.Width == -1 {
			// column overflows screen width
			continue
		}
		cell := centerCell(col.Def.Name, col.Width)
		ctx.Printf(col.Offset, 0, style, "%s%s", cell, col.Separator)
	}
}

func (t *Table) Draw(ctx *Context) {
	if !t.widthsComputed {
		t.computeWidths(ctx.Width())
		t.widthsComputed = true
	}
	for r, row := range t.Rows {
		if t.CustomDraw(t, r, ctx) {
			continue
		}
		for c, col := range t.Columns {
			if col.Width == -1 {
				// column overflows screen width
				continue
			}
			cell := col.alignCell(row.Cells[c])
			style := t.GetRowStyle(t, r)

			buf := StyledString(cell)
			ApplyAttrs(buf, style)
			cell = buf.Encode()
			ctx.Printf(col.Offset, r, style, "%s%s", cell, col.Separator)
		}
	}
}
