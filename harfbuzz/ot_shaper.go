package harfbuzz

import (
	"fmt"

	tt "github.com/benoitkugler/textlayout/fonts/truetype"
)

// Support functions for OpenType shaping related queries.
// ported from src/hb-ot-shape.cc Copyright © 2009,2010  Red Hat, Inc. 2010,2011,2012  Google, Inc. Behdad Esfahbod

/*
 * GSUB/GPOS feature query and enumeration interface
 */

const (
	// Special value for script index indicating unsupported script.
	noScriptIndex = 0xFFFF
	// Special value for feature index indicating unsupported feature.
	noFeatureIndex = 0xFFFF
	// Special value for language index indicating default or unsupported language.
	defaultLanguageIndex = 0xFFFF
	// Special value for variations index indicating unsupported variation.
	noVariationsIndex = 0xFFFFFFFF
)

type otShapePlanner struct {
	shaper                        otComplexShaper
	props                         SegmentProperties
	tables                        tt.LayoutTables
	aatMap                        aatMapBuilder
	map_                          otMapBuilder
	applyMorx                     bool
	scriptZeroMarks               bool
	scriptFallbackMarkPositioning bool
}

func newOtShapePlanner(tables *tt.LayoutTables, props SegmentProperties) *otShapePlanner {
	var out otShapePlanner
	out.map_ = newOtMapBuilder(tables, props)
	out.aatMap = aatMapBuilder{tables: tables}

	/* https://github.com/harfbuzz/harfbuzz/issues/2124 */
	out.applyMorx = len(tables.Morx) != 0 && (props.Direction.isHorizontal() || len(tables.GSUB.Lookups) == 0)

	out.shaper = out.shapeComplexCategorize()

	zwm, fb := out.shaper.marksBehavior()
	out.scriptZeroMarks = zwm != zeroWidthMarksNone
	out.scriptFallbackMarkPositioning = fb

	/* https://github.com/harfbuzz/harfbuzz/issues/1528 */
	if _, isDefault := out.shaper.(complexShaperDefault); out.applyMorx && !isDefault {
		out.shaper = complexShaperDefault{dumb: true}
	}
	return &out
}

func (planner *otShapePlanner) compile(plan *otShapePlan, key otShapePlanKey) {
	plan.props = planner.props
	plan.shaper = planner.shaper
	planner.map_.compile(&plan.map_, key)
	if planner.applyMorx {
		planner.aatMap.compile(&plan.aatMap)
	}

	plan.fracMask = plan.map_.getMask1(newTag('f', 'r', 'a', 'c'))
	plan.numrMask = plan.map_.getMask1(newTag('n', 'u', 'm', 'r'))
	plan.dnomMask = plan.map_.getMask1(newTag('d', 'n', 'o', 'm'))
	plan.hasFrac = plan.fracMask != 0 || (plan.numrMask != 0 && plan.dnomMask != 0)

	plan.rtlmMask = plan.map_.getMask1(newTag('r', 't', 'l', 'm'))
	plan.hasVert = plan.map_.getMask1(newTag('v', 'e', 'r', 't')) != 0

	kernTag := newTag('v', 'k', 'r', 'n')
	if planner.props.Direction.isHorizontal() {
		kernTag = newTag('k', 'e', 'r', 'n')
	}

	plan.kernMask, _ = plan.map_.getMask(kernTag)
	plan.requestedKerning = plan.kernMask != 0
	plan.trakMask, _ = plan.map_.getMask(newTag('t', 'r', 'a', 'k'))
	plan.requestedTracking = plan.trakMask != 0

	hasGposKern := plan.map_.getFeatureIndex(1, kernTag) != noFeatureIndex
	disableGpos := plan.shaper.gposTag() != 0 && plan.shaper.gposTag() != plan.map_.chosenScript[1]

	// Decide who provides glyph classes. GDEF or Unicode.
	if planner.tables.GDEF.Class == nil {
		plan.fallbackGlyphClasses = true
	}

	// Decide who does substitutions. GSUB, morx, or fallback.
	plan.applyMorx = planner.applyMorx

	//  Decide who does positioning. GPOS, kerx, kern, or fallback.
	hasKerx := planner.tables.Kerx != nil
	if hasKerx {
		plan.applyKerx = true
	} else if !planner.applyMorx && !disableGpos && len(planner.tables.GPOS.Lookups) != 0 {
		plan.applyGpos = true
	}

	if !plan.applyKerx && (!hasGposKern || !plan.applyGpos) {
		// apparently Apple applies kerx if GPOS kern was not applied.
		if hasKerx {
			plan.applyKerx = true
		} else if planner.tables.Kern != nil {
			plan.applyKern = true
		}
	}

	plan.zeroMarks = planner.scriptZeroMarks && !plan.applyKerx &&
		(!plan.applyKern || !hasMachineKerning(planner.tables.Kern))
	plan.hasGposMark = plan.map_.getMask1(newTag('m', 'a', 'r', 'k')) != 0

	plan.adjustMarkPositioningWhenZeroing = !plan.applyGpos && !plan.applyKerx &&
		(!plan.applyKern || !hasCrossKerning(planner.tables.Kern))

	plan.fallbackMarkPositioning = plan.adjustMarkPositioningWhenZeroing && planner.scriptFallbackMarkPositioning

	// currently we always apply trak.
	plan.applyTrak = plan.requestedTracking && !planner.tables.Trak.IsEmpty()
}

