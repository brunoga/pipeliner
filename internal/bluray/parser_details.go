package bluray

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"
)

var (
	rePageTitle      = regexp.MustCompile(`(?is)<title>([^<]*)</title>`)
	rePageEditionTag = regexp.MustCompile(`^(.*?)\s*\(([^)]+)\)\s*$`)
	reReleaseDateAlt = regexp.MustCompile(`<a class="grey noline"[^>]*title="[^"]*Release Date ([A-Z][a-z]+ \d{1,2}, \d{4})"`)
	reH1Flag         = regexp.MustCompile(`<h1>[^<]*</h1>\s*</a>\s*<img[^>]+title="([^"]+)"`)
	reBitrateParen   = regexp.MustCompile(`\s*\([\d.]+\s*Mbps\)\s*$`)
)

// ParseDetail extracts a Release from a /movies/<slug>/<id>/ page.
// It is an alias for ParseReleaseDetails.
func ParseDetail(rawHTML []byte, pageURL string) (*Release, error) {
	return ParseReleaseDetails(rawHTML, pageURL)
}

// ParseReleaseDetails parses a Blu-ray release detail page (e.g., /movies/Dredd-3D-Blu-ray/56292/).
func ParseReleaseDetails(rawHTML []byte, pageURL string) (*ReleaseDetails, error) {
	htmlStr := EnsureUTF8(rawHTML)
	doc, err := xhtml.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return nil, fmt.Errorf("failed to parse release HTML: %w", err)
	}

	slug, id := SlugAndIDFromURL(pageURL)
	if id == "" {
		id = extractIDFromURL(pageURL)
	}

	details := &ReleaseDetails{
		ID:     id,
		URL:    pageURL,
		Format: FormatFromSlug(slug),
	}

	// Canonical URL / og:url fallback
	ogURL := findFirstNode(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "meta" && nodeAttr(n, "property") == "og:url"
	})
	if ogURL != nil {
		if content := nodeAttr(ogURL, "content"); content != "" {
			if details.URL == "" {
				details.URL = content
			}
			if details.ID == "" {
				details.ID = extractIDFromURL(content)
			}
			if slug == "" {
				if ogSlug, _ := SlugAndIDFromURL(content); ogSlug != "" {
					details.Format = FormatFromSlug(ogSlug)
				}
			}
		}
	}

	// Extract <title> and parenthesised Edition tag if present
	if m := rePageTitle.FindStringSubmatch(htmlStr); len(m) == 2 {
		raw := strings.TrimSpace(html.UnescapeString(m[1]))
		if mm := rePageEditionTag.FindStringSubmatch(raw); len(mm) == 3 {
			details.Title = strings.TrimSpace(mm[1])
			details.Edition = strings.TrimSpace(mm[2])
		} else {
			details.Title = raw
		}
	}

	// 1. Header: <h1> inside <a class="black noline" data-globalproductid="..." href="https://www.blu-ray.com/Dredd/128351/">
	h1 := findFirstNode(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "h1"
	})
	if h1 != nil {
		if h1Text := nodeText(h1); h1Text != "" {
			details.Title = h1Text
		}
		if h1.Parent != nil && h1.Parent.Data == "a" {
			details.ParentMovieURL = decodeLinkWrapper(nodeAttr(h1.Parent, "href"))
			details.ParentMovieID = nodeAttr(h1.Parent, "data-globalparentid")
			if details.ParentMovieID == "" {
				details.ParentMovieID = extractIDFromURL(details.ParentMovieURL)
			}
		}
		// Country flag image next to h1's parent
		if h1.Parent != nil && h1.Parent.Parent != nil {
			flagImg := findFirstNode(h1.Parent.Parent, func(n *xhtml.Node) bool {
				return n.Type == xhtml.ElementNode && n.Data == "img" && strings.Contains(nodeAttr(n, "src"), "/flags/")
			})
			if flagImg != nil {
				if fm := reFlagCountry.FindStringSubmatch(nodeAttr(flagImg, "src")); len(fm) > 1 {
					details.CountryCode = Country(strings.ToLower(fm[1]))
				}
				if titleAttr := cleanText(nodeAttr(flagImg, "title")); titleAttr != "" {
					details.Country = titleAttr
				}
			}
		}
	}
	if details.Country == "" {
		if m := reH1Flag.FindStringSubmatch(htmlStr); len(m) == 2 {
			details.Country = html.UnescapeString(m[1])
		}
	}
	if details.Country == "" && details.CountryCode != "" {
		details.Country = string(details.CountryCode)
	}

	// 2. Subtitle (<span class="subheadingtitle">) and metadata line (<span class="subheading grey">)
	subTitleSpan := findFirstNode(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "span" && hasClass(n, "subheadingtitle")
	})
	if subTitleSpan != nil {
		details.Subtitle = nodeText(subTitleSpan)
		if details.Edition == "" && details.Subtitle != "" {
			details.Edition = details.Subtitle
		}
	}

	metaSpan := findFirstNode(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "span" && hasClass(n, "subheading") && hasClass(n, "grey")
	})
	if metaSpan != nil {
		var tmp ReleaseSummary
		parseReleaseMetaLine(nodeText(metaSpan), &tmp)
		details.Studio = tmp.Studio
		details.Year = tmp.Year
		details.RuntimeMin = tmp.RuntimeMins
		details.Rated = tmp.Rated
		if iso, ok := NormalizeDateISO(tmp.ReleaseDate); ok {
			details.ReleaseDate = iso
		} else {
			details.ReleaseDate = tmp.ReleaseDate
		}
	}
	if details.ReleaseDate == "" {
		if m := reReleaseDateAlt.FindStringSubmatch(htmlStr); len(m) == 2 {
			if iso, ok := NormalizeDateISO(m[1]); ok {
				details.ReleaseDate = iso
			}
		}
	}

	// 3. Cover art URLs
	frontOverlay := findFirstNode(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "img" && nodeAttr(n, "id") == "frontimage_overlay"
	})
	if frontOverlay != nil {
		details.CoverURL = nodeAttr(frontOverlay, "src")
	}
	if m := reFrontImage.FindStringSubmatch(htmlStr); len(m) > 1 {
		details.LargeFrontURL = m[1]
	}
	if m := reBackImage.FindStringSubmatch(htmlStr); len(m) > 1 {
		details.LargeBackURL = m[1]
	}
	if m := reSlipImage.FindStringSubmatch(htmlStr); len(m) > 1 {
		details.SlipcoverURL = m[1]
	}

	// 4. Genres from <div class="genreappeal">
	genreDivs := findNodes(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "div" && hasClass(n, "genreappeal")
	})
	for _, gd := range genreDivs {
		if g := nodeText(gd); g != "" {
			details.Genres = append(details.Genres, g)
		}
	}

	// 5. Specs column: <td width="228px"> containing <span class="subheading">Video</span>, Audio, Subtitles, Discs, etc.
	specsTD := findFirstNode(doc, func(n *xhtml.Node) bool {
		if n.Type == xhtml.ElementNode && n.Data == "td" && nodeAttr(n, "width") == "228px" {
			return findFirstNode(n, func(c *xhtml.Node) bool {
				return c.Type == xhtml.ElementNode && c.Data == "span" && hasClass(c, "subheading") && nodeText(c) == "Video"
			}) != nil
		}
		return false
	})
	if specsTD != nil {
		parseSpecsColumn(specsTD, details)
	}

	// Sync top-level Codec, Resolution, AspectRatio, and Format from Video/Discs
	if details.Video.Codec != "" {
		details.Codec = strings.TrimSpace(reBitrateParen.ReplaceAllString(details.Video.Codec, ""))
	}
	if details.Video.Resolution != "" {
		details.Resolution = details.Video.Resolution
	}
	if details.Video.AspectRatio != "" {
		details.AspectRatio = details.Video.AspectRatio
	}
	if details.Video.Is3D || details.Discs.Is3D {
		details.Format = FormatBD3D
	} else if details.Format == FormatBD && (details.Video.Is4K || details.Discs.Is4K) {
		details.Format = FormatUHD
	}

	// 6. Right column: Price & Ratings (<td width="266px">)
	rightTD := findFirstNode(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "td" && nodeAttr(n, "width") == "266px"
	})
	if rightTD != nil {
		parsePriceAndRatings(rightTD, details)
	}

	// 7. Movie info section (<div id="movie_info">): synopsis, director, writers, starring
	movieInfoDiv := findFirstNode(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "div" && nodeAttr(n, "id") == "movie_info"
	})
	if movieInfoDiv != nil {
		parseMovieInfoSection(movieInfoDiv, details)
	}

	if details.Title == "" && details.Studio == "" {
		return nil, fmt.Errorf("bluray: parse detail: page does not look like a release page")
	}

	return details, nil
}

