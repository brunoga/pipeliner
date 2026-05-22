package dag

import (
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/expr"
)

// NarrowCertain computes the field names that become certainly available on
// the accepting branch of a condition expression.
//
// It combines two mechanisms:
//  1. Syntactic narrowing — fields referenced in comparisons that prove they
//     are non-zero/non-empty when the expression is true (delegated to
//     expr.NarrowedCertain).
//  2. Semantic group promotions — well-known sentinel conditions that imply
//     a larger set of fields are guaranteed (e.g. enriched==true implies all
//     metainfo fields were written).
//
// The returned slice contains field names not already in certainFields.
func NarrowCertain(exprStr string, certainFields, reachableFields []string) []string {
	if exprStr == "" {
		return nil
	}
	e, err := expr.Compile(exprStr)
	if err != nil {
		return nil
	}

	certain := make(map[string]bool, len(certainFields))
	for _, f := range certainFields {
		certain[f] = true
	}
	reachable := make(map[string]bool, len(reachableFields))
	for _, f := range reachableFields {
		reachable[f] = true
	}

	// Collect candidates: syntactic narrowings + semantic group promotions.
	promoted := make(map[string]bool)

	// 1. Syntactic: fields directly referenced in comparisons.
	for _, f := range e.NarrowedCertain() {
		if reachable[f] && !certain[f] {
			promoted[f] = true
		}
	}

	// 2. Semantic group promotions.
	for _, g := range semanticGroups {
		if g.triggered(e.FieldRefs()) {
			for _, f := range g.promotes {
				if reachable[f] && !certain[f] {
					promoted[f] = true
				}
			}
		}
	}

	if len(promoted) == 0 {
		return nil
	}
	return sortedKeys(promoted)
}

// RejectAbsencePromoted returns field names promoted to certain by a REJECT
// rule whose expression uses absence-check operators (== "", == 0).
//
// "reject: field == ''" means only entries where the field is SET pass through,
// so the field becomes certain downstream — the mirror of NarrowCertain for
// an accept rule with a presence op.
func RejectAbsencePromoted(exprStr string, certainFields, reachableFields []string) []string {
	if exprStr == "" {
		return nil
	}
	e, err := expr.Compile(exprStr)
	if err != nil {
		return nil
	}
	certain := make(map[string]bool, len(certainFields))
	for _, f := range certainFields {
		certain[f] = true
	}
	reachable := make(map[string]bool, len(reachableFields))
	for _, f := range reachableFields {
		reachable[f] = true
	}
	promoted := make(map[string]bool)
	for _, f := range e.AbsencePromotedFields() {
		if reachable[f] && !certain[f] {
			promoted[f] = true
		}
	}
	if len(promoted) == 0 {
		return nil
	}
	return sortedKeys(promoted)
}

// RejectPresenceRemoved returns field names that should be removed from the
// reachable set by a REJECT rule using presence-check operators (!= "", > 0).
//
// "reject: field != ''" means only entries where the field is ABSENT pass,
// so the field should be removed from downstream field availability.
func RejectPresenceRemoved(exprStr string, reachableFields []string) []string {
	if exprStr == "" {
		return nil
	}
	e, err := expr.Compile(exprStr)
	if err != nil {
		return nil
	}
	reachable := make(map[string]bool, len(reachableFields))
	for _, f := range reachableFields {
		reachable[f] = true
	}
	var removed []string
	for _, f := range e.PresenceRemovedFields() {
		if reachable[f] {
			removed = append(removed, f)
		}
	}
	return removed
}

// AcceptAbsenceRemoved returns field names that should be removed from the
// reachable set on a route port's output branch when the port's accept
// expression uses absence-check operators (== "", == 0).
//
// Only valid for route port conditions (not condition plugin accept rules):
// route ports receive only matched entries so the absence is guaranteed,
// while condition lets unmatched entries pass through unchanged.
//
// AND: all absence-checked fields removed.
// OR:  only fields absent in every branch (intersection).
func AcceptAbsenceRemoved(exprStr string, reachableFields []string) []string {
	if exprStr == "" {
		return nil
	}
	e, err := expr.Compile(exprStr)
	if err != nil {
		return nil
	}
	reachable := make(map[string]bool, len(reachableFields))
	for _, f := range reachableFields {
		reachable[f] = true
	}
	var removed []string
	for _, f := range e.AbsenceRemovedFields() {
		if reachable[f] {
			removed = append(removed, f)
		}
	}
	return removed
}

// semanticGroup describes a set of fields that become certain when a specific
// sentinel field is referenced in the expression (conservatively: if the field
// name appears at all in the expression it's likely being tested).
type semanticGroup struct {
	// sentinel is the field whose presence in the expression triggers this group.
	sentinel string
	// promotes is the set of fields that become certain when sentinel is in an
	// accepting condition and is a reachable field.
	promotes []string
}

// triggered returns true when the sentinel field appears in the expression's
// field references.
func (g *semanticGroup) triggered(refs []string) bool {
	for _, r := range refs {
		if r == g.sentinel {
			return true
		}
	}
	return false
}

// semanticGroups is the table of well-known condition → field-set implications.
// Each entry is intentionally conservative: only add an implication when the
// underlying plugin contracts guarantee it.
var semanticGroups = []semanticGroup{
	{
		// enriched == true → a metainfo provider ran and set all its fields.
		sentinel: entry.FieldEnriched,
		promotes: []string{
			entry.FieldVideoYear,
			entry.FieldVideoLanguage,
			entry.FieldVideoOriginalTitle,
			entry.FieldVideoCountry,
			entry.FieldVideoGenres,
			entry.FieldVideoRating,
			entry.FieldVideoPopularity,
			entry.FieldVideoVotes,
			entry.FieldVideoImdbID,
			entry.FieldVideoQuality,
			entry.FieldVideoResolution,
		},
	},
	{
		// series_episode_id being tested implies season and episode are set —
		// the metainfo_series plugin always sets all three together.
		sentinel: entry.FieldSeriesEpisodeID,
		promotes: []string{
			entry.FieldSeriesSeason,
			entry.FieldSeriesEpisode,
		},
	},
	{
		// torrent_link_type being tested implies the torrent was sourced from
		// a plugin that sets the full torrent field suite.
		sentinel: entry.FieldTorrentLinkType,
		promotes: []string{
			entry.FieldTorrentSeeds,
			entry.FieldTorrentLeechers,
			entry.FieldTorrentFileSize,
			entry.FieldTorrentInfoHash,
		},
	},
}