type otShapePlan struct {
	shaper otComplexShaper
	props  SegmentProperties

	aatMap aatMap
	map_   otMap

	fracMask Mask
	numrMask Mask
	dnomMask Mask
	rtlmMask Mask
	kernMask Mask
	trakMask Mask

	hasFrac                          bool
	requestedTracking                bool
	requestedKerning                 bool
	hasVert                          bool
	hasGposMark                      bool
	zeroMarks                        bool
	fallbackGlyphClasses             bool
	fallbackMarkPositioning          bool
	adjustMarkPositioningWhenZeroing bool

	applyGpos bool
	applyKern bool
	applyKerx bool
	applyMorx bool
	applyTrak bool
}

func newOtShapePlan(tables *tt.LayoutTables, props SegmentProperties, userFeatures []Feature, otKey otShapePlanKey) otShapePlan {
	var sp otShapePlan
	planner := newOtShapePlanner(tables, props)

	planner.collectFeatures(userFeatures)

	planner.compile(&sp, otKey)

	sp.shaper.dataCreate(&sp)

	return sp
}

func (sp *otShapePlan) substitute(font *Font, buffer *Buffer) {
	if sp.applyMorx {
		sp.aatLayoutSubstitute(font, buffer)
	} else {
		sp.map_.substitute(sp, font, buffer)
	}
}

func (sp *otShapePlan) position(font *Font, buffer *Buffer) {
	if sp.applyGpos {
		sp.map_.position(sp, font, buffer)
	} else if sp.applyKerx {
		sp.aatLayoutPosition(font, buffer)
	} else if sp.applyKern {
		sp.otLayoutKern(font, buffer)
	}

	if sp.applyTrak {
		sp.aatLayoutTrack(font, buffer)
	}
}

var (
	commonFeatures = [...]otMapFeature{
		{newTag('a', 'b', 'v', 'm'), ffGLOBAL},
		{newTag('b', 'l', 'w', 'm'), ffGLOBAL},
		{newTag('c', 'c', 'm', 'p'), ffGLOBAL},
		{newTag('l', 'o', 'c', 'l'), ffGLOBAL},
		{newTag('m', 'a', 'r', 'k'), ffGlobalManualJoiners},
		{newTag('m', 'k', 'm', 'k'), ffGlobalManualJoiners},
		{newTag('r', 'l', 'i', 'g'), ffGLOBAL},
	}

	horizontalFeatures = [...]otMapFeature{
		{newTag('c', 'a', 'l', 't'), ffGLOBAL},
		{newTag('c', 'l', 'i', 'g'), ffGLOBAL},
		{newTag('c', 'u', 'r', 's'), ffGLOBAL},
		{newTag('d', 'i', 's', 't'), ffGLOBAL},
		{newTag('k', 'e', 'r', 'n'), ffGlobalHasFallback},
		{newTag('l', 'i', 'g', 'a'), ffGLOBAL},
		{newTag('r', 'c', 'l', 't'), ffGLOBAL},
	}
)

