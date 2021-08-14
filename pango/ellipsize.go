package pango

import (
	"math"

	"github.com/benoitkugler/textlayout/fribidi"
)

/* Overall, the way we ellipsize is we grow a "gap" out from an original
 * gap center position until:
 *
 *  line_width - gap_width + ellipsize_width <= goalWidth
 *
 * Line:  [-------------------------------------------]
 * Runs:  [------)[---------------)[------------------]
 * Gap center:                 *
 * Gap:             [----------------------]
 *
 * The gap center may be at the start or end in which case the gap grows
 * in only one direction.
 *
 * Note the line and last run are logically closed at the end; this allows
 * us to use a gap position at x=line_width and still have it be part of
 * of a run.
 *
 * We grow the gap out one "span" at a time, where a span is simply a
 * consecutive run of clusters that we can't interrupt with an ellipsis.
 *
 * When choosing whether to grow the gap at the start or the end, we
 * calculate the next span to remove in both directions and see which
 * causes the smaller increase in:
 *
 *  MAX (gap_end - gap_center, gap_start - gap_center)
 *
 * All computations are done using logical order; the ellipsization
 * process occurs before the runs are ordered into visual order.
 */

// EllipsizeMode describes what sort of (if any)
// ellipsization should be applied to a line of text. In
// the ellipsization process characters are removed from the
// text in order to make it fit to a given width and replaced
// with an ellipsis.
type EllipsizeMode uint8

const (
	ELLIPSIZE_NONE   EllipsizeMode = iota // No ellipsization
	ELLIPSIZE_START                       // Omit characters at the start of the text
	ELLIPSIZE_MIDDLE                      // Omit characters in the middle of the text
	ELLIPSIZE_END                         // Omit characters at the end of the text
)

// keeps information about a single run
type runInfo struct {
	run         *GlyphItem
	startOffset int       // Character offset of run start
	width       GlyphUnit // Width of run in Pango units
}

// iterator to a position within the ellipsized line
type lineIter struct {
	runIter   GlyphItemIter
	run_index int
}

// state of ellipsization process
type ellipsizeState struct {
	layout *Layout  // Layout being ellipsized
	attrs  AttrList // Attributes used for itemization/shaping

	runInfo []runInfo // Array of information about each run, of size `n_runs`
	// n_runs   int

	total_width GlyphUnit // Original width of line in Pango units
	gap_center  GlyphUnit // Goal for center of gap

	ellipsis_run   *GlyphItem // Run created to hold ellipsis
	ellipsis_width GlyphUnit  // Width of ellipsis, in Pango units

	// Whether the first character in the ellipsized
	// is wide; this triggers us to try to use a
	// mid-line ellipsis instead of a baseline
	ellipsis_is_cjk bool

	line_start_attr *attrIterator // Cached AttrIterator for the start of the run

	gap_start_iter lineIter      // Iteratator pointig to the first cluster in gap
	gap_start_x    GlyphUnit     // x position of start of gap, in Pango units
	gap_start_attr *attrIterator // Attribute iterator pointing to a range containing the first character in gap

	gap_end_iter lineIter  // Iterator pointing to last cluster in gap
	gap_end_x    GlyphUnit // x position of end of gap, in Pango units

	shape_flags shapeFlags
}

// Compute global information needed for the itemization process

func (line *LayoutLine) newState(attrs AttrList, shape_flags shapeFlags) ellipsizeState {
	var state ellipsizeState

	state.layout = line.layout

	state.attrs = attrs
	state.shape_flags = shape_flags

	state.runInfo = make([]runInfo, line.Runs.length())

	start_offset := line.StartIndex
	for l, i := line.Runs, 0; l != nil; l, i = l.Next, i+1 {
		run := l.Data
		width := run.Glyphs.getWidth()
		state.runInfo[i].run = run
		state.runInfo[i].width = width
		state.runInfo[i].startOffset = start_offset
		state.total_width += width
		start_offset += run.Item.Length
	}

	return state
}

