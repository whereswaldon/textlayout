import freetype

fonts = [
    "test/4x6.pcf",
    "test/8x16.pcf.gz",
    "test/charB18.pcf.gz",
    "test/courB18.pcf.gz",
    "test/hanglg16.pcf.gz",
    "test/helvB18.pcf.gz",
    "test/lubB18.pcf.gz",
    "test/ncenB18.pcf.gz",
    "test/orp-italic.pcf.gz",
    "test/timB18.pcf.gz",
    "test/timR24-ISO8859-1.pcf.gz",
    "test/timR24.pcf.gz",
]

print("""
package bitmap

// Code generated by freetype_ref.py from `files`. DO NOT EDIT.
""")

print("var expectedSizes = []fonts.BitmapSize{")
for file in fonts:
    face = freetype.Face(file)
    size = face.available_sizes[0]
    print(f"    {{Height: {size.height}, Width: {size.width}, XPpem: {int(size.x_ppem / 64)}, YPpem: {int(size.y_ppem / 64)} }},")
print("}")

# advances

print("""
// a one array horizontal advance means mono spaced
var aAdvances = []struct{runes []rune; hAdvances []float32; vAdvance float32}{
""")
for file in fonts:
    face = freetype.Face(file)

    # since freetype change the glyph indexes, we use runes as input instead
    runes = []
    for (rune, gid) in face.get_chars():
        runes.append(rune)
    s = ", ".join([str(a) for a in runes])
    print(f"{{ runes : []rune{{ {s} }}, ")

    h_advances = []
    for rune in runes:
        i = face.get_char_index(rune)
        adv = face.get_advance(i, freetype.FT_LOAD_NO_SCALE)
        assert adv % 64 == 0
        h_advances.append(adv / 64)
    if len(set(h_advances)) == 1:  # monospaced
        print(f"hAdvances: []float32{{ {h_advances[0] } }},")
    else:
        s = ", ".join([str(a) for a in h_advances])
        print(f"hAdvances: []float32{{ {s}  }},")

    face = freetype.Face(file)
    v_advances = []
    for i in range(face.num_glyphs):
        adv = face.get_advance(
            i, freetype.FT_LOAD_NO_SCALE | freetype.FT_LOAD_VERTICAL_LAYOUT)
        v_advances.append(adv / 64)
    assert len(set(v_advances)) == 1
    print(f"vAdvance : {v_advances[0]} }},")
print("}")
