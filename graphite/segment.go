package graphite

const MAX_SEG_GROWTH_FACTOR = 64

type charInfo struct {
	before int // slot index before us, comes before
	after  int // slot index after us, comes after
	// featureIndex int  // index into features list in the segment −> Always 0
	char rune // Unicode character from character stream
	// base        int   // index into input string corresponding to this charinfo
	breakWeight int16 // breakweight coming from lb table
	flags       uint8 // 0,1 segment split.
}

func (ch *charInfo) addFlags(val uint8) { ch.flags |= val }

type segment struct {
	face        *graphiteFace
	silf        *silfSubtable // selected subtable
	feats       FeaturesValue
	first, last *slot      // first and last slot in segment
	charinfo    []charInfo // character info, one per input character

	// Position        m_advance;          // whole segment advance
	// SlotRope        m_slots;            // Vector of slot buffers
	// AttributeRope   m_userAttrs;        // Vector of userAttrs buffers
	// JustifyRope     m_justifies;        // Slot justification info buffers
	// FeatureList     m_feats;            // feature settings referenced by charinfos in this segment
	freeSlots  *slot // linked list of free slots
	collisions []slotCollision

	dir int // text direction
	// SlotJustify   * m_freeJustifies;    // Slot justification blocks free list
	// const Face    * m_face;             // GrFace
	// const Silf    * m_silf;
	// size_t          m_bufSize,          // how big a buffer to create when need more slots
	numGlyphs int
	//                 m_numCharinfo;      // size of the array and number of input characters
	defaultOriginal int // number of whitespace chars in the string
	// uint8           m_flags,            // General purpose flags

	passBits uint32 // if bit set then skip pass
}

func (face *graphiteFace) newSegment(text []rune, script Tag, features FeaturesValue, dir int) *segment {
	var seg segment

	// adapt convention
	script = spaceToZero(script)

	// allocate memory
	seg.charinfo = make([]charInfo, len(text))
	seg.numGlyphs = len(text)

	// choose silf
	if len(face.silf) != 0 {
		seg.silf = &face.silf[0]
	} else {
		seg.silf = &silfSubtable{}
	}

	seg.dir = dir
	seg.feats = features

	seg.processRunes(text)
	return &seg
}

func (seg *segment) currdir() bool { return ((seg.dir>>6)^seg.dir)&1 != 0 }

func (seg *segment) mergePassBits(val uint32) { seg.passBits &= val }

func (seg *segment) processRunes(text []rune) {
	for slotID, r := range text {
		gid := seg.face.cmap.Lookup(r)
		if gid == 0 {
			gid = seg.silf.findPdseudoGlyph(r)
		}
		seg.appendSlot(slotID, r, gid)
	}
}

func (seg *segment) newSlot() *slot {
	return new(slot)
}

func (seg *segment) newJustify() *slotJustify {
	return new(slotJustify)
}

func (seg *segment) appendSlot(index int, cid rune, gid GID) {
	sl := seg.newSlot()

	info := &seg.charinfo[index]
	info.char = cid
	// info.featureIndex = featureID
	// info.base = indexFeat
	glyph := seg.face.getGlyph(gid)
	if glyph != nil {
		info.breakWeight = glyph.attrs.get(uint16(seg.silf.AttrBreakWeight))
	}

	sl.setGlyph(seg, gid)
	sl.original, sl.before, sl.after = index, index, index
	if seg.last != nil {
		seg.last.next = sl
	}
	sl.prev = seg.last
	seg.last = sl
	if seg.first == nil {
		seg.first = sl
	}

	if aPassBits := uint16(seg.silf.AttrSkipPasses); glyph != nil && aPassBits != 0 {
		m := uint32(glyph.attrs.get(aPassBits))
		if seg.silf.NumPasses > 16 {
			m |= uint32(glyph.attrs.get(aPassBits+1)) << 16
		}
		seg.mergePassBits(m)
	}
}