func (planner *otShapePlanner) collectFeatures(userFeatures []Feature) {
	map_ := &planner.map_

	map_.enableFeature(newTag('r', 'v', 'r', 'n'))
	map_.addGSUBPause(nil)

	switch planner.props.Direction {
	case LeftToRight:
		map_.enableFeature(newTag('l', 't', 'r', 'a'))
		map_.enableFeature(newTag('l', 't', 'r', 'm'))
	case RightToLeft:
		map_.enableFeature(newTag('r', 't', 'l', 'a'))
		map_.addFeature(newTag('r', 't', 'l', 'm'))
	}

	/* Automatic fractions. */
	map_.addFeature(newTag('f', 'r', 'a', 'c'))
	map_.addFeature(newTag('n', 'u', 'm', 'r'))
	map_.addFeature(newTag('d', 'n', 'o', 'm'))

	/* Random! */
	map_.enableFeatureExt(newTag('r', 'a', 'n', 'd'), ffRandom, otMapMaxValue)

	/* Tracking.  We enable dummy feature here just to allow disabling
	* AAT 'trak' table using features.
	* https://github.com/harfbuzz/harfbuzz/issues/1303 */
	map_.enableFeatureExt(newTag('t', 'r', 'a', 'k'), ffHasFallback, 1)

	map_.enableFeature(newTag('H', 'A', 'R', 'F'))

	planner.shaper.collectFeatures(planner)

	map_.enableFeature(newTag('B', 'U', 'Z', 'Z'))

	for _, feat := range commonFeatures {
		map_.addFeatureExt(feat.tag, feat.flags, 1)
	}

	if planner.props.Direction.isHorizontal() {
		for _, feat := range horizontalFeatures {
			map_.addFeatureExt(feat.tag, feat.flags, 1)
		}
	} else {
		/* We really want to find a 'vert' feature if there's any in the font, no
		 * matter which script/langsys it is listed (or not) under.
		 * See various bugs referenced from:
		 * https://github.com/harfbuzz/harfbuzz/issues/63 */
		map_.enableFeatureExt(newTag('v', 'e', 'r', 't'), ffGlobalSearch, 1)
	}

	for _, f := range userFeatures {
		ftag := ffNone
		if f.Start == FeatureGlobalStart && f.End == FeatureGlobalEnd {
			ftag = ffGLOBAL
		}
		map_.addFeatureExt(f.Tag, ftag, f.Value)
	}

	if planner.applyMorx {
		aatMap := &planner.aatMap
		for _, f := range userFeatures {
			aatMap.addFeature(f.Tag, f.Value)
		}
	}

	planner.shaper.overrideFeatures(planner)
}

/*
 * shaper
 */

type otContext struct {
	plan         *otShapePlan
	font         *Font
	face         Face
	buffer       *Buffer
	userFeatures []Feature

	// transient stuff
	targetDirection Direction
}

/* Main shaper */

/*
 * Substitute
 */

func vertCharFor(u rune) rune {
	switch u >> 8 {
	case 0x20:
		switch u {
		case 0x2013:
			return 0xfe32 // EN DASH
		case 0x2014:
			return 0xfe31 // EM DASH
		case 0x2025:
			return 0xfe30 // TWO DOT LEADER
		case 0x2026:
			return 0xfe19 // HORIZONTAL ELLIPSIS
		}
	case 0x30:
		switch u {
		case 0x3001:
			return 0xfe11 // IDEOGRAPHIC COMMA
		case 0x3002:
			return 0xfe12 // IDEOGRAPHIC FULL STOP
		case 0x3008:
			return 0xfe3f // LEFT ANGLE BRACKET
		case 0x3009:
			return 0xfe40 // RIGHT ANGLE BRACKET
		case 0x300a:
			return 0xfe3d // LEFT DOUBLE ANGLE BRACKET
		case 0x300b:
			return 0xfe3e // RIGHT DOUBLE ANGLE BRACKET
		case 0x300c:
			return 0xfe41 // LEFT CORNER BRACKET
		case 0x300d:
			return 0xfe42 // RIGHT CORNER BRACKET
		case 0x300e:
			return 0xfe43 // LEFT WHITE CORNER BRACKET
		case 0x300f:
			return 0xfe44 // RIGHT WHITE CORNER BRACKET
		case 0x3010:
			return 0xfe3b // LEFT BLACK LENTICULAR BRACKET
		case 0x3011:
			return 0xfe3c // RIGHT BLACK LENTICULAR BRACKET
		case 0x3014:
			return 0xfe39 // LEFT TORTOISE SHELL BRACKET
		case 0x3015:
			return 0xfe3a // RIGHT TORTOISE SHELL BRACKET
		case 0x3016:
			return 0xfe17 // LEFT WHITE LENTICULAR BRACKET
		case 0x3017:
			return 0xfe18 // RIGHT WHITE LENTICULAR BRACKET
		}
	case 0xfe:
		switch u {
		case 0xfe4f:
			return 0xfe34 // WAVY LOW LINE
		}
	case 0xff:
		switch u {
		case 0xff01:
			return 0xfe15 // FULLWIDTH EXCLAMATION MARK
		case 0xff08:
			return 0xfe35 // FULLWIDTH LEFT PARENTHESIS
		case 0xff09:
			return 0xfe36 // FULLWIDTH RIGHT PARENTHESIS
		case 0xff0c:
			return 0xfe10 // FULLWIDTH COMMA
		case 0xff1a:
			return 0xfe13 // FULLWIDTH COLON
		case 0xff1b:
			return 0xfe14 // FULLWIDTH SEMICOLON
		case 0xff1f:
			return 0xfe16 // FULLWIDTH QUESTION MARK
		case 0xff3b:
			return 0xfe47 // FULLWIDTH LEFT SQUARE BRACKET
		case 0xff3d:
			return 0xfe48 // FULLWIDTH RIGHT SQUARE BRACKET
		case 0xff3f:
			return 0xfe33 // FULLWIDTH LOW LINE
		case 0xff5b:
			return 0xfe37 // FULLWIDTH LEFT CURLY BRACKET
		case 0xff5d:
			return 0xfe38 // FULLWIDTH RIGHT CURLY BRACKET
		}
	}

	return u
}