//  // Cleanup memory allocation

// func free_state (state *EllipsizeState)
//  {
//    pango_attr_list_unref (state.attrs);
//    if (state.line_start_attr)
// 	 pango_attr_iterator_destroy (state.line_start_attr);
//    if (state.gap_start_attr)
// 	 pango_attr_iterator_destroy (state.gap_start_attr);
//    g_free (state.runInfo);
//  }

// computes the width of a single cluster
func (iter lineIter) getClusterWidth() GlyphUnit {
	runIter := iter.runIter
	glyphs := runIter.glyphItem.Glyphs

	var width GlyphUnit
	if runIter.startGlyph < runIter.endGlyph { // LTR
		for i := runIter.startGlyph; i < runIter.endGlyph; i++ {
			width += glyphs.Glyphs[i].Geometry.Width
		}
	} else { // RTL
		for i := runIter.startGlyph; i > runIter.endGlyph; i-- {
			width += glyphs.Glyphs[i].Geometry.Width
		}
	}

	return width
}

// move forward one cluster. Returns `false` if we were already at the end
func (state *ellipsizeState) lineIterNextCluster(iter *lineIter) bool {
	if !iter.runIter.NextCluster() {
		if iter.run_index == len(state.runInfo)-1 {
			return false
		} else {
			iter.run_index++
			iter.runIter.InitStart(state.runInfo[iter.run_index].run, state.layout.Text)
		}
	}
	return true
}

// move backward one cluster. Returns `false` if we were already at the end
func (state *ellipsizeState) lineIterPrevCluster(iter *lineIter) bool {
	if !iter.runIter.PrevCluster() {
		if iter.run_index == 0 {
			return false
		} else {
			iter.run_index--
			iter.runIter.InitEnd(state.runInfo[iter.run_index].run, state.layout.Text)
		}
	}
	return true
}

//  //
//   * An ellipsization boundary is defined by two things
//   *
//   * - Starts a cluster - forced by structure of code
//   * - Starts a grapheme - checked here
//   *
//   * In the future we'd also like to add a check for cursive connectivity here.
//   * This should be an addition to #PangoGlyphVisAttr
//   *

// checks if there is a ellipsization boundary before the cluster `iter` points to
func (state ellipsizeState) startsAtEllipsizationBoundary(iter lineIter) bool {
	runInfo := state.runInfo[iter.run_index]

	if iter.runIter.StartChar == 0 && iter.run_index == 0 {
		return true
	}

	return state.layout.logAttrs[runInfo.startOffset+iter.runIter.StartChar].IsCursorPosition()
}

// checks if there is a ellipsization boundary after the cluster `iter` points to
func (state ellipsizeState) endsAtEllipsizationBoundary(iter lineIter) bool {
	runInfo := state.runInfo[iter.run_index]

	if iter.runIter.EndChar == runInfo.run.Item.Length && iter.run_index == len(state.runInfo)-1 {
		return true
	}

	return state.layout.logAttrs[runInfo.startOffset+iter.runIter.EndChar+1].IsCursorPosition()
}

// helper function to re-itemize a string of text
func (state *ellipsizeState) itemizeText(text []rune, attrs AttrList) *Item {
	items := state.layout.context.Itemize(text, 0, len(text), attrs)

	if debugMode {
		assert(items != nil && items.Next == nil, "itemizeText")
	}
	return items.Data
}