// reverse the slots but keep diacritics in their same position after their bases
func (seg *segment) reverseSlots() {
	seg.dir = seg.dir ^ 64 // invert the reverse flag
	if seg.first == seg.last {
		return
	} // skip 0 or 1 glyph runs

	var (
		curr                  = seg.first
		t, tlast, tfirst, out *slot
	)

	for curr != nil && seg.getSlotBidiClass(curr) == 16 {
		curr = curr.next
	}
	if curr == nil {
		return
	}
	tfirst = curr.prev
	tlast = curr

	for curr != nil {
		if seg.getSlotBidiClass(curr) == 16 {
			d := curr.next
			for d != nil && seg.getSlotBidiClass(d) == 16 {
				d = d.next
			}
			if d != nil {
				d = d.prev
			} else {
				d = seg.last
			}
			p := out.next // one after the diacritics. out can't be null
			if p != nil {
				p.prev = d
			} else {
				tlast = d
			}
			t = d.next
			d.next = p
			curr.prev = out
			out.next = curr
		} else { // will always fire first time round the loop
			if out != nil {
				out.prev = curr
			}
			t = curr.next
			curr.next = out
			out = curr
		}
		curr = t
	}
	out.prev = tfirst
	if tfirst != nil {
		tfirst.next = out
	} else {
		seg.first = out
	}
	seg.last = tlast
}

// TODO: check if font is really needed
func (seg *segment) positionSlots(font *graphiteFont, iStart, iEnd *slot, isRtl, isFinal bool) position {
	var (
		currpos    position
		clusterMin float32
		bbox       rect
		reorder    = (seg.currdir() != isRtl)
	)

	if reorder {
		seg.reverseSlots()
		iStart, iEnd = iEnd, iStart
	}
	if iStart == nil {
		iStart = seg.first
	}
	if iEnd == nil {
		iEnd = seg.last
	}

	if iStart == nil || iEnd == nil { // only true for empty segments
		return currpos
	}

	if isRtl {
		for s, end := iEnd, iStart.prev; s != nil && s != end; s = s.prev {
			if s.isBase() {
				clusterMin = currpos.x
				currpos = s.finalise(seg, font, currpos, &bbox, 0, &clusterMin, isRtl, isFinal, 0)
			}
		}
	} else {
		for s, end := iStart, iEnd.next; s != nil && s != end; s = s.next {
			if s.isBase() {
				clusterMin = currpos.x
				currpos = s.finalise(seg, font, currpos, &bbox, 0, &clusterMin, isRtl, isFinal, 0)
			}
		}
	}
	if reorder {
		seg.reverseSlots()
	}
	return currpos
}

func (seg *segment) doMirror(aMirror byte) {
	for s := seg.first; s != nil; s = s.next {
		g := GID(seg.face.getGlyphAttr(s.glyphID, uint16(aMirror)))
		if g != 0 && (seg.dir&4 == 0 || seg.face.getGlyphAttr(s.glyphID, uint16(aMirror)+1) == 0) {
			s.setGlyph(seg, g)
		}
	}
}

func (seg *segment) getSlotBidiClass(s *slot) int8 {
	if res := s.bidiCls; res != -1 {
		return res
	}
	res := int8(seg.face.getGlyphAttr(s.glyphID, uint16(seg.silf.AttrDirectionality)))
	s.bidiCls = res
	return res
}

// check the bounds and return nil if needed
func (seg *segment) getCharInfo(index int) *charInfo {
	if index < len(seg.charinfo) {
		return &seg.charinfo[index]
	}
	return nil
}

// check the bounds and return nil if needed
func (seg *segment) getCollisionInfo(s *slot) *slotCollision {
	if s.index < len(seg.collisions) {
		return &seg.collisions[s.index]
	}
	return nil
}

func (seg *segment) getFeature(findex uint8) int32 {
	if feat, ok := seg.feats.findFeature(Tag(findex)); ok {
		return int32(feat.Value)
	}
	return 0
}

func findRoot(is *slot) *slot {
	s := is
	for ; s.parent != nil; s = s.parent {
	}
	return s
}

func (seg *segment) getGlyphMetric(iSlot *slot, metric, attrLevel uint8, rtl bool) int32 {
	if attrLevel > 0 {
		is := findRoot(iSlot)
		return is.clusterMetric(seg, metric, attrLevel, rtl)
	}
	return seg.face.getGlyphMetric(iSlot.glyphID, metric)
}
