package harfbuzz

import (
	"encoding/hex"
	"strings"

	"github.com/benoitkugler/textlayout/fonts/truetype"
	"github.com/benoitkugler/textlayout/language"
)

// ported from harfbuzz/src/hb-ot-tag.cc Copyright © 2009  Red Hat, Inc. 2011  Google, Inc. Behdad Esfahbod, Roozbeh Pournader

var (
	// OpenType script tag, `DFLT`, for features that are not script-specific.
	HB_OT_TAG_DEFAULT_SCRIPT = newTag('D', 'F', 'L', 'T')
	// OpenType language tag, `dflt`. Not a valid language tag, but some fonts
	// mistakenly use it.
	HB_OT_TAG_DEFAULT_LANGUAGE = newTag('d', 'f', 'l', 't')
)

//  /* hb_script_t */

func oldTagFromScript(script hb_script_t) hb_tag_t {
	/* This seems to be accurate as of end of 2012. */

	switch script {
	case 0:
		return HB_OT_TAG_DEFAULT_SCRIPT

	/* KATAKANA and HIRAGANA both map to 'kana' */
	case language.Hiragana:
		return newTag('k', 'a', 'n', 'a')

	/* Spaces at the end are preserved, unlike ISO 15924 */
	case language.Lao:
		return newTag('l', 'a', 'o', ' ')
	case language.Yi:
		return newTag('y', 'i', ' ', ' ')
	/* Unicode-5.0 additions */
	case language.Nko:
		return newTag('n', 'k', 'o', ' ')
	/* Unicode-5.1 additions */
	case language.Vai:
		return newTag('v', 'a', 'i', ' ')
	}

	/* Else, just change first char to lowercase and return */
	return hb_tag_t(script | 0x20000000)
}

//  static hb_script_t
//  hb_ot_old_tag_to_script (hb_tag_t tag)
//  {
//    if (unlikely (tag == HB_OT_TAG_DEFAULT_SCRIPT))
// 	 return HB_SCRIPT_INVALID;

//    /* This side of the conversion is fully algorithmic. */

//    /* Any spaces at the end of the tag are replaced by repeating the last
// 	* letter.  Eg 'nko ' -> 'Nkoo' */
//    if (unlikely ((tag & 0x0000FF00u) == 0x00002000u))
// 	 tag |= (tag >> 8) & 0x0000FF00u; /* Copy second letter to third */
//    if (unlikely ((tag & 0x000000FFu) == 0x00000020u))
// 	 tag |= (tag >> 8) & 0x000000FFu; /* Copy third letter to fourth */

//    /* Change first char to uppercase and return */
//    return (hb_script_t) (tag & ~0x20000000u);
//  }

func newTagFromScript(script hb_script_t) hb_tag_t {
	switch script {
	case language.Bengali:
		return newTag('b', 'n', 'g', '2')
	case language.Devanagari:
		return newTag('d', 'e', 'v', '2')
	case language.Gujarati:
		return newTag('g', 'j', 'r', '2')
	case language.Gurmukhi:
		return newTag('g', 'u', 'r', '2')
	case language.Kannada:
		return newTag('k', 'n', 'd', '2')
	case language.Malayalam:
		return newTag('m', 'l', 'm', '2')
	case language.Oriya:
		return newTag('o', 'r', 'y', '2')
	case language.Tamil:
		return newTag('t', 'm', 'l', '2')
	case language.Telugu:
		return newTag('t', 'e', 'l', '2')
	case language.Myanmar:
		return newTag('m', 'y', 'm', '2')
	}

	return HB_OT_TAG_DEFAULT_SCRIPT
}

//  static hb_script_t
//  hb_ot_new_tag_to_script (hb_tag_t tag)
//  {
//    switch (tag) {
// 	 case newTag('b','n','g','2'):	return HB_SCRIPT_BENGALI;
// 	 case newTag('d','e','v','2'):	return HB_SCRIPT_DEVANAGARI;
// 	 case newTag('g','j','r','2'):	return HB_SCRIPT_GUJARATI;
// 	 case newTag('g','u','r','2'):	return HB_SCRIPT_GURMUKHI;
// 	 case newTag('k','n','d','2'):	return HB_SCRIPT_KANNADA;
// 	 case newTag('m','l','m','2'):	return HB_SCRIPT_MALAYALAM;
// 	 case newTag('o','r','y','2'):	return HB_SCRIPT_ORIYA;
// 	 case newTag('t','m','l','2'):	return HB_SCRIPT_TAMIL;
// 	 case newTag('t','e','l','2'):	return HB_SCRIPT_TELUGU;
// 	 case newTag('m','y','m','2'):	return HB_SCRIPT_MYANMAR;
//    }