// shapes the ellipsis using the font and is_cjk information computed by
// updateEllipsisShape() from the first character in the gap.
func (state *ellipsizeState) shapeEllipsis() {
	var attrs AttrList
	// Create/reset state.ellipsis_run
	if state.ellipsis_run == nil {
		state.ellipsis_run = new(GlyphItem)
		state.ellipsis_run.Glyphs = new(GlyphString)
	}

	if state.ellipsis_run.Item != nil {
		state.ellipsis_run.Item = nil
	}

	// Create an attribute list
	run_attrs := state.gap_start_attr.getAttributes()
	for _, attr := range run_attrs {
		attr.StartIndex = 0
		attr.EndIndex = MaxInt
		attrs.insert(attr)
	}

	fallback := NewAttrFallback(false)
	attrs.insert(fallback)

	// First try using a specific ellipsis character in the best matching font
	var ellipsis_text []rune
	if state.ellipsis_is_cjk {
		ellipsis_text = []rune{'\u22EF'} // U+22EF: MIDLINE HORIZONTAL ELLIPSIS, used for CJK
	} else {
		ellipsis_text = []rune{'\u2026'} // U+2026: HORIZONTAL ELLIPSIS
	}

	item := state.itemizeText(ellipsis_text, attrs)

	// If that fails we use "..." in the first matching font
	if item.Analysis.Font == nil || !pango_font_has_char(item.Analysis.Font, ellipsis_text[0]) {
		// Modify the fallback iter for it is inside the AttrList; Don't try this at home
		fallback.Data = AttrInt(1)
		ellipsis_text = []rune("...")
		item = state.itemizeText(ellipsis_text, attrs)
	}

	state.ellipsis_run.Item = item

	// Now shape
	glyphs := state.ellipsis_run.Glyphs
	glyphs.shapeWithFlags(ellipsis_text, 0, len(ellipsis_text), &item.Analysis, state.shape_flags)

	state.ellipsis_width = 0
	for _, g := range glyphs.Glyphs {
		state.ellipsis_width += g.Geometry.Width
	}
}

// helper function to advance a AttrIterator to a particular rune index.
func advanceIteratorTo(iter *attrIterator, newIndex int) {
	for do := true; do; do = iter.next() {
		if iter.EndIndex > newIndex {
			break
		}
	}
}

// updates the shaping of the ellipsis if necessary when we move the
// position of the start of the gap.
//
// The shaping of the ellipsis is determined by two things:
// - The font attributes applied to the first character in the gap
// - Whether the first character in the gap is wide or not. If the
//   first character is wide, then we assume that we are ellipsizing
//   East-Asian text, so prefer a mid-line ellipsizes to a baseline
//   ellipsis, since that's typical practice for Chinese/Japanese/Korean.
func (state *ellipsizeState) updateEllipsisShape() {
	recompute := false

	// Unfortunately, we can only advance AttrIterator forward; so each
	// time we back up we need to go forward to find the new position. To make
	// this not utterly slow, we cache an iterator at the start of the line
	if state.line_start_attr == nil {
		state.line_start_attr = state.attrs.getIterator()
		advanceIteratorTo(state.line_start_attr, state.runInfo[0].run.Item.Offset)
	}

	if state.gap_start_attr != nil {
		// See if the current attribute range contains the new start position
		start, _ := state.gap_start_attr.StartIndex, state.gap_start_attr.EndIndex
		if state.gap_start_iter.runIter.StartIndex < start {
			state.gap_start_attr = nil
		}
	}

	// Check whether we need to recompute the ellipsis because of new font attributes
	if state.gap_start_attr == nil {
		state.gap_start_attr = state.line_start_attr.copy()
		advanceIteratorTo(state.gap_start_attr, state.runInfo[state.gap_start_iter.run_index].run.Item.Offset)
		recompute = true
	}

	// Check whether we need to recompute the ellipsis because we switch from CJK to not or vice-versa
	start_wc := state.layout.Text[state.gap_start_iter.runIter.StartIndex]
	is_cjk := isWide(start_wc)

	if is_cjk != state.ellipsis_is_cjk {
		state.ellipsis_is_cjk = is_cjk
		recompute = true
	}

	if recompute {
		state.shapeEllipsis()
	}
}

