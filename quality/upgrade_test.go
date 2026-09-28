package quality

import "testing"

func q(s string) Quality { return Parse(s) }

func TestDecideStrictUpgrade(t *testing.T) {
	if got := Decide(q("Movie 1080p"), q("Movie 720p"), false, false); got != UpgradeQuality {
		t.Errorf("1080p over 720p: got %v, want UpgradeQuality", got)
	}
}

func TestDecideNoUpgrade(t *testing.T) {
	if got := Decide(q("Movie 720p"), q("Movie 1080p"), false, false); got != UpgradeNo {
		t.Errorf("720p over 1080p: got %v, want UpgradeNo", got)
	}
}

func TestDecideProperRepackAtSameQuality(t *testing.T) {
	got := Decide(q("Movie 1080p REPACK"), q("Movie 1080p"), true, false)
	if got != UpgradeProperRepack {
		t.Errorf("repack over non-repack 1080p: got %v, want UpgradeProperRepack", got)
	}
}

func TestDecideRepackOverRepackBlocked(t *testing.T) {
	// Regression: a REPACK reappearing in a feed must not re-accept against
	// an already-stored REPACK at the same quality.
	got := Decide(q("Movie 1080p REPACK"), q("Movie 1080p"), true, true)
	if got != UpgradeNo {
		t.Errorf("repack over stored-repack: got %v, want UpgradeNo (loop guard)", got)
	}
}

func TestDecideRepackDowngradeBlocked(t *testing.T) {
	// A 720p REPACK should not replace a stored 1080p just because it's a REPACK.
	got := Decide(q("Movie 720p REPACK"), q("Movie 1080p"), true, false)
	if got != UpgradeNo {
		t.Errorf("repack downgrade: got %v, want UpgradeNo", got)
	}
}
