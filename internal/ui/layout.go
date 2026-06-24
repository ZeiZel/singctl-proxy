package ui

// layout is the responsive mode chosen from the terminal size on every
// WindowSizeMsg. The view branches on it for column count, borders, selector
// orientation and help density.
type layout int

const (
	layoutTooSmall layout = iota // below the usable floor — show a hint only
	layoutNarrow                 // single column, borderless panels, vertical selector
	layoutMedium                 // single column, bordered panels, horizontal selector
	layoutWide                   // two panels side-by-side, roomier
)

// Breakpoints. Width drives the column/border decisions; height gates the
// "too small" floor (width-aware: the narrow vertical selector needs more rows
// than the medium/wide horizontal one) and the blank-spacer padding.
const (
	minCols     = 34  // below this the UI can't render legibly
	minRowsWide = 12  // header + compact panels + horizontal selector + footer
	minRowsNarr = 15  // narrow stacks a 5-row vertical selector → needs more
	narrowMax = 56  // [minCols, narrowMax) → narrow
	wideMin   = 100 // [wideMin, ∞) → wide; between → medium

	dashConnRows = 6 // max live-connection rows shown in the dashboard preview
)

func layoutFor(w, h int) layout {
	if w < minCols {
		return layoutTooSmall
	}
	minRows := minRowsWide
	if w < narrowMax {
		minRows = minRowsNarr
	}
	if h < minRows {
		return layoutTooSmall
	}
	switch {
	case w < narrowMax:
		return layoutNarrow
	case w < wideMin:
		return layoutMedium
	default:
		return layoutWide
	}
}