func parseSpecsColumn(td *xhtml.Node, details *ReleaseDetails) {
	sections := make(map[string][]string)
	var currentSection string
	var lineBuf strings.Builder

	flushLine := func() {
		line := cleanText(lineBuf.String())
		lineBuf.Reset()
		if line != "" && currentSection != "" {
			line = strings.TrimSuffix(line, "(less)")
			line = strings.TrimSuffix(line, "(more)")
			line = strings.TrimSpace(line)
			if line != "" {
				sections[currentSection] = append(sections[currentSection], line)
			}
		}
	}

	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			id := nodeAttr(n, "id")
			if id == "shortaudio" && findFirstNode(td, func(x *xhtml.Node) bool { return nodeAttr(x, "id") == "longaudio" }) != nil {
				return
			}
			if id == "shortsubs" && findFirstNode(td, func(x *xhtml.Node) bool { return nodeAttr(x, "id") == "longsubs" }) != nil {
				return
			}
			if n.Data == "span" && hasClass(n, "subheading") {
				flushLine()
				currentSection = nodeText(n)
				return
			}
			if n.Data == "br" || n.Data == "div" {
				flushLine()
			}
		}
		if n.Type == xhtml.TextNode {
			lineBuf.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == xhtml.ElementNode && n.Data == "div" {
			flushLine()
		}
	}
	walk(td)
	flushLine()

	// Video
	if vLines, ok := sections["Video"]; ok {
		details.Video.RawLines = vLines
		for _, l := range vLines {
			lower := strings.ToLower(l)
			switch {
			case strings.HasPrefix(lower, "codec:"):
				details.Video.Codec = strings.TrimSpace(l[len("Codec:"):])
			case strings.HasPrefix(lower, "resolution:"):
				details.Video.Resolution = strings.TrimSpace(l[len("Resolution:"):])
			case strings.HasPrefix(lower, "aspect ratio:"):
				details.Video.AspectRatio = strings.TrimSpace(l[len("Aspect ratio:"):])
			case strings.HasPrefix(lower, "original aspect ratio:"):
				details.Video.OriginalAspectRatio = strings.TrimSpace(l[len("Original aspect ratio:"):])
			case strings.HasPrefix(lower, "hdr:"):
				details.Video.HDR = strings.TrimSpace(l[len("HDR:"):])
			}
		}
		details.Video.Is3D = is3DIndicator(details.Video.Codec, details.Video.Resolution, details.Title, details.Subtitle)
		details.Video.Is4K = is4KIndicator(details.Video.Codec, details.Video.Resolution, details.Title, details.Subtitle)
	}

	// Audio
	if aLines, ok := sections["Audio"]; ok {
		for _, l := range aLines {
			if strings.EqualFold(l, "None") {
				continue
			}
			track := AudioTrack{Raw: l}
			if idx := strings.Index(l, ":"); idx > 0 {
				track.Language = strings.TrimSpace(l[:idx])
				track.Format = strings.TrimSpace(l[idx+1:])
			} else {
				track.Format = l
			}
			details.Audio = append(details.Audio, track)
		}
	}

	// Subtitles
	if sLines, ok := sections["Subtitles"]; ok {
		for _, l := range sLines {
			if strings.EqualFold(l, "None") {
				continue
			}
			for _, part := range strings.Split(l, ",") {
				sub := cleanText(part)
				if sub != "" {
					details.Subtitles = append(details.Subtitles, sub)
				}
			}
		}
	}

	// Discs
	if dLines, ok := sections["Discs"]; ok {
		details.Discs.RawLines = dLines
		for _, l := range dLines {
			lower := strings.ToLower(l)
			if strings.Contains(lower, "3d") {
				details.Discs.Is3D = true
			}
			if strings.Contains(lower, "4k") || strings.Contains(lower, "ultra hd") {
				details.Discs.Is4K = true
			}
			if strings.Contains(lower, "single disc") {
				details.Discs.Count = 1
				details.Discs.Format = l
			} else if strings.Contains(lower, "-disc") || strings.Contains(lower, "discs") {
				details.Discs.Format = l
				fields := strings.Fields(l)
				if len(fields) > 0 {
					if n, err := strconv.Atoi(strings.TrimSuffix(fields[0], "-disc")); err == nil {
						details.Discs.Count = n
					}
				}
			}
		}
	}

	// Digital
	if digLines, ok := sections["Digital"]; ok {
		details.Digital = digLines
	}

	// Packaging
	if pkgLines, ok := sections["Packaging"]; ok {
		details.Packaging = pkgLines
	}

	// Playback
	if pbLines, ok := sections["Playback"]; ok {
		details.Playback = strings.Join(pbLines, "; ")
	}
}