//    return HB_SCRIPT_UNKNOWN;
//  }

//  #ifndef HB_DISABLE_DEPRECATED
//  void
//  hb_ot_tags_from_script (hb_script_t  script,
// 			 hb_tag_t    *script_tag_1,
// 			 hb_tag_t    *script_tag_2)
//  {
//    unsigned int count = 2;
//    hb_tag_t tags[2];
//    hb_ot_tags_from_script_and_language (script, HB_LANGUAGE_INVALID, &count, tags, nullptr, nullptr);
//    *script_tag_1 = count > 0 ? tags[0] : HB_OT_TAG_DEFAULT_SCRIPT;
//    *script_tag_2 = count > 1 ? tags[1] : HB_OT_TAG_DEFAULT_SCRIPT;
//  }
//  #endif

//  /*
//   * Complete list at:
//   * https://docs.microsoft.com/en-us/typography/opentype/spec/scripttags
//   *
//   * Most of the script tags are the same as the ISO 15924 tag but lowercased.
//   * So we just do that, and handle the exceptional cases in a switch.
//   */

func allTagsFromScript(script hb_script_t) []hb_tag_t {
	var tags []hb_tag_t

	tag := newTagFromScript(script)
	if tag != HB_OT_TAG_DEFAULT_SCRIPT {
		// HB_SCRIPT_MYANMAR maps to 'mym2', but there is no 'mym3'.
		if tag != newTag('m', 'y', 'm', '2') {
			tags = append(tags, tag|'3')
		}
		tags = append(tags, tag)
	}

	oldTag := oldTagFromScript(script)
	if oldTag != HB_OT_TAG_DEFAULT_SCRIPT {
		tags = append(tags, oldTag)
	}
	return tags
}

//  /**
//   * hb_ot_tag_to_script:
//   * @tag: a script tag
//   *
//   * Converts a script tag to an #hb_script_t.
//   *
//   * Return value: The #hb_script_t corresponding to @tag.
//   *
//   **/
//  hb_script_t
//  hb_ot_tag_to_script (hb_tag_t tag)
//  {
//    unsigned char digit = tag & 0x000000FFu;
//    if (unlikely (digit == '2' || digit == '3'))
// 	 return hb_ot_new_tag_to_script (tag & 0xFFFFFF32);

//    return hb_ot_old_tag_to_script (tag);
//  }

//  /* hb_language_t */

//  struct LangTag
//  {
//    char language[4];
//    hb_tag_t tag;

//    int cmp (const char *a) const
//    {
// 	 const char *b = this->language;
// 	 unsigned int da, db;
// 	 const char *p;

// 	 p = strchr (a, '-');
// 	 da = p ? (unsigned int) (p - a) : strlen (a);

// 	 p = strchr (b, '-');
// 	 db = p ? (unsigned int) (p - b) : strlen (b);

// 	 return strncmp (a, b, max (da, db));
//    }
//    int cmp (const LangTag *that) const
//    { return cmp (that->language); }
//  };

//  #include "hb-ot-tag-table.hh"

//  /* The corresponding languages IDs for the following IDs are unclear,
//   * overlap, or are architecturally weird. Needs more research. */

//  /*{"??",	{newTag('B','C','R',' ')}},*/	/* Bible Cree */
//  /*{"zh?",	{newTag('C','H','N',' ')}},*/	/* Chinese (seen in Microsoft fonts) */
//  /*{"ar-Syrc?",	{newTag('G','A','R',' ')}},*/	/* Garshuni */
//  /*{"??",	{newTag('N','G','R',' ')}},*/	/* Nagari */
//  /*{"??",	{newTag('Y','I','C',' ')}},*/	/* Yi Classic */
//  /*{"zh?",	{newTag('Z','H','P',' ')}},*/	/* Chinese Phonetic */

//  #ifndef HB_DISABLE_DEPRECATED
//  hb_tag_t
//  hb_ot_tag_from_language (hb_language_t language)
//  {
//    unsigned int count = 1;
//    hb_tag_t tags[1];
//    hb_ot_tags_from_script_and_language (HB_SCRIPT_UNKNOWN, language, nullptr, nullptr, &count, tags);
//    return count > 0 ? tags[0] : HB_OT_TAG_DEFAULT_LANGUAGE;
//  }
//  #endif

