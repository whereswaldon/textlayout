// Package truetype provides support for OpenType and TrueType font formats, used in PDF.
//
// It is largely influenced by github.com/ConradIrwin/font and golang.org/x/image/font/sfnt,
// and FreeType2.
package truetype

import (
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/benoitkugler/textlayout/fonts"
)

var Loader fonts.FontLoader = loader{}

var _ fonts.Font = (*Font)(nil)

type loader struct{}

type fixed struct {
	Major int16
	Minor uint16
}

type longdatetime struct {
	SecondsSince1904 uint64
}

var (
	// errMissingHead is returned by ParseOTF when the font has no head section.
	errMissingHead = errors.New("missing head table in font")

	// errInvalidChecksum is returned by ParseOTF if the font's checksum is wrong
	errInvalidChecksum = errors.New("invalid checksum")

	// errUnsupportedFormat is returned from Parse if parsing failed
	errUnsupportedFormat = errors.New("unsupported font format")

	// errMissingTable is returned from *Table if the table does not exist in the font.
	errMissingTable = errors.New("missing table")

	errUnsupportedTableOffsetLength = errors.New("unsupported table offset or length")
	errInvalidDfont                 = errors.New("invalid dfont")
)

// Font represents a SFNT font, which is the underlying representation found
// in .otf and .ttf files.
// SFNT is a container format, which contains a number of tables identified by
// Tags. Depending on the type of glyphs embedded in the file which tables will
// exist. In particular, there's a big different between TrueType glyphs (usually .ttf)
// and CFF/PostScript Type 2 glyphs (usually .otf)
type Font struct {
	// Type represents the kind of glyphs in this font.
	// It is one of TypeTrueType, TypeTrueTypeApple, TypePostScript1, TypeOpenType
	Type Tag

	file fonts.Ressource // source, needed to parse each table

	tables map[Tag]*tableSection // header only, contents is processed on demand
}

// tableSection represents a table within the font file.
type tableSection struct {
	offset  uint32 // Offset into the file this table starts.
	length  uint32 // Length of this table within the file.
	zLength uint32 // Uncompressed length of this table.
}

// HeadTable returns the table corresponding to the 'head' tag.
func (font *Font) HeadTable() (*TableHead, error) {
	s, found := font.tables[tagHead]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return nil, err
	}

	return parseTableHead(buf)
}

// return the 'bhed' table, which is identical to the 'head' table
func (font *Font) bhedTable() (*TableHead, error) {
	s, found := font.tables[tagBhed]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return nil, err
	}

	return parseTableHead(buf)
}

// NameTable returns the table corresponding to the 'name' tag.
func (font *Font) NameTable() (TableName, error) {
	s, found := font.tables[tagName]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return nil, err
	}
	return parseTableName(buf)
}

func (font *Font) HheaTable() (*TableHhea, error) {
	s, found := font.tables[tagHhea]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return nil, err
	}

	return parseTableHhea(buf)
}

func (font *Font) OS2Table() (*TableOS2, error) {
	s, found := font.tables[tagOS2]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return nil, err
	}

	return parseTableOS2(buf)
}

// GposTable returns the Glyph Positioning table identified with the 'GPOS' tag.
func (font *Font) GposTable() (*TableGPOS, error) {
	s, found := font.tables[TagGpos]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return nil, err
	}

	return parseTableGPOS(buf)
}

// GsubTable returns the Glyph Substitution table identified with the 'GSUB' tag.
func (font *Font) GsubTable() (*TableGSUB, error) {
	s, found := font.tables[TagGsub]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return nil, err
	}

	return parseTableGSUB(buf)
}

// GDefTable returns the Glyph Definition table identified with the 'GDEF' tag.
func (font *Font) GDefTable() (TableGDEF, error) {
	s, found := font.tables[TagGdef]
	if !found {
		return TableGDEF{}, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return TableGDEF{}, err
	}

	return parseTableGdef(buf)
}

// CmapTable returns the Character to Glyph Index Mapping table.
func (font *Font) CmapTable() (Cmap, error) {
	s, found := font.tables[tagCmap]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return nil, err
	}

	return parseTableCmap(buf)
}