func (c *otContext) otRotateChars() {
	info := c.buffer.Info

	if c.targetDirection.isBackward() {
		rtlmMask := c.plan.rtlmMask

		for i := range info {
			codepoint := uni.mirroring(info[i].codepoint)
			if codepoint != info[i].codepoint && c.font.hasGlyph(codepoint) {
				info[i].codepoint = codepoint
			} else {
				info[i].mask |= rtlmMask
			}
		}
	}

	if c.targetDirection.isVertical() && !c.plan.hasVert {
		for i := range info {
			codepoint := vertCharFor(info[i].codepoint)
			if codepoint != info[i].codepoint && c.font.hasGlyph(codepoint) {
				info[i].codepoint = codepoint
			}
		}
	}
}

func (c *otContext) setupMasksFraction() {
	if c.buffer.scratchFlags&bsfHasNonASCII == 0 || !c.plan.hasFrac {
		return
	}

	buffer := c.buffer

	var preMask, postMask Mask
	if buffer.Props.Direction.isForward() {
		preMask = c.plan.numrMask | c.plan.fracMask
		postMask = c.plan.fracMask | c.plan.dnomMask
	} else {
		preMask = c.plan.fracMask | c.plan.dnomMask
		postMask = c.plan.numrMask | c.plan.fracMask
	}

	count := len(buffer.Info)
	info := buffer.Info
	for i := 0; i < count; i++ {
		if info[i].codepoint == 0x2044 /* FRACTION SLASH */ {
			start, end := i, i+1
			for start != 0 && info[start-1].unicode.generalCategory() == DecimalNumber {
				start--
			}
			for end < count && info[end].unicode.generalCategory() == DecimalNumber {
				end++
			}

			buffer.unsafeToBreak(start, end)

			for j := start; j < i; j++ {
				info[j].mask |= preMask
			}
			info[i].mask |= c.plan.fracMask
			for j := i + 1; j < end; j++ {
				info[j].mask |= postMask
			}

			i = end - 1
		}
	}
}

func (c *otContext) initializeMasks() {
	c.buffer.resetMasks(c.plan.map_.globalMask)
}

func (c *otContext) setupMasks() {
	map_ := &c.plan.map_
	buffer := c.buffer

	c.setupMasksFraction()

	c.plan.shaper.setupMasks(c.plan, buffer, c.font)

	for _, feature := range c.userFeatures {
		if !(feature.Start == FeatureGlobalStart && feature.End == FeatureGlobalEnd) {
			mask, shift := map_.getMask(feature.Tag)
			buffer.setMasks(feature.Value<<shift, mask, feature.Start, feature.End)
		}
	}
}