func parsePriceAndRatings(td *xhtml.Node, details *ReleaseDetails) {
	strike := findFirstNode(td, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "strike"
	})
	if strike != nil {
		details.Price.ListPrice = nodeText(strike)
	}

	buyLinks := findNodes(td, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "a" && strings.Contains(nodeAttr(n, "href"), "link/click.php")
	})
	for _, a := range buyLinks {
		txt := nodeText(a)
		if strings.ContainsAny(txt, "$€£¥") && details.Price.CurrentPrice == "" {
			details.Price.CurrentPrice = txt
			details.Price.BuyURL = nodeAttr(a, "href")
			break
		}
	}

	scoreDiv := findFirstNode(td, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "div" && strings.HasPrefix(nodeAttr(n, "id"), "ratingscore_")
	})
	if scoreDiv != nil {
		details.Ratings.MovieScore, _ = strconv.ParseFloat(nodeText(scoreDiv), 64)
	}
	votesDiv := findFirstNode(td, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "div" && strings.HasPrefix(nodeAttr(n, "id"), "votes_")
	})
	if votesDiv != nil {
		details.Ratings.MovieVotes, _ = strconv.Atoi(nodeText(votesDiv))
	}

	imdbLink := findFirstNode(td, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "a" && nodeAttr(n, "id") == "imdb_icon"
	})
	if imdbLink != nil {
		details.Ratings.IMDbURL = nodeAttr(imdbLink, "href")
	}
	rtLink := findFirstNode(td, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "a" && nodeAttr(n, "id") == "rt_icon"
	})
	if rtLink != nil {
		details.Ratings.RTURL = nodeAttr(rtLink, "href")
	}

	reviewDiv := findFirstNode(td, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "div" && nodeAttr(n, "id") == "review_rating"
	})
	if reviewDiv != nil {
		scores := parseRatingTable(reviewDiv)
		details.Ratings.ReviewVideo = scores["video"]
		details.Ratings.Review3D = scores["3d"]
		details.Ratings.ReviewAudio = scores["audio"]
		details.Ratings.ReviewExtras = scores["extras"]
		details.Ratings.ReviewOverall = scores["overall"]
	}

	userDiv := findFirstNode(td, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "div" && nodeAttr(n, "id") == "user_rating"
	})
	if userDiv != nil {
		scores := parseRatingTable(userDiv)
		details.Ratings.UserVideo = scores["video"]
		details.Ratings.User3D = scores["3d"]
		details.Ratings.UserAudio = scores["audio"]
		details.Ratings.UserExtras = scores["extras"]
		details.Ratings.UserOverall = scores["overall"]
	}
}