func hb_ot_tags_from_language(lang_str string, limit int) []hb_tag_t {
	// check for matches of multiple subtags.
	if tags := hb_ot_tags_from_complex_language(lang_str, limit); len(tags) != 0 {
		return tags
	}

	// find a language matching in the first component.
	s := strings.IndexByte(lang_str, '-')
	if s != -1 && limit >= 6 {
		extlangEnd := strings.IndexByte(lang_str[s+1:], '-')
		// if there is an extended language tag, use it.
		ref := extlangEnd - s - 1
		if extlangEnd == -1 {
			ref = len(lang_str[s+1:])
		}
		if ref == 3 && isAlpha(lang_str[s+1]) {
			lang_str = lang_str[s+1:]
		}
	}

	if tag_idx := bfindLanguage(lang_str); tag_idx != -1 {
		for tag_idx != 0 && ot_languages[tag_idx].language == ot_languages[tag_idx-1].language {
			tag_idx--
		}
		var out []hb_tag_t
		for i := 0; tag_idx+i < len(ot_languages) &&
			ot_languages[tag_idx+i].tag != 0 &&
			ot_languages[tag_idx+i].language == ot_languages[tag_idx].language; i++ {
			out = append(out, ot_languages[tag_idx+i].tag)
		}
		return out
	}

	if s == -1 {
		s = len(lang_str)
	}
	if s == 3 {
		// assume it's ISO-639-3 and upper-case and use it.
		return []hb_tag_t{newTag(lang_str[0], lang_str[1], lang_str[2], ' ') & ^truetype.Tag(0x20202000)}
	}

	return nil
}

// return 0 if no tag
func parse_private_use_subtag(private_use_subtag string, prefix string, normalize func(byte) byte) (hb_tag_t, bool) {

	s := strings.Index(private_use_subtag, prefix)
	if s == -1 {
		return 0, false
	}

	var tag [4]byte
	s += len(prefix)
	if private_use_subtag[s] == '-' {
		s += 1
		nb, _ := hex.Decode(tag[:], []byte(private_use_subtag[s:]))
		if nb != 8 {
			return 0, false
		}
	} else {
		var i int
		for ; i < 4 && isAlnum(private_use_subtag[s+i]); i++ {
			tag[i] = normalize(private_use_subtag[s+i])
		}
		if i == 0 {
			return 0, false
		}

		for ; i < 4; i++ {
			tag[i] = ' '
		}
	}
	out := newTag(tag[0], tag[1], tag[2], tag[3])
	if (out & 0xDFDFDFDF) == HB_OT_TAG_DEFAULT_SCRIPT {
		out ^= ^truetype.Tag(0xDFDFDFDF)
	}
	return out, true
}

// hb_ot_tags_from_script_and_language converts an `hb_script_t` and an `hb_language_t`
// to script and language tags.
func hb_ot_tags_from_script_and_language(script hb_script_t, language hb_language_t) (scriptTags, languageTags []hb_tag_t) {
	if language != "" {
		lang_str := hb_language_to_string(language)
		limit := -1
		private_use_subtag := ""
		if lang_str[0] == 'x' && lang_str[1] == '-' {
			private_use_subtag = lang_str
		} else {
			var s int
			for s = 1; s < len(lang_str); s++ { // s index in lang_str
				if lang_str[s-1] == '-' && lang_str[s+1] == '-' {
					if lang_str[s] == 'x' {
						private_use_subtag = lang_str[s:]
						if limit == -1 {
							limit = s - 1
						}
						break
					} else if limit == -1 {
						limit = s - 1
					}
				}
			}
			if limit == -1 {
				limit = s
			}
		}

		s, hasScript := parse_private_use_subtag(private_use_subtag, "-hbsc", toLower)
		if hasScript {
			scriptTags = []hb_tag_t{s}
		}

		l, hasLanguage := parse_private_use_subtag(private_use_subtag, "-hbot", toUpper)
		if hasLanguage {
			languageTags = append(languageTags, l)
		} else {
			languageTags = hb_ot_tags_from_language(lang_str, limit)
		}
	}

	if len(scriptTags) == 0 {
		scriptTags = allTagsFromScript(script)
	}
	return
}