func zeroWidthDefaultIgnorables(buffer *Buffer) {
	if buffer.scratchFlags&bsfHasDefaultIgnorables == 0 ||
		buffer.Flags&PreserveDefaultIgnorables != 0 ||
		buffer.Flags&RemoveDefaultIgnorables != 0 {
		return
	}

	pos := buffer.Pos
	for i, info := range buffer.Info {
		if info.isDefaultIgnorable() {
			pos[i].XAdvance, pos[i].YAdvance, pos[i].XOffset, pos[i].YOffset = 0, 0, 0, 0
		}
	}
}

func hideDefaultIgnorables(buffer *Buffer, font *Font) {
	if buffer.scratchFlags&bsfHasDefaultIgnorables == 0 ||
		buffer.Flags&PreserveDefaultIgnorables != 0 {
		return
	}

	info := buffer.Info

	var (
		invisible = buffer.Invisible
		ok        bool
	)
	if invisible == 0 {
		invisible, ok = font.face.GetNominalGlyph(' ')
	}
	if buffer.Flags&RemoveDefaultIgnorables == 0 && ok {
		// replace default-ignorables with a zero-advance invisible glyph.
		for i := range info {
			if info[i].isDefaultIgnorable() {
				info[i].Glyph = invisible
			}
		}
	} else {
		otLayoutDeleteGlyphsInplace(buffer, (*GlyphInfo).isDefaultIgnorable)
	}
}

// use unicodeProp to assign a class
func synthesizeGlyphClasses(buffer *Buffer) {
	info := buffer.Info
	for i := range info {
		/* Never mark default-ignorables as marks.
		 * They won't get in the way of lookups anyway,
		 * but having them as mark will cause them to be skipped
		 * over if the lookup-flag says so, but at least for the
		 * Mongolian variation selectors, looks like Uniscribe
		 * marks them as non-mark.  Some Mongolian fonts without
		 * GDEF rely on this.  Another notable character that
		 * this applies to is COMBINING GRAPHEME JOINER. */
		class := tt.Mark
		if info[i].unicode.generalCategory() != NonSpacingMark || info[i].isDefaultIgnorable() {
			class = tt.BaseGlyph
		}

		info[i].glyphProps = class
	}
}

func (c *otContext) substituteBeforePosition() {
	buffer := c.buffer
	// normalize and sets Glyph

	c.otRotateChars()

	otShapeNormalize(c.plan, buffer, c.font)

	c.setupMasks()

	// this is unfortunate to go here, but necessary...
	if c.plan.fallbackMarkPositioning {
		fallbackMarkPositionRecategorizeMarks(buffer)
	}

	// Glyph fields are now set up ...
	// ... apply complex substitution from font

	layoutSubstituteStart(c.font, buffer)

	if c.plan.fallbackGlyphClasses {
		synthesizeGlyphClasses(c.buffer)
	}

	c.plan.substitute(c.font, buffer)
}

func (c *otContext) substituteAfterPosition() {
	hideDefaultIgnorables(c.buffer, c.font)
	if c.plan.applyMorx {
		aatLayoutRemoveDeletedGlyphsInplace(c.buffer)
	}

	if debugMode {
		fmt.Println("start postprocess-glyphs")
	}
	c.plan.shaper.postprocessGlyphs(c.plan, c.buffer, c.font)
	if debugMode {
		fmt.Println("end postprocess-glyphs")
	}
}

/*
 * Position
 */

func zeroMarkWidthsByGdef(buffer *Buffer, adjustOffsets bool) {
	for i, inf := range buffer.Info {
		if inf.isMark() {
			pos := &buffer.Pos[i]
			if adjustOffsets { // adjustMarkOffsets
				pos.XOffset -= pos.XAdvance
				pos.YOffset -= pos.YAdvance
			}
			// zeroMarkWidth
			pos.XAdvance = 0
			pos.YAdvance = 0
		}
	}
}

// override Pos array with default values
func (c *otContext) positionDefault() {
	direction := c.buffer.Props.Direction
	info := c.buffer.Info
	pos := c.buffer.Pos
	if direction.isHorizontal() {
		for i, inf := range info {
			pos[i].XAdvance, pos[i].YAdvance = c.font.getGlyphHAdvance(inf.Glyph), 0
			pos[i].XOffset, pos[i].YOffset = c.font.subtractGlyphHOrigin(inf.Glyph, 0, 0)
		}
	} else {
		for i, inf := range info {
			pos[i].XAdvance, pos[i].YAdvance = 0, c.font.getGlyphVAdvance(inf.Glyph)
			pos[i].XOffset, pos[i].YOffset = c.font.subtractGlyphVOrigin(inf.Glyph, 0, 0)
		}
	}
	if c.buffer.scratchFlags&bsfHasSpaceFallback != 0 {
		fallbackSpaces(c.font, c.buffer)
	}
}