func parseRatingTable(container *xhtml.Node) map[string]float64 {
	out := make(map[string]float64)
	rows := findNodes(container, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "tr"
	})
	for _, tr := range rows {
		tds := findNodes(tr, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "td"
		})
		if len(tds) >= 3 {
			label := strings.ToLower(nodeText(tds[0]))
			val, err := strconv.ParseFloat(nodeText(tds[2]), 64)
			if err == nil {
				out[label] = val
			}
		}
	}
	return out
}

func parseMovieInfoSection(div *xhtml.Node, details *ReleaseDetails) {
	var mode string
	var synCandidates []string

	for c := div.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xhtml.ElementNode && c.Data == "br" {
			mode = ""
			continue
		}
		if c.Type == xhtml.TextNode {
			txt := cleanText(c.Data)
			lower := strings.ToLower(txt)
			switch {
			case strings.HasPrefix(lower, "director:"), strings.HasPrefix(lower, "directors:"):
				mode = "director"
			case strings.HasPrefix(lower, "writer:"), strings.HasPrefix(lower, "writers:"):
				mode = "writers"
			case strings.HasPrefix(lower, "starring:"):
				mode = "starring"
			case strings.HasPrefix(lower, "producer:"), strings.HasPrefix(lower, "producers:"):
				mode = "producer"
			case mode == "" && len(txt) > 25 && !strings.HasPrefix(lower, "for more about"):
				synCandidates = append(synCandidates, txt)
			}
		}
		if c.Type == xhtml.ElementNode && c.Data == "a" {
			href := decodeLinkWrapper(nodeAttr(c, "href"))
			name := nodeText(c)
			if name == "" || strings.Contains(href, "#") {
				continue
			}
			person := PersonLink{
				Name: name,
				ID:   extractIDFromURL(href),
				URL:  href,
			}
			switch mode {
			case "director":
				if details.Director == "" {
					details.Director = name
				} else {
					details.Director += ", " + name
				}
			case "writers":
				details.Writers = append(details.Writers, person)
			case "starring":
				details.Starring = append(details.Starring, person)
			}
		}
	}
	if len(synCandidates) > 0 {
		details.Synopsis = synCandidates[0]
	}
}

