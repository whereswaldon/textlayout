package truetype

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/benoitkugler/textlayout/fonts"
)

const maxCompositeNesting = 20 // protect against malicious fonts

type TableGlyf []GlyphData // length numGlyphs

// shared with gvar and sbix.
// return an error only if data is not long enough
func parseTableLoca(data []byte, numGlyphs int, isLong bool) ([]uint32, error) {
	var size int
	if isLong {
		size = (numGlyphs + 1) * 4
	} else {
		size = (numGlyphs + 1) * 2
	}
	if len(data) < size {
		return nil, errors.New("invalid location table (EOF)")
	}
	out := make([]uint32, numGlyphs+1)
	if isLong {
		for i := range out {
			out[i] = binary.BigEndian.Uint32(data[4*i:])
		}
	} else {
		for i := range out {
			out[i] = 2 * uint32(binary.BigEndian.Uint16(data[2*i:])) // The actual local offset divided by 2 is stored.
		}
	}
	return out, nil
}

// locaOffsets has length numGlyphs + 1
func parseTableGlyf(data []byte, locaOffsets []uint32) (TableGlyf, error) {
	out := make(TableGlyf, len(locaOffsets)-1)
	var err error
	for i := range out {
		// If a glyph has no outline, then loca[n] = loca [n+1].
		if locaOffsets[i] == locaOffsets[i+1] {
			continue
		}
		out[i], err = parseGlyphData(data, locaOffsets[i])
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

type contourPoint struct {
	x, y       float32
	isEndPoint bool
	isExplicit bool // this point is referenced, i.e., explicit deltas specified */
}

func (c *contourPoint) translate(x, y float32) {
	c.x += x
	c.y += y
}

func (c *contourPoint) transform(matrix [4]float32) {
	px := c.x*matrix[0] + c.y*matrix[2]
	c.y = c.x*matrix[1] + c.y*matrix[3]
	c.x = px
}

type GlyphData struct {
	data interface{ isGlyphData() } // nil for absent glyphs

	Xmin, Ymin, Xmax, Ymax int16
}

func (simpleGlyphData) isGlyphData()    {}
func (compositeGlyphData) isGlyphData() {}

// does not includes phantom points
func (g GlyphData) pointNumbersCount() int {
	switch g := g.data.(type) {
	case simpleGlyphData:
		return len(g.points)
	case compositeGlyphData:
		/* pseudo component points for each component in composite glyph */
		return len(g.glyphs)
	}
	return 0
}

func (g GlyphData) getExtents(hmtx tableHVmtx, gid fonts.GlyphIndex) fonts.GlyphExtents {
	var extents fonts.GlyphExtents
	/* Undocumented rasterizer behavior: shift glyph to the left by (lsb - xMin), i.e., xMin = lsb */
	/* extents.x_bearing = hb_min (glyph_header.xMin, glyph_header.xMax); */
	if int(gid) < len(hmtx) {
		extents.XBearing = float32(hmtx[gid].SideBearing)
	}
	extents.YBearing = float32(max16(g.Ymin, g.Ymax))
	extents.Width = float32(max16(g.Xmin, g.Xmax) - min16(g.Xmin, g.Xmax))
	extents.Height = float32(min16(g.Ymin, g.Ymax) - max16(g.Ymin, g.Ymax))
	return extents
}

func parseGlyphData(data []byte, offset uint32) (out GlyphData, err error) {
	if len(data) < int(offset)+10 {
		return out, errors.New("invalid 'glyf' table (EOF)")
	}
	data = data[offset:]
	numberOfContours := int(int16(binary.BigEndian.Uint16(data))) // careful with the conversion to signed integer
	out.Xmin = int16(binary.BigEndian.Uint16(data[2:]))
	out.Ymin = int16(binary.BigEndian.Uint16(data[4:]))
	out.Xmax = int16(binary.BigEndian.Uint16(data[6:]))
	out.Ymax = int16(binary.BigEndian.Uint16(data[8:]))
	if numberOfContours >= 0 { // simple glyph
		out.data, err = parseSimpleGlyphData(data[10:], numberOfContours)
	} else { // composite glyph
		out.data, err = parseCompositeGlyphData(data[10:])
	}
	return out, err
}

type glyphContourPoint struct {
	flag uint8
	x, y int16
}

type simpleGlyphData struct {
	endPtsOfContours []uint16 // valid indexes in `points` after parsing
	instructions     []byte
	points           []glyphContourPoint
}

// return all the contour points, without phantoms
func (sg simpleGlyphData) getContourPoints() []contourPoint {
	points := make([]contourPoint, len(sg.points))
	for _, end := range sg.endPtsOfContours {
		points[end].isEndPoint = true
	}
	for i, p := range sg.points {
		points[i].x, points[i].y = float32(p.x), float32(p.y)
	}
	return points
}

// update points and return the data after the points
func parseGlyphContourPoints(data []byte, points []glyphContourPoint, setter func(i int, v int16), shortFlag, sameFlag uint8) ([]byte, error) {
	var v int16 // coordinates are relative to the previous
	for i, p := range points {
		flag := p.flag
		if flag&shortFlag != 0 {
			if len(data) == 0 {
				return nil, errors.New("invalid simple glyph data points (EOF)")
			}
			val := data[0]
			data = data[1:]
			if flag&sameFlag != 0 {
				v += int16(val)
			} else {
				v -= int16(val)
			}
		} else {
			if flag&sameFlag == 0 {
				if len(data) < 2 {
					return nil, errors.New("invalid simple glyph data points (EOF)")
				}
				val := binary.BigEndian.Uint16(data)
				data = data[2:]
				v += int16(val)
			}
		}
		setter(i, v)
	}
	return data, nil
}

// data starts after the glyph header
func parseSimpleGlyphData(data []byte, numberOfContours int) (out simpleGlyphData, err error) {
	out.endPtsOfContours, err = parseUint16s(data, numberOfContours)
	if err != nil {
		return out, fmt.Errorf("invalid simple glyph data: %s", err)
	}
	if !sort.SliceIsSorted(out.endPtsOfContours, func(i, j int) bool {
		return out.endPtsOfContours[i] < out.endPtsOfContours[j]
	}) {
		return out, errors.New("invalid simple glyph data end points")
	}

	out.instructions, data, err = parseGlyphInstruction(data[2*numberOfContours:])
	if err != nil {
		return out, fmt.Errorf("invalid simple glyph data: %s", err)
	}

	if len(out.endPtsOfContours) == 0 {
		return out, nil
	}

	numPoints := int(out.endPtsOfContours[len(out.endPtsOfContours)-1]) + 1

	const (
		xShortVector                  = 0x02
		yShortVector                  = 0x04
		repeatFlag                    = 0x08
		xIsSameOrPositiveXShortVector = 0x10
		yIsSameOrPositiveYShortVector = 0x20
	)

	out.points = make([]glyphContourPoint, numPoints)

	// read flags
	for i := 0; i < numPoints; i++ {
		if len(data) == 0 {
			return out, errors.New("invalid simple glyph data flags (EOF)")
		}
		flag := data[0]
		out.points[i].flag = flag
		data = data[1:]

		if flag&repeatFlag != 0 {
			if len(data) == 0 {
				return out, errors.New("invalid simple glyph data flags (EOF)")
			}
			repeatCount := int(data[0])
			data = data[1:]
			if i+repeatCount+1 > numPoints { // gracefully handle out of bounds
				repeatCount = numPoints - i - 1
			}
			subSlice := out.points[i+1 : i+repeatCount+1]
			for j := range subSlice {
				subSlice[j].flag = flag
			}
			i += repeatCount
		}
	}

	// read x coordinates
	data, err = parseGlyphContourPoints(data, out.points, func(i int, v int16) { out.points[i].x = v },
		xShortVector, xIsSameOrPositiveXShortVector)
	if err != nil {
		return out, err
	}
	// read y coordinates
	_, err = parseGlyphContourPoints(data, out.points, func(i int, v int16) { out.points[i].y = v },
		yShortVector, yIsSameOrPositiveYShortVector)

	return out, err
}

type compositeGlyphData struct {
	glyphs       []compositeGlyphPart
	instructions []byte
}

type compositeGlyphPart struct {
	flags      uint16
	glyphIndex fonts.GlyphIndex
	arg1, arg2 uint16     // before interpretation
	scale      [4]float32 // x, 01, 10, y
}

func (c *compositeGlyphPart) hasUseMyMetrics() bool {
	const useMyMetrics = 0x0200
	return c.flags&useMyMetrics != 0
}

// return true if arg1 and arg2 indicated an anchor point,
// not offsets
func (c *compositeGlyphPart) isAnchored() bool {
	const argsAreXyValues = 0x0002
	return c.flags&argsAreXyValues == 0
}

func (c *compositeGlyphPart) isScaledOffsets() bool {
	const (
		scaledComponentOffset   = 0x0800
		unscaledComponentOffset = 0x1000
	)
	return c.flags&(scaledComponentOffset|unscaledComponentOffset) == scaledComponentOffset
}

func (c *compositeGlyphPart) transformPoints(points []contourPoint) {
	if c.isAnchored() {
		return
	}
	transX, transY := float32(int16(c.arg1)), float32(int16(c.arg2))
	scale := c.scale
	if c.isScaledOffsets() {
		for i := range points {
			points[i].translate(transX, transY)
			points[i].transform(scale)
		}
	} else {
		for i := range points {
			points[i].transform(scale)
			points[i].translate(transX, transY)
		}
	}
}

// data starts after the glyph header
func parseCompositeGlyphData(data []byte) (out compositeGlyphData, err error) {
	const (
		arg1And2AreWords = 1 << iota
		_
		_
		weHaveAScale
		_
		moreComponents
		weHaveAnXAndYScale
		weHaveATwoByTwo
		weHaveInstructions
	)
	var flags uint16
	for do := true; do; do = flags&moreComponents != 0 {
		var part compositeGlyphPart

		if len(data) < 4 {
			return out, errors.New("invalid composite glyph data (EOF)")
		}
		flags = binary.BigEndian.Uint16(data)
		part.glyphIndex = fonts.GlyphIndex(binary.BigEndian.Uint16(data[2:]))

		if flags&arg1And2AreWords != 0 { // 16 bits
			if len(data) < 4+4 {
				return out, errors.New("invalid composite glyph data (EOF)")
			}
			part.arg1 = binary.BigEndian.Uint16(data[4:])
			part.arg2 = binary.BigEndian.Uint16(data[6:])
			data = data[8:]
		} else {
			if len(data) < 4+2 {
				return out, errors.New("invalid composite glyph data (EOF)")
			}
			part.arg1 = uint16(data[4])
			part.arg2 = uint16(data[5])
			data = data[6:]
		}

		part.scale[0], part.scale[3] = 1, 1
		if flags&weHaveAScale != 0 {
			if len(data) < 2 {
				return out, errors.New("invalid composite glyph data (EOF)")
			}
			part.scale[0] = fixed214ToFloat(binary.BigEndian.Uint16(data))
			part.scale[3] = part.scale[0]
			data = data[2:]
		} else if flags&weHaveAnXAndYScale != 0 {
			if len(data) < 4 {
				return out, errors.New("invalid composite glyph data (EOF)")
			}
			part.scale[0] = fixed214ToFloat(binary.BigEndian.Uint16(data))
			part.scale[3] = fixed214ToFloat(binary.BigEndian.Uint16(data[2:]))
			data = data[4:]
		} else if flags&weHaveATwoByTwo != 0 {
			if len(data) < 8 {
				return out, errors.New("invalid composite glyph data (EOF)")
			}
			part.scale[0] = fixed214ToFloat(binary.BigEndian.Uint16(data))
			part.scale[1] = fixed214ToFloat(binary.BigEndian.Uint16(data[2:]))
			part.scale[2] = fixed214ToFloat(binary.BigEndian.Uint16(data[4:]))
			part.scale[3] = fixed214ToFloat(binary.BigEndian.Uint16(data[6:]))
			data = data[8:]
		}

		out.glyphs = append(out.glyphs, part)
	}
	if flags&weHaveInstructions != 0 {
		out.instructions, _, err = parseGlyphInstruction(data)
		if err != nil {
			return out, fmt.Errorf("invalid composite glyph data: %s", err)
		}
	}
	return out, nil
}

func parseGlyphInstruction(data []byte) ([]byte, []byte, error) {
	if len(data) < 2 {
		return nil, nil, errors.New("invalid glyph instructions (EOF)")
	}
	instructionLength := int(binary.BigEndian.Uint16(data))
	if len(data) < 2+instructionLength {
		return nil, nil, errors.New("invalid glyph instructions (EOF)")
	}
	return data[2 : 2+instructionLength], data[2+instructionLength:], nil
}