func (c *otContext) positionComplex() {
	info := c.buffer.Info
	pos := c.buffer.Pos

	/* If the font has no GPOS and direction is forward, then when
	* zeroing mark widths, we shift the mark with it, such that the
	* mark is positioned hanging over the previous glyph.  When
	* direction is backward we don't shift and it will end up
	* hanging over the next glyph after the final reordering.
	*
	* Note: If fallback positioning happens, we don't care about
	* this as it will be overriden. */
	adjustOffsetsWhenZeroing := c.plan.adjustMarkPositioningWhenZeroing && c.buffer.Props.Direction.isForward()

	// we change glyph origin to what GPOS expects (horizontal), apply GPOS, change it back.

	for i, inf := range info {
		pos[i].XOffset, pos[i].YOffset = c.font.addGlyphHOrigin(inf.Glyph, pos[i].XOffset, pos[i].YOffset)
	}

	otLayoutPositionStart(c.font, c.buffer)

	if c.plan.zeroMarks {
		if zwm, _ := c.plan.shaper.marksBehavior(); zwm == zeroWidthMarksByGdefEarly {
			zeroMarkWidthsByGdef(c.buffer, adjustOffsetsWhenZeroing)
		}
	}

	c.plan.position(c.font, c.buffer)

	if c.plan.zeroMarks {
		if zwm, _ := c.plan.shaper.marksBehavior(); zwm == zeroWidthMarksByGdefLate {
			zeroMarkWidthsByGdef(c.buffer, adjustOffsetsWhenZeroing)
		}
	}

	// finish off. Has to follow a certain order.
	zeroWidthDefaultIgnorables(c.buffer)
	if c.plan.applyMorx {
		aatLayoutZeroWidthDeletedGlyphs(c.buffer)
	}
	otLayoutPositionFinishOffsets(c.font, c.buffer)

	for i, inf := range info {
		pos[i].XOffset, pos[i].YOffset = c.font.subtractGlyphHOrigin(inf.Glyph, pos[i].XOffset, pos[i].YOffset)
	}

	if c.plan.fallbackMarkPositioning {
		fallbackMarkPosition(c.plan, c.font, c.buffer, adjustOffsetsWhenZeroing)
	}
}

func (c *otContext) position() {
	c.buffer.clearPositions()

	c.positionDefault()

	c.positionComplex()

	if c.buffer.Props.Direction.isBackward() {
		c.buffer.Reverse()
	}
}

/* Propagate cluster-level glyph flags to be the same on all cluster glyphs.
 * Simplifies using them. */
func propagateFlags(buffer *Buffer) {
	if buffer.scratchFlags&bsfHasUnsafeToBreak == 0 {
		return
	}

	info := buffer.Info

	iter, count := buffer.ClusterIterator()
	for start, end := iter.Next(); start < count; start, end = iter.Next() {
		var mask uint32
		for i := start; i < end; i++ {
			if info[i].mask&GlyphFlagUnsafeToBreak != 0 {
				mask = GlyphFlagUnsafeToBreak
				break
			}
		}
		if mask != 0 {
			for i := start; i < end; i++ {
				info[i].mask |= mask
			}
		}
	}
}

// shaperOpentype is the main shaper of this library.
// It handles complex language and Opentype layout features found in fonts.
type shaperOpentype struct {
	plan otShapePlan
	key  otShapePlanKey
}

var _ shaper = (*shaperOpentype)(nil)

type otShapePlanKey = [2]int // -1 for not found

func newShaperOpentype(tables *tt.LayoutTables, props SegmentProperties, userFeatures []Feature, coords []float32) *shaperOpentype {
	var out shaperOpentype
	out.key = otShapePlanKey{
		0: tables.GSUB.FindVariationIndex(coords),
		1: tables.GPOS.FindVariationIndex(coords),
	}
	out.plan = newOtShapePlan(tables, props, userFeatures, out.key)
	return &out
}