// computes the position of the gap center and finds the smallest span containing it
func (state *ellipsizeState) findInitialSpan() {
	switch state.layout.ellipsize {
	case ELLIPSIZE_START:
		state.gap_center = 0
	case ELLIPSIZE_MIDDLE:
		state.gap_center = state.total_width / 2
	case ELLIPSIZE_END:
		state.gap_center = state.total_width
	}

	// Find the run containing the gap center

	var (
		x GlyphUnit
		i int
	)
	for ; i < len(state.runInfo); i++ {
		if x+state.runInfo[i].width > state.gap_center {
			break
		}

		x += state.runInfo[i].width
	}

	if i == len(state.runInfo) {
		// Last run is a closed interval, so back off one run
		i--
		x -= state.runInfo[i].width
	}

	// Find the cluster containing the gap center

	state.gap_start_iter.run_index = i
	runIter := &state.gap_start_iter.runIter
	glyph_item := state.runInfo[i].run

	var cluster_width GlyphUnit // Quiet GCC, the line must have at least one cluster
	have_cluster := runIter.InitStart(glyph_item, state.layout.Text)
	for ; have_cluster; have_cluster = runIter.NextCluster() {
		cluster_width = state.gap_start_iter.getClusterWidth()

		if x+cluster_width > state.gap_center {
			break
		}

		x += cluster_width
	}

	if !have_cluster {
		// Last cluster is a closed interval, so back off one cluster
		x -= cluster_width
	}

	state.gap_end_iter = state.gap_start_iter

	state.gap_start_x = x
	state.gap_end_x = x + cluster_width

	// Expand the gap to a full span

	for !state.startsAtEllipsizationBoundary(state.gap_start_iter) {
		state.lineIterPrevCluster(&state.gap_start_iter)
		state.gap_start_x -= state.gap_start_iter.getClusterWidth()
	}

	for !state.endsAtEllipsizationBoundary(state.gap_end_iter) {
		state.lineIterNextCluster(&state.gap_end_iter)
		state.gap_end_x += state.gap_end_iter.getClusterWidth()
	}

	state.updateEllipsisShape()
}

// Removes one run from the start or end of the gap. Returns false
// if there's nothing left to remove in either direction.
func (state *ellipsizeState) removeOneSpan() bool {
	// Find one span backwards and forward from the gap
	new_gap_start_iter := state.gap_start_iter
	new_gap_start_x := state.gap_start_x
	var width GlyphUnit
	for do := true; do; do = !state.startsAtEllipsizationBoundary(new_gap_start_iter) || width == 0 {
		if !state.lineIterPrevCluster(&new_gap_start_iter) {
			break
		}
		width = new_gap_start_iter.getClusterWidth()
		new_gap_start_x -= width
	}

	new_gap_end_iter := state.gap_end_iter
	new_gap_end_x := state.gap_end_x
	for do := true; do; do = !state.endsAtEllipsizationBoundary(new_gap_end_iter) || width == 0 {
		if !state.lineIterNextCluster(&new_gap_end_iter) {
			break
		}
		width = new_gap_end_iter.getClusterWidth()
		new_gap_end_x += width
	}

	if state.gap_end_x == new_gap_end_x && state.gap_start_x == new_gap_start_x {
		return false
	}

	// In the case where we could remove a span from either end of the
	// gap, we look at which causes the smaller increase in the
	// MAX (gap_end - gap_center, gap_start - gap_center)
	if state.gap_end_x == new_gap_end_x ||
		(state.gap_start_x != new_gap_start_x &&
			state.gap_center-new_gap_start_x < new_gap_end_x-state.gap_center) {
		state.gap_start_iter = new_gap_start_iter
		state.gap_start_x = new_gap_start_x

		state.updateEllipsisShape()
	} else {
		state.gap_end_iter = new_gap_end_iter
		state.gap_end_x = new_gap_end_x
	}

	return true
}