// PostTable returns the Post table names
func (font *Font) PostTable() (PostTable, error) {
	s, found := font.tables[tagPost]
	if !found {
		return PostTable{}, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return PostTable{}, err
	}

	numGlyph, err := font.numGlyphs()
	if err != nil {
		return PostTable{}, err
	}

	return parseTablePost(buf, numGlyph)
}

func (font *Font) numGlyphs() (uint16, error) {
	maxpSection, found := font.tables[tagMaxp]
	if !found {
		return 0, errMissingTable
	}

	buf, err := font.findTableBuffer(maxpSection)
	if err != nil {
		return 0, err
	}

	return parseMaxpTable(buf)
}

// HtmxTable returns the glyphs widths (array of size numGlyphs),
// expressed in fonts units.
func (font *Font) HtmxTable() ([]int16, error) {
	numGlyph, err := font.numGlyphs()
	if err != nil {
		return nil, err
	}

	hhea, err := font.HheaTable()
	if err != nil {
		return nil, err
	}

	htmxSection, found := font.tables[tagHmtx]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(htmxSection)
	if err != nil {
		return nil, err
	}

	return parseHtmxTable(buf, uint16(hhea.NumOfLongHorMetrics), numGlyph)
}

func (font *Font) TableKern() (TableKernx, error) {
	numGlyph, err := font.numGlyphs()
	if err != nil {
		return nil, err
	}

	section, found := font.tables[tagKern]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(section)
	if err != nil {
		return nil, err
	}

	return parseKernTable(buf, int(numGlyph))
}

// MorxTable parse the AAT 'morx' table.
func (font *Font) MorxTable() (TableMorx, error) {
	s, found := font.tables[tagMorx]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return nil, err
	}

	numGlyph, err := font.numGlyphs()
	if err != nil {
		return nil, err
	}

	return parseTableMorx(buf, int(numGlyph))
}

// KerxTable parse the AAT 'morx' table.
func (font *Font) KerxTable() (TableKernx, error) {
	s, found := font.tables[tagKerx]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return nil, err
	}

	numGlyph, err := font.numGlyphs()
	if err != nil {
		return nil, err
	}

	return parseTableKerx(buf, int(numGlyph))
}

// TableTrak parse the AAT 'trak' table.
func (font *Font) TableTrak() (TableTrak, error) {
	section, found := font.tables[tagTrak]
	if !found {
		return TableTrak{}, errMissingTable
	}

	buf, err := font.findTableBuffer(section)
	if err != nil {
		return TableTrak{}, err
	}

	return parseTrakTable(buf)
}

// VarTable returns the variation table
func (font *Font) VarTable(names TableName) (*TableFvar, error) {
	s, found := font.tables[tagFvar]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return nil, err
	}

	return parseTableFvar(buf, names)
}

func (font *Font) avarTable() (*tableAvar, error) {
	s, found := font.tables[tagAvar]
	if !found {
		return nil, errMissingTable
	}

	buf, err := font.findTableBuffer(s)
	if err != nil {
		return nil, err
	}
	// TODO: check the coherent in numberof axis
	return parseTableAvar(buf)
}

// Parse parses an OpenType or TrueType file and returns a Font.
// The underlying file is still needed to parse the tables, and must not be closed.
// See Loader for support for collections.
func Parse(file fonts.Ressource) (*Font, error) {
	return parseOneFont(file, 0, false)
}