// pull it all together!
func (sp *shaperOpentype) shape(font *Font, buffer *Buffer, features []Feature) {
	c := otContext{plan: &sp.plan, font: font, face: font.face, buffer: buffer, userFeatures: features}
	c.buffer.scratchFlags = bsfDefault

	const maxOpsFactor = 1024
	c.buffer.maxOps = len(c.buffer.Info) * maxOpsFactor

	// save the original direction, we use it later.
	c.targetDirection = c.buffer.Props.Direction

	c.buffer.clearOutput()

	c.initializeMasks()
	c.buffer.setUnicodeProps()
	c.buffer.insertDottedCircle(c.font)

	c.buffer.formClusters()

	c.buffer.ensureNativeDirection()

	if debugMode {
		fmt.Println("start preprocess-text")
	}
	c.plan.shaper.preprocessText(c.plan, c.buffer, c.font)
	if debugMode {
		fmt.Println("end preprocess-text")
	}

	c.substituteBeforePosition()
	c.position()
	c.substituteAfterPosition()

	propagateFlags(c.buffer)

	c.buffer.Props.Direction = c.targetDirection

	c.buffer.maxOps = maxOpsDefault
}

//  /**
//   * hb_ot_shape_plan_collect_lookups:
//   * @shape_plan: #ShapePlan to query
//   * @table_tag: GSUB or GPOS
//   * @lookup_indexes: (out): The #hb_set_t set of lookups returned
//   *
//   * Computes the complete set of GSUB or GPOS lookups that are applicable
//   * under a given @shape_plan.
//   *
//   * Since: 0.9.7
//   **/
//  void
//  hb_ot_shape_plan_collect_lookups (ShapePlan *shape_plan,
// 				   hb_tag_t         table_tag,
// 				   hb_set_t        *lookup_indexes /* OUT */)
//  {
//    shape_plan.ot.collect_lookups (table_tag, lookup_indexes);
//  }

//  /* TODO Move this to hb-ot-shape-normalize, make it do decompose, and make it public. */
//  static void
//  add_char (Font          *font,
// 	   hb_unicode_funcs_t *unicode,
// 	   hb_bool_t           mirror,
// 	   rune      u,
// 	   hb_set_t           *glyphs)
//  {
//    rune glyph;
//    if (font.get_nominal_glyph (u, &glyph))
// 	 glyphs.add (glyph);
//    if (mirror)
//    {
// 	 rune m = unicode.Mirroring (u);
// 	 if (m != u && font.get_nominal_glyph (m, &glyph))
// 	   glyphs.add (glyph);
//    }
//  }

//  /**
//   * hb_ot_shape_glyphs_closure:
//   * @font: #Font to work upon
//   * @buffer: The input buffer to compute from
//   * @features: (array length=num_features): The features enabled on the buffer
//   * @num_features: The number of features enabled on the buffer
//   * @glyphs: (out): The #hb_set_t set of glyphs comprising the transitive closure of the query
//   *
//   * Computes the transitive closure of glyphs needed for a specified
//   * input buffer under the given font and feature list. The closure is
//   * computed as a set, not as a list.
//   *
//   * Since: 0.9.2
//   **/
//  void
//  hb_ot_shape_glyphs_closure (Font          *font,
// 				 Buffer        *buffer,
// 				 const  Feature *features,
// 				 unsigned int        num_features,
// 				 hb_set_t           *glyphs)
//  {
//    const char *shapers[] = {"ot", nil};
//    ShapePlan *shape_plan = hb_shape_plan_create_cached (font.Face, &buffer.Props,
// 								  features, num_features, shapers);

//    bool mirror = GetHorizontalDirection (buffer.Props.script) == RightToLeft;

//    unsigned int count = buffer.len;
//    GlyphInfo *info = buffer.Info;
//    for (unsigned int i = 0; i < count; i++)
// 	 add_char (font, buffer.unicode, mirror, info[i].Codepoint, glyphs);

//    hb_set_t *lookups = hb_set_create ();
//    hb_ot_shape_plan_collect_lookups (shape_plan, HB_OT_TAG_GSUB, lookups);
//    hb_ot_layout_lookups_substitute_closure (font.Face, lookups, glyphs);

//    hb_set_destroy (lookups);

//    hb_shape_plan_destroy (shape_plan);
//  }

//  #endif