// ParseMovieDetails parses a theatrical / master movie page (e.g., https://www.blu-ray.com/Dredd/128351/).
func ParseMovieDetails(rawHTML []byte, pageURL string) (*MovieDetails, error) {
	htmlStr := EnsureUTF8(rawHTML)
	doc, err := xhtml.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return nil, fmt.Errorf("failed to parse movie HTML: %w", err)
	}

	movie := &MovieDetails{
		ID:  extractIDFromURL(pageURL),
		URL: pageURL,
	}

	if movie.ID == "" {
		if m := reMenuAjaxParam.FindStringSubmatch(htmlStr); len(m) > 1 {
			movie.ID = m[1]
		}
	}

	h1 := findFirstNode(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "h1" && hasClass(n, "eurostile")
	})
	if h1 != nil {
		var titleBuf strings.Builder
		for c := h1.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xhtml.TextNode {
				titleBuf.WriteString(c.Data)
			} else if c.Type == xhtml.ElementNode && c.Data == "font" {
				if ym := reYearBare.FindStringSubmatch(nodeText(c)); len(ym) > 1 {
					movie.Year, _ = strconv.Atoi(ym[1])
				}
			}
		}
		movie.Title = cleanText(titleBuf.String())
		if movie.Title == "" {
			movie.Title = nodeText(h1)
		}
	} else {
		anyH1 := findFirstNode(doc, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "h1"
		})
		if anyH1 != nil {
			movie.Title = nodeText(anyH1)
		}
	}

	canonLink := findFirstNode(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "link" && nodeAttr(n, "rel") == "canonical"
	})
	if canonLink != nil {
		if href := nodeAttr(canonLink, "href"); href != "" {
			movie.URL = href
		}
	}

	if movie.Year == 0 {
		blackbox := findFirstNode(doc, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "div" && hasClass(n, "blackbox")
		})
		if blackbox != nil {
			if ym := reYearBare.FindStringSubmatch(nodeText(blackbox)); len(ym) > 1 {
				movie.Year, _ = strconv.Atoi(ym[1])
			}
		}
	}

	genreDivs := findNodes(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "div" && hasClass(n, "genreappeal")
	})
	for _, gd := range genreDivs {
		if g := nodeText(gd); g != "" {
			movie.Genres = append(movie.Genres, g)
		}
	}

	specMenus := findNodes(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "td" && hasClass(n, "specmenu")
	})
	for _, sm := range specMenus {
		if sm.Parent == nil {
			continue
		}
		si := findFirstNode(sm.Parent, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "td" && hasClass(n, "specitem")
		})
		if si == nil {
			continue
		}
		key := strings.ToLower(nodeText(sm))
		val := nodeText(si)
		switch key {
		case "studio":
			var studios []string
			for _, a := range findNodes(si, func(n *xhtml.Node) bool { return n.Type == xhtml.ElementNode && n.Data == "a" }) {
				if s := nodeText(a); s != "" {
					studios = append(studios, s)
				}
			}
			if len(studios) > 0 {
				movie.Studio = strings.Join(studios, ", ")
			} else {
				movie.Studio = val
			}
		case "country":
			movie.Country = val
		case "language":
			movie.Language = val
		case "runtime":
			if rm := reRuntimeMins.FindStringSubmatch(val); len(rm) > 1 {
				movie.RuntimeMins, _ = strconv.Atoi(rm[1])
			}
		case "rated":
			movie.Rated = val
		case "technical details":
			var lines []string
			var cur strings.Builder
			for c := si.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == xhtml.ElementNode && c.Data == "br" {
					if l := cleanText(cur.String()); l != "" {
						lines = append(lines, l)
					}
					cur.Reset()
				} else {
					cur.WriteString(nodeText(c))
				}
			}
			if l := cleanText(cur.String()); l != "" {
				lines = append(lines, l)
			}
			movie.TechnicalDetails = lines
			for _, l := range lines {
				lower := strings.ToLower(l)
				if strings.Contains(lower, "native 3d") {
					movie.IsNative3D = true
				}
				if strings.Contains(lower, "converted 3d") {
					movie.IsConverted3D = true
				}
			}
		}
	}

	longSyn := findFirstNode(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "div" && nodeAttr(n, "id") == "longsynopsis"
	})
	if longSyn != nil {
		movie.Synopsis = strings.TrimSpace(strings.TrimSuffix(nodeText(longSyn), "(less)"))
	} else {
		shortSyn := findFirstNode(doc, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "div" && nodeAttr(n, "id") == "shortsynopsis"
		})
		if shortSyn != nil {
			movie.Synopsis = strings.TrimSpace(strings.TrimSuffix(nodeText(shortSyn), "(more)"))
		}
	}

	overviewDiv := findFirstNode(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "div" && nodeAttr(n, "id") == "content_overview"
	})
	if overviewDiv != nil {
		crewCol := findFirstNode(overviewDiv, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "div" && strings.Contains(nodeAttr(n, "style"), "700px")
		})
		if crewCol != nil {
			var tmp ReleaseDetails
			parseMovieInfoSection(crewCol, &tmp)
			movie.Director = tmp.Director
			movie.Writers = tmp.Writers
			movie.Starring = tmp.Starring
		}
	}

	return movie, nil
}

