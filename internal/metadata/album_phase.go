package metadata

// Measured on real albums: the right edition scores 0.95-1, another edition with the same track count 0.77.
const editionMatchRatio = 0.9

// The release that holds the album as presented: same track count, titles in the same order.
func matchRelease(tracks []string, candidates []Tracklist) (Tracklist, bool) {
	var best Tracklist
	bestRatio := 0.0
	for _, tl := range candidates {
		ratio, ok := editionRatio(tracks, tl)
		if !ok || ratio < editionMatchRatio {
			continue
		}
		if bestRatio == 0 || ratio > bestRatio || (ratio == bestRatio && preferRelease(tl, best)) {
			best, bestRatio = tl, ratio
		}
	}
	return best, bestRatio > 0
}

// The share of known titles that match the release's title at the same position.
func editionRatio(tracks []string, tl Tracklist) (float64, bool) {
	if len(tl.Tracks) != len(tracks) {
		return 0, false
	}
	known, matched := 0, 0
	for i, title := range tracks {
		if title == "" {
			continue
		}
		known++
		a, _ := cleanTitle(title)
		b, _ := cleanTitle(tl.Tracks[i].Title)
		if Similarity(a, b) >= trackMatchThreshold {
			matched++
		}
	}
	if known == 0 {
		return 0, false
	}
	return float64(matched) / float64(known), true
}

// Official first, then the earliest date (any date before none), then the lowest ID so the choice is stable.
func preferRelease(a, b Tracklist) bool {
	if (a.Status == "Official") != (b.Status == "Official") {
		return a.Status == "Official"
	}
	if a.Date != b.Date {
		if a.Date == "" || b.Date == "" {
			return a.Date != ""
		}
		return a.Date < b.Date
	}
	return a.ID < b.ID
}
