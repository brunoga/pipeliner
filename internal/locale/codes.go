// Package locale maps ISO language and country codes to English display names.
// Used by metainfo plugins (TMDb, Trakt) that receive 2-letter codes in API
// responses but need to surface human-readable strings on Entry.Fields.
package locale

// LanguageName639_1 maps ISO 639-1 two-letter language codes (e.g. "en", "ja")
// to English display names. Falls back to the original code when not found.
func LanguageName639_1(code string) string {
	if n, ok := iso639_1[code]; ok {
		return n
	}
	return code
}

var iso639_1 = map[string]string{
	"af": "Afrikaans", "ar": "Arabic", "bg": "Bulgarian", "bn": "Bengali",
	"cs": "Czech", "da": "Danish", "de": "German", "el": "Greek",
	"en": "English", "es": "Spanish", "et": "Estonian", "fa": "Persian",
	"fi": "Finnish", "fr": "French", "gu": "Gujarati", "he": "Hebrew",
	"hi": "Hindi", "hr": "Croatian", "hu": "Hungarian", "id": "Indonesian",
	"it": "Italian", "ja": "Japanese", "ko": "Korean", "lt": "Lithuanian",
	"lv": "Latvian", "mk": "Macedonian", "ml": "Malayalam", "mr": "Marathi",
	"ms": "Malay", "nl": "Dutch", "no": "Norwegian", "pa": "Punjabi",
	"pl": "Polish", "pt": "Portuguese", "ro": "Romanian", "ru": "Russian",
	"sk": "Slovak", "sl": "Slovenian", "sq": "Albanian", "sr": "Serbian",
	"sv": "Swedish", "sw": "Swahili", "ta": "Tamil", "te": "Telugu",
	"th": "Thai", "tl": "Filipino", "tr": "Turkish", "uk": "Ukrainian",
	"ur": "Urdu", "vi": "Vietnamese", "zh": "Chinese",
}

// CountryName3166_1Alpha2 maps ISO 3166-1 alpha-2 country codes (e.g. "us",
// "jp") to English display names. Covers the most common TV/film producing
// countries; falls back to the original (uppercased) code when not found.
func CountryName3166_1Alpha2(code string) string {
	if n, ok := iso3166_1_alpha2[code]; ok {
		return n
	}
	return code
}

var iso3166_1_alpha2 = map[string]string{
	"ar": "Argentina", "au": "Australia", "at": "Austria", "be": "Belgium",
	"br": "Brazil", "ca": "Canada", "cl": "Chile", "cn": "China",
	"co": "Colombia", "cz": "Czech Republic", "dk": "Denmark", "fi": "Finland",
	"fr": "France", "de": "Germany", "hk": "Hong Kong", "hu": "Hungary",
	"in": "India", "id": "Indonesia", "ie": "Ireland", "il": "Israel",
	"it": "Italy", "jp": "Japan", "kr": "South Korea", "mx": "Mexico",
	"nl": "Netherlands", "nz": "New Zealand", "no": "Norway", "pl": "Poland",
	"pt": "Portugal", "ru": "Russia", "za": "South Africa", "es": "Spain",
	"se": "Sweden", "ch": "Switzerland", "tw": "Taiwan", "th": "Thailand",
	"tr": "Turkey", "gb": "United Kingdom", "us": "United States",
}