// ParseMovieReleasesAll parses the HTML response from
// /products/menu_ajax.php?p=<MovieID>&c=20&action=showreleasesall
// which lists every edition across all countries grouped by format (<h3>3D Blu-ray Editions (N)</h3>, etc.).
func ParseMovieReleasesAll(rawHTML []byte, movieID string) (*MovieReleases, error) {
	htmlStr := EnsureUTF8(rawHTML)
	doc, err := xhtml.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return nil, fmt.Errorf("failed to parse movie releases HTML: %w", err)
	}

	releases := &MovieReleases{
		MovieID: movieID,
	}

	tds := findNodes(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "td"
	})
	if len(tds) == 0 {
		tds = []*xhtml.Node{doc}
	}

	for _, td := range tds {
		var currentSection string
		var currentCountry Country
		var currentItem *ReleaseSummary

		flushItem := func() {
			if currentItem == nil {
				return
			}
			secLower := strings.ToLower(currentSection)
			switch {
			case strings.Contains(secLower, "3d blu-ray"):
				currentItem.Is3D = true
				releases.BluRay3D = append(releases.BluRay3D, *currentItem)
			case strings.Contains(secLower, "4k blu-ray"):
				currentItem.Is4K = true
				releases.BluRay4K = append(releases.BluRay4K, *currentItem)
				if currentItem.Is3D {
					releases.BluRay3D = append(releases.BluRay3D, *currentItem)
				}
			case strings.Contains(secLower, "blu-ray bundles"):
				releases.BluRayBundles = append(releases.BluRayBundles, *currentItem)
			case strings.Contains(secLower, "blu-ray editions"):
				releases.BluRay = append(releases.BluRay, *currentItem)
			case strings.Contains(secLower, "dvd"):
				releases.DVD = append(releases.DVD, *currentItem)
			default:
				if currentSection != "" {
					releases.Digital = append(releases.Digital, *currentItem)
				}
			}
			currentItem = nil
		}

		for c := td.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xhtml.ElementNode {
				switch c.Data {
				case "h3":
					flushItem()
					currentSection = nodeText(c)
				case "img":
					if fm := reFlagCountry.FindStringSubmatch(nodeAttr(c, "src")); len(fm) > 1 {
						flushItem()
						currentCountry = Country(strings.ToLower(fm[1]))
					}
				case "a":
					href := decodeLinkWrapper(nodeAttr(c, "href"))
					if href == "" || strings.HasPrefix(href, "#") {
						continue
					}
					flushItem()
					title := nodeText(c)
					fullTitleAttr := cleanText(nodeAttr(c, "title"))
					if fullTitleAttr != "" && strings.HasSuffix(title, "...") {
						title = fullTitleAttr
					}
					currentItem = &ReleaseSummary{
						ID:            extractIDFromURL(href),
						ParentMovieID: movieID,
						Title:         title,
						Edition:       fullTitleAttr,
						URL:           href,
						Country:       currentCountry,
						Is3D:          is3DIndicator(title, fullTitleAttr, href, currentSection),
						Is4K:          is4KIndicator(title, fullTitleAttr, href, currentSection),
					}
				case "small":
					if currentItem != nil {
						style := nodeAttr(c, "style")
						txt := nodeText(c)
						if txt != "" {
							if strings.Contains(style, "999999") {
								currentItem.Studio = txt
							} else if strings.Contains(style, "green") {
								currentItem.Price = txt
							}
						}
					}
				case "br":
					flushItem()
				}
			}
		}
		flushItem()
	}

	return releases, nil
}