//  /**
//   * hb_ot_tag_to_language:
//   * @tag: an language tag
//   *
//   * Converts a language tag to an #hb_language_t.
//   *
//   * Return value: (transfer none) (nullable):
//   * The #hb_language_t corresponding to @tag.
//   *
//   * Since: 0.9.2
//   **/
//  hb_language_t
//  hb_ot_tag_to_language (hb_tag_t tag)
//  {
//    unsigned int i;

//    if (tag == HB_OT_TAG_DEFAULT_LANGUAGE)
// 	 return nullptr;

//    {
// 	 hb_language_t disambiguated_tag = hb_ot_ambiguous_tag_to_language (tag);
// 	 if (disambiguated_tag != HB_LANGUAGE_INVALID)
// 	   return disambiguated_tag;
//    }

//    for (i = 0; i < ARRAY_LENGTH (ot_languages); i++)
// 	 if (ot_languages[i].tag == tag)
// 	   return hb_language_from_string (ot_languages[i].language, -1);

//    /* Return a custom language in the form of "x-hbot-AABBCCDD".
// 	* If it's three letters long, also guess it's ISO 639-3 and lower-case and
// 	* prepend it (if it's not a registered tag, the private use subtags will
// 	* ensure that calling hb_ot_tag_from_language on the result will still return
// 	* the same tag as the original tag).
// 	*/
//    {
// 	 char buf[20];
// 	 char *str = buf;
// 	 if (ISALPHA (tag >> 24)
// 	 && ISALPHA ((tag >> 16) & 0xFF)
// 	 && ISALPHA ((tag >> 8) & 0xFF)
// 	 && (tag & 0xFF) == ' ')
// 	 {
// 	   buf[0] = TOLOWER (tag >> 24);
// 	   buf[1] = TOLOWER ((tag >> 16) & 0xFF);
// 	   buf[2] = TOLOWER ((tag >> 8) & 0xFF);
// 	   buf[3] = '-';
// 	   str += 4;
// 	 }
// 	 snprintf (str, 16, "x-hbot-%08x", tag);
// 	 return hb_language_from_string (&*buf, -1);
//    }
//  }

//  /**
//   * hb_ot_tags_to_script_and_language:
//   * @script_tag: a script tag
//   * @language_tag: a language tag
//   * @script: (out) (optional): the #hb_script_t corresponding to @script_tag.
//   * @language: (out) (optional): the #hb_language_t corresponding to @script_tag and
//   * @language_tag.
//   *
//   * Converts a script tag and a language tag to an #hb_script_t and an
//   * #hb_language_t.
//   *
//   * Since: 2.0.0
//   **/
//  void
//  hb_ot_tags_to_script_and_language (hb_tag_t       script_tag,
// 					hb_tag_t       language_tag,
// 					hb_script_t   *script /* OUT */,
// 					hb_language_t *language /* OUT */)
//  {
//    hb_script_t script_out = hb_ot_tag_to_script (script_tag);
//    if (script)
// 	 *script = script_out;
//    if (language)
//    {
// 	 unsigned int script_count = 1;
// 	 hb_tag_t primary_script_tag[1];
// 	 hb_ot_tags_from_script_and_language (script_out,
// 					  HB_LANGUAGE_INVALID,
// 					  &script_count,
// 					  primary_script_tag,
// 					  nullptr, nullptr);
// 	 *language = hb_ot_tag_to_language (language_tag);
// 	 if (script_count == 0 || primary_script_tag[0] != script_tag)
// 	 {
// 	   unsigned char *buf;
// 	   const char *lang_str = hb_language_to_string (*language);
// 	   size_t len = strlen (lang_str);
// 	   buf = (unsigned char *) malloc (len + 16);
// 	   if (unlikely (!buf))
// 	   {
// 	 *language = nullptr;
// 	   }
// 	   else
// 	   {
// 	 int shift;
// 	 memcpy (buf, lang_str, len);
// 	 if (lang_str[0] != 'x' || lang_str[1] != '-') {
// 	   buf[len++] = '-';
// 	   buf[len++] = 'x';
// 	 }
// 	 buf[len++] = '-';
// 	 buf[len++] = 'h';
// 	 buf[len++] = 'b';
// 	 buf[len++] = 's';
// 	 buf[len++] = 'c';
// 	 buf[len++] = '-';
// 	 for (shift = 28; shift >= 0; shift -= 4)
// 	   buf[len++] = TOHEX (script_tag >> shift);
// 	 *language = hb_language_from_string ((char *) buf, len);
// 	 free (buf);
// 	   }
// 	 }
//    }
//  }