// Fixes up the properties of the ellipsis run once we've determined the final extents of the gap
func (state *ellipsizeState) fixupEllipsisRun(extraWidth GlyphUnit) {
	glyphs := state.ellipsis_run.Glyphs
	item := state.ellipsis_run.Item

	// Make the entire glyphstring into a single logical cluster
	for i := range glyphs.Glyphs {
		glyphs.logClusters[i] = 0
		glyphs.Glyphs[i].attr.isClusterStart = false
	}

	glyphs.Glyphs[0].attr.isClusterStart = true
	glyphs.Glyphs[len(glyphs.Glyphs)-1].Geometry.Width += extraWidth

	// Fix up the item to point to the entire elided text
	item.Offset = state.gap_start_iter.runIter.StartIndex
	item.Length = state.gap_end_iter.runIter.EndIndex - item.Offset

	// The level for the item is the minimum level of the elided text
	var level fribidi.Level = math.MaxInt8
	for _, rf := range state.runInfo[state.gap_start_iter.run_index : state.gap_end_iter.run_index+1] {
		level = minL(level, rf.run.Item.Analysis.Level)
	}

	item.Analysis.Level = level

	item.Analysis.Flags |= AFIsEllipsis
}

// Computes the new list of runs for the line
func (state *ellipsizeState) getRunList() *RunList {
	var partialStartRun, partialEndRun *GlyphItem
	// We first cut out the pieces of the starting and ending runs we want to
	// preserve; we do the end first in case the end and the start are
	// the same. Doing the start first would disturb the indices for the end.
	runInfo := &state.runInfo[state.gap_end_iter.run_index]
	runIter := &state.gap_end_iter.runIter
	if runIter.EndChar != runInfo.run.Item.Length {
		partialEndRun = runInfo.run
		runInfo.run = runInfo.run.pango_glyph_item_split(state.layout.Text, runIter.EndIndex-runInfo.run.Item.Offset)
	}

	runInfo = &state.runInfo[state.gap_start_iter.run_index]
	runIter = &state.gap_start_iter.runIter
	if runIter.StartChar != 0 {
		partialStartRun = runInfo.run.pango_glyph_item_split(state.layout.Text, runIter.StartIndex-runInfo.run.Item.Offset)
	}

	// Now assemble the new list of runs
	var result *RunList
	for _, rf := range state.runInfo[0:state.gap_start_iter.run_index] {
		result = &RunList{Data: rf.run, Next: result}
	}

	if partialStartRun != nil {
		result = &RunList{Data: partialStartRun, Next: result}
	}

	result = &RunList{Data: state.ellipsis_run, Next: result}

	if partialEndRun != nil {
		result = &RunList{Data: partialEndRun, Next: result}
	}

	for _, rf := range state.runInfo[state.gap_end_iter.run_index+1:] {
		result = &RunList{Data: rf.run, Next: result}
	}

	return result.reverse()
}

// computes the width of the line as currently ellipsized
func (state *ellipsizeState) currentWidth() GlyphUnit {
	return state.total_width - (state.gap_end_x - state.gap_start_x) + state.ellipsis_width
}

// ellipsize ellipsizes a `LayoutLine`, with the runs still in logical order,
// and according to the layout's policy to fit within the set width of the layout.
// It returns whether the line had to be ellipsized
func (line *LayoutLine) ellipsize(attrs AttrList, shapeFlag shapeFlags, goalWidth GlyphUnit) bool {
	if line.layout.ellipsize == ELLIPSIZE_NONE || goalWidth < 0 {
		return false
	}

	state := line.newState(attrs, shapeFlag)
	if state.total_width <= goalWidth {
		return false
	}

	state.findInitialSpan()

	for state.currentWidth() > goalWidth {
		if !state.removeOneSpan() {
			break
		}
	}

	state.fixupEllipsisRun(maxG(goalWidth-state.currentWidth(), 0))

	line.Runs = state.getRunList()
	return true
}
