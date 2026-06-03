package quality

// Upgrade classifies the result of comparing an incoming release against an
// already-tracked one for the same identity (series/episode or movie/year).
type Upgrade int

const (
	UpgradeNo           Upgrade = iota // not better than what's tracked; reject as duplicate
	UpgradeQuality                     // strictly better quality
	UpgradeProperRepack                // same-or-better quality marked PROPER/REPACK
)

// Decide reports whether an incoming release should be downloaded over the
// already-tracked one. The PROPER/REPACK branch is blocked when the stored
// record is itself a REPACK at no-worse quality — without that guard the
// same REPACK torrent reappearing in a feed would be accepted on every run.
func Decide(incoming, stored Quality, incomingIsProperRepack, storedIsRepack bool) Upgrade {
	if incoming.Better(stored) {
		return UpgradeQuality
	}
	if incomingIsProperRepack && !storedIsRepack && !stored.Better(incoming) {
		return UpgradeProperRepack
	}
	return UpgradeNo
}