// ParseTheatricalSearch parses the HTML returned by /search/?quicksearch=1&section=theatrical.
func ParseTheatricalSearch(rawHTML []byte) ([]MovieDetails, int, error) {
	htmlStr := EnsureUTF8(rawHTML)
	if !reSearchPage.MatchString(htmlStr) && !strings.Contains(htmlStr, `class="figure"`) && !reTheatricalCnt.MatchString(htmlStr) {
		return nil, 0, fmt.Errorf("bluray: parse theatrical search: body does not look like a search results page (length=%d)", len(rawHTML))
	}
	doc, err := xhtml.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to parse theatrical search HTML: %w", err)
	}

	totalMatches := 0
	h4Nodes := findNodes(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "h4"
	})
	for _, h4 := range h4Nodes {
		if m := reTheatricalCnt.FindStringSubmatch(nodeText(h4)); len(m) > 1 {
			totalMatches, _ = strconv.Atoi(m[1])
			break
		}
	}

	figDivs := findNodes(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "div" && hasClass(n, "figure")
	})

	var movies []MovieDetails
	for _, fig := range figDivs {
		a := findFirstNode(fig, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "a" && hasClass(n, "hoverlink")
		})
		if a == nil {
			continue
		}
		href := decodeLinkWrapper(nodeAttr(a, "href"))
		id := nodeAttr(a, "data-globalproductid")
		if id == "" {
			id = extractIDFromURL(href)
		}
		alt := cleanText(nodeAttr(a, "alt"))

		m := MovieDetails{
			ID:    id,
			Title: alt,
			URL:   href,
		}
		if ym := reYearParens.FindStringSubmatch(alt); len(ym) > 1 {
			m.Year, _ = strconv.Atoi(ym[1])
		}

		capBottom := findFirstNode(fig, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "div" && hasClass(n, "figurecaptionbottom")
		})
		if capBottom != nil {
			var childDivs []*xhtml.Node
			for c := capBottom.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == xhtml.ElementNode && c.Data == "div" {
					childDivs = append(childDivs, c)
				}
			}
			if len(childDivs) >= 1 {
				if t := nodeText(childDivs[0]); t != "" {
					m.Title = t
				}
			}
			if len(childDivs) >= 2 {
				if ym := reYearBare.FindStringSubmatch(nodeText(childDivs[1])); len(ym) > 1 {
					m.Year, _ = strconv.Atoi(ym[1])
				}
			}
		}

		capTop := findFirstNode(fig, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "div" && hasClass(n, "figurecaptiontop")
		})
		if capTop != nil {
			starSpan := findFirstNode(capTop, func(n *xhtml.Node) bool {
				return n.Type == xhtml.ElementNode && n.Data == "span" && hasClass(n, "fa-star")
			})
			if starSpan != nil {
				m.Rating, _ = strconv.ParseFloat(nodeText(starSpan), 64)
			}
		}

		movies = append(movies, m)
	}

	return movies, totalMatches, nil
}
