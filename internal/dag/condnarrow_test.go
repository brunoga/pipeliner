package dag

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
)

func TestNarrowCertain(t *testing.T) {
	certain := []string{"source", "title", "torrent_link_type"}
	reachable := []string{
		"source", "title", "torrent_link_type",
		"torrent_seeds", "torrent_leechers", "torrent_file_size", "torrent_info_hash",
		"enriched", "video_year", "video_genres", "series_season", "series_episode",
		"series_episode_id",
	}

	t.Run("simple comparison promotes field", func(t *testing.T) {
		got := NarrowCertain("torrent_seeds >= 10", certain, reachable)
		if !containsStr(got, "torrent_seeds") {
			t.Errorf("torrent_seeds >= 10: want torrent_seeds promoted, got %v", got)
		}
	})

	t.Run("already-certain field not re-promoted", func(t *testing.T) {
		got := NarrowCertain("torrent_link_type == \"magnet\"", certain, reachable)
		// torrent_link_type is already certain, should not appear in promoted list
		if containsStr(got, "torrent_link_type") {
			t.Errorf("already-certain field should not be in promoted list, got %v", got)
		}
	})

	t.Run("enriched sentinel promotes video fields", func(t *testing.T) {
		got := NarrowCertain("enriched == true", certain, reachable)
		if !containsStr(got, entry.FieldVideoYear) {
			t.Errorf("enriched==true: want video_year promoted, got %v", got)
		}
	})

	t.Run("series_episode_id sentinel promotes season/episode", func(t *testing.T) {
		got := NarrowCertain("series_episode_id != \"\"", certain, reachable)
		if !containsStr(got, entry.FieldSeriesSeason) {
			t.Errorf("series_episode_id: want series_season promoted, got %v", got)
		}
		if !containsStr(got, entry.FieldSeriesEpisode) {
			t.Errorf("series_episode_id: want series_episode promoted, got %v", got)
		}
	})

	t.Run("torrent_link_type sentinel promotes torrent fields", func(t *testing.T) {
		// torrent_link_type is already certain but the sentinel still fires for
		// promoting other torrent fields that are only reachable.
		got := NarrowCertain("torrent_link_type == \"magnet\"", certain, reachable)
		if !containsStr(got, entry.FieldTorrentSeeds) {
			t.Errorf("torrent_link_type: want torrent_seeds promoted, got %v", got)
		}
	})

	t.Run("empty expression returns nil", func(t *testing.T) {
		if NarrowCertain("", certain, reachable) != nil {
			t.Error("empty expression should return nil")
		}
	})

	t.Run("invalid expression returns nil", func(t *testing.T) {
		if NarrowCertain("(((broken", certain, reachable) != nil {
			t.Error("invalid expression should return nil, not error")
		}
	})

	t.Run("field not reachable is not promoted", func(t *testing.T) {
		got := NarrowCertain("movie_tagline != \"\"", certain, reachable)
		if containsStr(got, "movie_tagline") {
			t.Errorf("non-reachable field should not be promoted, got %v", got)
		}
	})
}

func TestRejectAbsencePromoted(t *testing.T) {
	certain  := []string{"source", "title"}
	reachable := []string{
		"source", "title", "torrent_files", "description",
	}

	t.Run("reject == '' promotes field", func(t *testing.T) {
		got := RejectAbsencePromoted(`torrent_files == ""`, certain, reachable)
		if !containsStr(got, "torrent_files") {
			t.Errorf(`reject torrent_files == "": want torrent_files promoted, got %v`, got)
		}
	})

	t.Run("reject OR absence promotes all fields", func(t *testing.T) {
		got := RejectAbsencePromoted(`torrent_files == "" or description == ""`, certain, reachable)
		if !containsStr(got, "torrent_files") || !containsStr(got, "description") {
			t.Errorf("OR reject: want both promoted, got %v", got)
		}
	})

	t.Run("reject AND absence promotes nothing (ambiguous)", func(t *testing.T) {
		got := RejectAbsencePromoted(`torrent_files == "" and description == ""`, certain, reachable)
		if len(got) != 0 {
			t.Errorf("AND reject: want nothing promoted, got %v", got)
		}
	})

	t.Run("reject presence op returns nothing", func(t *testing.T) {
		got := RejectAbsencePromoted(`torrent_files != ""`, certain, reachable)
		if len(got) != 0 {
			t.Errorf("presence op reject: want nothing from RejectAbsencePromoted, got %v", got)
		}
	})

	t.Run("already-certain field not re-promoted", func(t *testing.T) {
		got := RejectAbsencePromoted(`source == ""`, certain, reachable)
		if containsStr(got, "source") {
			t.Errorf("already-certain field should not appear in promoted list")
		}
	})
}

func TestRejectPresenceRemoved(t *testing.T) {
	reachable := []string{"source", "title", "description", "torrent_files"}

	t.Run("reject != '' removes field", func(t *testing.T) {
		got := RejectPresenceRemoved(`description != ""`, reachable)
		if !containsStr(got, "description") {
			t.Errorf(`reject description != "": want description removed, got %v`, got)
		}
	})

	t.Run("reject OR presence removes all", func(t *testing.T) {
		got := RejectPresenceRemoved(`description != "" or torrent_files != ""`, reachable)
		if !containsStr(got, "description") || !containsStr(got, "torrent_files") {
			t.Errorf("OR reject: want both removed, got %v", got)
		}
	})

	t.Run("reject AND presence removes nothing (ambiguous)", func(t *testing.T) {
		got := RejectPresenceRemoved(`description != "" and torrent_files != ""`, reachable)
		if len(got) != 0 {
			t.Errorf("AND reject: want nothing removed, got %v", got)
		}
	})

	t.Run("reject absence op returns nothing", func(t *testing.T) {
		got := RejectPresenceRemoved(`torrent_files == ""`, reachable)
		if len(got) != 0 {
			t.Errorf("absence op: RejectPresenceRemoved should return nothing, got %v", got)
		}
	})
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
