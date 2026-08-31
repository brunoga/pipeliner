package match

// Probe diagnostics answer the operational question "why doesn't title X match
// list Y?" without a log dive. The classic failure it surfaces is a
// normalization mismatch: a favorite named "Star Trek: Strange New Worlds"
// never matching a scene release "Star Trek Strange New Worlds" because the
// colon survived an older Normalize. Probe normalizes both sides the same way
// the filters do, reports the verdict, and ranks the non-matching candidates by
// edit distance so the near-misses (the ones a small title fix would rescue)
// sort to the top.

// CandidateResult is one list entry evaluated against a probe input.
type CandidateResult struct {
	// Norm is the candidate title after Normalize.
	Norm string `json:"norm"`
	// Year is the candidate's release year (0 = unknown).
	Year int `json:"year"`
	// Matched reports whether this candidate matched the input (title fuzzy
	// match AND year compatibility).
	Matched bool `json:"matched"`
	// TitleMatched reports whether the titles alone matched, ignoring year.
	// When Matched is false but TitleMatched is true, the year gate is the
	// reason — a distinct, actionable diagnosis from a title mismatch.
	TitleMatched bool `json:"title_matched"`
	// Distance is the Levenshtein distance between the input's normalized form
	// and this candidate's normalized form. 0 means the normalized strings are
	// identical (a match unless a glob pattern or year blocked it).
	Distance int `json:"distance"`
	// PunctuationOnly is true when the two normalized forms differ only in
	// characters that are neither letters nor digits — the signature of a
	// punctuation/normalization mismatch rather than a genuinely different
	// title. This is exactly what an un-folded colon or ampersand produces.
	PunctuationOnly bool `json:"punctuation_only"`
}

// ProbeResult is the full diagnosis of one input title against a candidate list.
type ProbeResult struct {
	// Input is the raw title as supplied.
	Input string `json:"input"`
	// InputNorm is Input after Normalize — the string actually compared.
	InputNorm string `json:"input_norm"`
	// Year is the input's supplied year (0 = unknown).
	Year int `json:"year"`
	// Matched is true if any candidate matched.
	Matched bool `json:"matched"`
	// MatchedBy is the normalized form of the first matching candidate, or ""
	// when nothing matched.
	MatchedBy string `json:"matched_by,omitempty"`
	// Candidates holds every evaluated candidate, sorted matches-first and then
	// by ascending Distance so the nearest near-miss leads.
	Candidates []CandidateResult `json:"candidates"`
}

// Probe evaluates input (with the given year) against a candidate list the same
// way the series/movies filters do, and returns a sorted diagnosis. year may be
// 0 for series-style title-only matching; when both the input and a candidate
// carry a year they must be compatible for Matched to hold.
func Probe(input string, year int, list []TitleEntry) ProbeResult {
	inputNorm := Normalize(input)
	res := ProbeResult{Input: input, InputNorm: inputNorm, Year: year}

	res.Candidates = make([]CandidateResult, 0, len(list))
	for _, c := range list {
		titleMatched := Fuzzy(inputNorm, c.Norm)
		matched := titleMatched && YearsCompatible(year, c.Year)
		cr := CandidateResult{
			Norm:            c.Norm,
			Year:            c.Year,
			Matched:         matched,
			TitleMatched:    titleMatched,
			Distance:        levenshtein(inputNorm, c.Norm),
			PunctuationOnly: differsOnlyByPunctuation(inputNorm, c.Norm),
		}
		if matched && !res.Matched {
			res.Matched = true
			res.MatchedBy = c.Norm
		}
		res.Candidates = append(res.Candidates, cr)
	}

	sortCandidates(res.Candidates)
	return res
}

// sortCandidates orders matches first, then by ascending edit distance, then
// alphabetically for a stable result. It avoids importing sort's closure
// overhead concerns by using a simple insertion sort — candidate lists are
// short (a user's favorites), so this is not a hot path.
func sortCandidates(cs []CandidateResult) {
	less := func(a, b CandidateResult) bool {
		if a.Matched != b.Matched {
			return a.Matched // matches first
		}
		if a.Distance != b.Distance {
			return a.Distance < b.Distance
		}
		return a.Norm < b.Norm
	}
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && less(cs[j], cs[j-1]); j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}

// differsOnlyByPunctuation reports whether a and b become equal once every rune
// that is neither a letter nor a digit is removed. Both inputs are already
// lowercased normalized forms, so this isolates "same words, different
// separators" — the fingerprint of a normalization gap.
func differsOnlyByPunctuation(a, b string) bool {
	if a == b {
		return false // identical, not a near-miss
	}
	return stripToAlnum(a) == stripToAlnum(b)
}

func stripToAlnum(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if isAlnum(r) {
			out = append(out, r)
		}
	}
	return string(out)
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// levenshtein returns the edit distance between a and b using the standard
// two-row dynamic-programming algorithm. It operates on runes so multibyte
// titles are measured correctly.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
