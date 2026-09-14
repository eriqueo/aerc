package app

import "testing"

func testSelectorDialog(title, prompt string, options []string) *SelectorDialog {
	return NewSelectorDialog(title, prompt, options, 0, nil, nil)
}

func TestSelectorDialogWidthIsCompactAndCentered(t *testing.T) {
	dialog := testSelectorDialog(
		"Unsubscribe from PNC",
		"HTTPS removes you directly.",
		[]string{"HTTPS (recommended)", "Email (fallback)"},
	)
	start, width := dialog.ContextWidth()

	if got := width(200); got < selectorDialogMinWidth || got > selectorDialogMaxWidth {
		t.Fatalf("wide dialog width = %d, want %d..%d", got,
			selectorDialogMinWidth, selectorDialogMaxWidth)
	}
	if got, want := start(200), (200-width(200))/2; got != want {
		t.Fatalf("wide dialog start = %d, want %d", got, want)
	}
	if got, want := width(40), 40-2*selectorDialogMargin; got != want {
		t.Fatalf("narrow dialog width = %d, want %d", got, want)
	}
	if got, want := start(40)+width(40), 40-selectorDialogMargin; got != want {
		t.Fatalf("narrow dialog right edge = %d, want %d", got, want)
	}
}

func TestSelectorDialogWidthCapsLongContent(t *testing.T) {
	dialog := testSelectorDialog(
		"A very long selector title that should never turn into a full-width stripe across the terminal",
		"A very long prompt that should be truncated while the compact card remains bounded in a wide terminal window",
		[]string{"An unusually long option that must not make the dialog unbounded"},
	)
	_, width := dialog.ContextWidth()
	if got := width(300); got != selectorDialogMaxWidth {
		t.Fatalf("long-content width = %d, want %d", got, selectorDialogMaxWidth)
	}
}

func TestSelectorDialogHeightPreservesPromptLines(t *testing.T) {
	dialog := testSelectorDialog("Warning", "first\nsecond\nthird", []string{"OK"})
	start, height := dialog.ContextHeight()

	if got, want := height(50), 9; got != want {
		t.Fatalf("dialog height = %d, want %d", got, want)
	}
	if got, want := start(50), (50-height(50))/2; got != want {
		t.Fatalf("dialog start = %d, want %d", got, want)
	}
	if got := height(6); got != 6 {
		t.Fatalf("short-terminal height = %d, want 6", got)
	}
}
