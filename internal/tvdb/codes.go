package tvdb

import "time"

// LanguageName maps ISO 639-2 three-letter codes returned by the TVDB API to
// English display names. Falls back to the original code when not found.
func LanguageName(code string) string {
	if name, ok := iso639[code]; ok {
		return name
	}
	return code
}

var iso639 = map[string]string{
	"ara": "Arabic",
	"bul": "Bulgarian",
	"ces": "Czech",
	"chi": "Chinese",
	"zho": "Chinese",
	"hrv": "Croatian",
	"dan": "Danish",
	"nld": "Dutch",
	"eng": "English",
	"fin": "Finnish",
	"fra": "French",
	"deu": "German",
	"ger": "German",
	"ell": "Greek",
	"heb": "Hebrew",
	"hin": "Hindi",
	"hun": "Hungarian",
	"ind": "Indonesian",
	"ita": "Italian",
	"jpn": "Japanese",
	"kor": "Korean",
	"msa": "Malay",
	"nor": "Norwegian",
	"pol": "Polish",
	"por": "Portuguese",
	"ron": "Romanian",
	"rum": "Romanian",
	"rus": "Russian",
	"slk": "Slovak",
	"slo": "Slovak",
	"spa": "Spanish",
	"swe": "Swedish",
	"tha": "Thai",
	"tur": "Turkish",
	"ukr": "Ukrainian",
	"vie": "Vietnamese",
}

// CountryName maps ISO 3166-1 alpha-3 lowercase codes returned by the TVDB API
// to English display names. Covers the most common TV-producing countries;
// falls back to the original code for anything not in the table.
func CountryName(code string) string {
	if name, ok := iso3166[code]; ok {
		return name
	}
	return code
}

var iso3166 = map[string]string{
	"arg": "Argentina",
	"aus": "Australia",
	"aut": "Austria",
	"bel": "Belgium",
	"bra": "Brazil",
	"can": "Canada",
	"chl": "Chile",
	"chn": "China",
	"col": "Colombia",
	"cze": "Czech Republic",
	"dnk": "Denmark",
	"fin": "Finland",
	"fra": "France",
	"deu": "Germany",
	"hkg": "Hong Kong",
	"hun": "Hungary",
	"ind": "India",
	"idn": "Indonesia",
	"irl": "Ireland",
	"isr": "Israel",
	"ita": "Italy",
	"jpn": "Japan",
	"kor": "South Korea",
	"mex": "Mexico",
	"nld": "Netherlands",
	"nzl": "New Zealand",
	"nor": "Norway",
	"pol": "Poland",
	"prt": "Portugal",
	"rus": "Russia",
	"zaf": "South Africa",
	"esp": "Spain",
	"swe": "Sweden",
	"che": "Switzerland",
	"twn": "Taiwan",
	"tha": "Thailand",
	"tur": "Turkey",
	"gbr": "United Kingdom",
	"usa": "United States",
}

// ParseDate parses a date string returned by the TVDB API. The format varies:
// ISO-8601 with time ("2008-01-20T05:00:00.000Z") or plain date ("2008-01-20").
// Returns zero time on failure or empty input.
func ParseDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