// Load implements fonts.FontLoader. For collection font files (.ttc, .otc),
// multiple fonts may be returned.
func (loader) Load(file fonts.Ressource) (fonts.Fonts, error) {
	_, err := file.Seek(0, io.SeekStart) // file might have been used before
	if err != nil {
		return nil, err
	}

	var bytes [4]byte
	_, err = file.Read(bytes[:])
	if err != nil {
		return nil, err
	}
	magic := newTag(bytes[:])

	file.Seek(0, io.SeekStart)

	var (
		f              *Font
		offsets        []uint32
		relativeOffset bool
	)
	switch magic {
	case SignatureWOFF:
		f, err = parseWOFF(file, 0, false)
	case TypeTrueType, TypeOpenType, TypePostScript1, TypeAppleTrueType:
		f, err = parseOTF(file, 0, false)
	case ttcTag:
		offsets, err = parseTTCHeader(file)
	case dfontResourceDataOffset:
		offsets, err = parseDfont(file)
		relativeOffset = true
	default:
		return nil, errUnsupportedFormat
	}
	if err != nil {
		return nil, err
	}

	// only one font
	if f != nil {
		return fonts.Fonts{f}, nil
	}

	// collection
	out := make(fonts.Fonts, len(offsets))
	for i, o := range offsets {
		out[i], err = parseOneFont(file, o, relativeOffset)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func parseOneFont(file fonts.Ressource, offset uint32, relativeOffset bool) (*Font, error) {
	_, err := file.Seek(int64(offset), io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("invalid offset: %s", err)
	}

	var bytes [4]byte
	_, err = file.Read(bytes[:])
	if err != nil {
		return nil, err
	}
	magic := newTag(bytes[:])
	switch magic {
	case SignatureWOFF:
		return parseWOFF(file, offset, relativeOffset)
	case TypeTrueType, TypeOpenType, TypePostScript1, TypeAppleTrueType:
		return parseOTF(file, offset, relativeOffset)
	default:
		// no more collections allowed here
		return nil, errUnsupportedFormat
	}
}

func (font *Font) findTableBuffer(s *tableSection) ([]byte, error) {
	var buf []byte

	if s.length != 0 && s.length < s.zLength {
		zbuf := io.NewSectionReader(font.file, int64(s.offset), int64(s.length))
		r, err := zlib.NewReader(zbuf)
		if err != nil {
			return nil, err
		}
		defer r.Close()

		buf = make([]byte, s.zLength, s.zLength)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
	} else {
		buf = make([]byte, s.length, s.length)
		if _, err := font.file.ReadAt(buf, int64(s.offset)); err != nil {
			return nil, err
		}
	}
	return buf, nil
}

// HasTable returns `true` is the font has the given table.
func (f *Font) HasTable(tag Tag) bool {
	_, has := f.tables[tag]
	return has
}

func (f *Font) PostscriptInfo() (fonts.PSInfo, bool) {
	return fonts.PSInfo{}, false
}

// PoscriptName returns the optional PoscriptName of the font
func (f *Font) PoscriptName() string {
	// adapted from freetype

	// TODO: support multiple masters

	// scan the name table to see whether we have a Postscript name here,
	// either in Macintosh or Windows platform encodings
	names, err := f.NameTable()
	if err != nil {
		return ""
	}

	windows, mac := names.getEntry(NamePostscript)

	// prefer Windows entries over Apple
	if windows != nil {
		return windows.String()
	}
	if mac != nil {
		return mac.String()
	}
	return ""
}

// TODO: polish and cache on the font
type fontDetails struct {
	hasOutline, hasColor bool
	head                 *TableHead
	os2                  *TableOS2
}

// load various tables to compute meta data
func (f *Font) analyze() (fontDetails, error) {
	var out fontDetails
	if f.HasTable(tagCBLC) || f.HasTable(tagSbix) || f.HasTable(tagCOLR) {
		out.hasColor = true
	}

	// do we have outlines in there ?
	out.hasOutline = f.HasTable(tagGlyf) || f.HasTable(tagCFF) || f.HasTable(tagCFF2)

	isAppleSbix := f.HasTable(tagSbix)

	// Apple 'sbix' color bitmaps are rendered scaled and then the 'glyf'
	// outline rendered on top.  We don't support that yet, so just ignore
	// the 'glyf' outline and advertise it as a bitmap-only font.
	if isAppleSbix {
		out.hasOutline = false
	}

	var (
		isAppleSbit bool
		err         error
	)
	// if this font doesn't contain outlines, we try to load
	// a `bhed' table
	if !out.hasOutline {
		out.head, err = f.bhedTable()
		isAppleSbit = err == nil
	}

	// load the font header (`head' table) if this isn't an Apple
	// sbit font file
	if !isAppleSbit || isAppleSbix {
		out.head, err = f.HeadTable()
		if err != nil {
			return out, err
		}
	}

	hasCblc := f.HasTable(tagCBLC)
	hasCbdt := f.HasTable(tagCBDT)

	// Ignore outlines for CBLC/CBDT fonts.
	if hasCblc || hasCbdt {
		out.hasOutline = false
	}

	// OpenType 1.8.2 introduced limits to this value;
	// however, they make sense for older SFNT fonts also
	if out.head.UnitsPerEm < 16 || out.head.UnitsPerEm > 16384 {
		return out, fmt.Errorf("invalid UnitsPerEm value %d", out.head.UnitsPerEm)
	}

	// TODO: check if this is needed
	// /* the following tables are often not present in embedded TrueType */
	// /* fonts within PDF documents, so don't check for them.            */
	// LOAD_(maxp)
	// LOAD_(cmap)

	// /* the following tables are optional in PCL fonts -- */
	// /* don't check for errors                            */
	// LOAD_(name)
	// LOAD_(post)

	// do not load the metrics headers and tables if this is an Apple
	// sbit font file
	if isAppleSbit {
		return out, nil
	}

	// load the `hhea' and `hmtx' tables
	_, err = f.HheaTable()
	if err == nil {
		_, err = f.HtmxTable()
		if err != nil {
			return out, err
		}
	} else {
		// No `hhea' table necessary for SFNT Mac fonts.
		if f.Type == TypeAppleTrueType {
			out.hasOutline = false
		} else {
			return out, errors.New("horizontal header is missing")
		}
	}

	// TODO:
	// try to load the `vhea' and `vmtx' tables
	// LOADM_(hhea, 1)
	// if !error {
	// 	LOADM_(hmtx, 1)
	// 	if !error {
	// 		face.vertical_info = 1
	// 	}
	// }
	// if error && FT_ERR_NEQ(error, Table_Missing) {
	// 	goto Exit
	// }

	out.os2, _ = f.OS2Table() // we treat the table as missing if there are any errors
	return out, nil
}

// TODO: handle the error in a first processing step (distinct from Parse though)
func (f *Font) Style() (isItalic, isBold bool, familyName, styleName string) {
	details, _ := f.analyze()
	names, _ := f.NameTable()

	// Bit 8 of the `fsSelection' field in the `OS/2' table denotes
	// a WWS-only font face.  `WWS' stands for `weight', width', and
	// `slope', a term used by Microsoft's Windows Presentation
	// Foundation (WPF).  This flag has been introduced in version
	// 1.5 of the OpenType specification (May 2008).

	if details.os2 != nil && details.os2.FsSelection&256 != 0 {
		familyName = names.getName(NamePreferredFamily)
		if familyName == "" {
			familyName = names.getName(NameFontFamily)
		}

		styleName = names.getName(NamePreferredSubfamily)
		if styleName == "" {
			styleName = names.getName(NameFontSubfamily)
		}
	} else {
		familyName = names.getName(NameWWSFamily)
		if familyName == "" {
			familyName = names.getName(NamePreferredFamily)
		}
		if familyName == "" {
			familyName = names.getName(NameFontFamily)
		}

		styleName = names.getName(NameWWSSubfamily)
		if styleName == "" {
			styleName = names.getName(NamePreferredSubfamily)
		}
		if styleName == "" {
			styleName = names.getName(NameFontSubfamily)
		}
	}

	styleName = strings.TrimSpace(styleName)
	if styleName == "" { // assume `Regular' style because we don't know better
		styleName = "Regular"
	}

	// Compute style flags.
	if details.hasOutline && details.os2 != nil {
		// We have an OS/2 table; use the `fsSelection' field.  Bit 9
		// indicates an oblique font face.  This flag has been
		// introduced in version 1.5 of the OpenType specification.
		isItalic = details.os2.FsSelection&(1<<9) != 0 || details.os2.FsSelection&1 != 0
		isBold = details.os2.FsSelection&(1<<5) != 0
	} else if details.head != nil { // TODO: remove when error is handled
		// this is an old Mac font, use the header field
		isBold = details.head.MacStyle&1 != 0
		isItalic = details.head.MacStyle&2 != 0
	}

	return
}

func (f *Font) GlyphKind() (scalable, bitmap, color bool) {
	// TODO: support for bitmap
	details, _ := f.analyze()
	return details.hasOutline, false, details.hasColor
}
