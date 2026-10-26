package bluray

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const sampleSearchFullHTML = `<!DOCTYPE html>
<html>
<body>
<h2 class="oswaldcollection">Search movies (1145 matches) <span><img src="https://images.static-bluray.com/flags/US.png"></span></h2>
<table width="100%" border="0" cellpadding="0" cellspacing="0">
<tr>
	<td width="15%">
		<center>
			<a href="https://www.blu-ray.com/movies/Dredd-3D-Blu-ray/56292/"><img class="cover" src="https://images.static-bluray.com/movies/covers/56292_small.jpg" title="Dredd 3D (Blu-ray)"></a>
		</center>
	</td>
	<td width="85%">
		<a href="https://www.blu-ray.com/movies/Dredd-3D-Blu-ray/56292/" class="noline"><h3>Dredd 3D Blu-ray</h3></a>
		&nbsp;<img src="https://images.static-bluray.com/flags/US.png" title="United States">
		&nbsp;<img src="https://images.static-bluray.com/rating/b8.jpg" title="Overall: 4.2 of 5">
		<br><a href="https://www.blu-ray.com/movies/Dredd-3D-Blu-ray/56292/" class="noline"><span class="subheadingsmall">Blu-ray 3D + Blu-ray</span></a><br>
		<small style="color: #666666">Lionsgate Films | 2012 | 96 min | Rated R | 2K Blu-ray: Region A <font style="color: #999999">(locked)</font> | Jan 08, 2013</small><br>
		In a violent, futuristic city where the police have the authority to act as judge, jury and executioner.<br>
		<a href="https://www.blu-ray.com/movies/movies.php?genre=Action">Action</a> / <a href="https://www.blu-ray.com/movies/movies.php?genre=Sci-Fi">Sci-Fi</a>
	</td>
</tr>
</table>
<div class="button"><a href="https://www.blu-ray.com/movies/search.php?action=search&other_bluray3d=1&page=1">Next &raquo;</a></div>
</body>
</html>`

const sampleReleaseHTML = `<!DOCTYPE html>
<html>
<head>
<meta property="og:url" content="https://www.blu-ray.com/movies/Dredd-3D-Blu-ray/56292/" />
</head>
<body>
<table>
<tr>
<td width="518">
<a class="black noline" data-globalproductid="128351" data-globalparentid="128351" href="https://www.blu-ray.com/Dredd/128351/"><h1>Dredd 3D Blu-ray</h1></a><img src="https://images.static-bluray.com/flags/US.png" title="United States">
</td>
</tr>
</table>
<span class="subheadingtitle">Blu-ray 3D + Blu-ray</span>
<span class="subheading grey">Lionsgate Films | 2012 | 96 min | Rated R | Jan 08, 2013</span>
<img class="coverfront" id="frontimage_overlay" src="https://images.static-bluray.com/movies/covers/56292_medium.jpg">
<script>
$('#largefrontimage_container').append('<img id="largefrontimage" src="https://images.static-bluray.com/movies/covers/56292_front.jpg">');
$('#largebackimage_container').append('<img id="largebackimage" src="https://images.static-bluray.com/movies/covers/56292_back.jpg">');
$('#largeslipimage_container').append('<img id="largeslipimage" src="https://images.static-bluray.com/movies/covers/56292_slip.jpg">');
</script>
<div class="genreappeal"><div>Action</div></div>
<div class="genreappeal"><div>Sci-Fi</div></div>
<table>
<tr>
<td width="228px">
	<span class="subheading">Video</span><br>
	Codec: MPEG-4 MVC (27.50 Mbps)<br>
	Resolution: 1080p<br>
	Aspect ratio: 2.40:1<br>
	Original aspect ratio: 2.39:1<br>
	<span class="subheading">Audio</span><br>
	<div id="shortaudio">English: DTS-HD Master Audio 7.1, Neo:X<br>Spanish: Dolby Digital 5.1 (640 kbps)<br></div>
	<div id="longaudio">English: DTS-HD Master Audio 7.1, Neo:X<br>Spanish: Dolby Digital 5.1 (640 kbps)<br>(less)</div>
	<span class="subheading">Subtitles</span><br>
	<div id="shortsubs">English, English SDH, Spanish</div>
	<div id="longsubs">English, English SDH, Spanish (less)</div>
	<span class="subheading">Discs</span><br>
	Blu-ray 3D<br>Blu-ray Disc<br>Single disc (1 BD-50)<br>
	<span class="subheading">Digital</span><br>
	Digital copy expired<br>
	<span class="subheading">Packaging</span><br>
	Slipcover in original pressing<br>
	<span class="subheading">Playback</span><br>
	2K Blu-ray: Region A (locked)<br>
</td>
<td width="266px">
	<span class="subheading">Price</span><br>
	List price: <strike>$14.99</strike><br>
	Amazon: <a href="https://www.blu-ray.com/link/click.php?p=148366">$8.73</a><br>
	<div id="ratingscore_128351_0">7.7</div>
	<div id="votes_128351_0">1930</div>
	<a id="imdb_icon" href="https://www.imdb.com/title/tt1343727/"></a>
	<a id="rt_icon" href="http://www.rottentomatoes.com/m/dredd/"></a>
	<div id="review_rating">
		<table>
			<tr><td>Video</td><td></td><td>4.5</td></tr>
			<tr><td>3D</td><td></td><td>4.0</td></tr>
			<tr><td>Audio</td><td></td><td>5.0</td></tr>
			<tr><td>Extras</td><td></td><td>2.0</td></tr>
			<tr><td>Overall</td><td></td><td><b>4.5</b></td></tr>
		</table>
	</div>
	<div id="user_rating">
		<table>
			<tr><td>Video</td><td></td><td>4.3</td></tr>
			<tr><td>3D</td><td></td><td>3.9</td></tr>
			<tr><td>Audio</td><td></td><td>4.7</td></tr>
			<tr><td>Extras</td><td></td><td>3.1</td></tr>
			<tr><td>Overall</td><td></td><td><b>4.2</b></td></tr>
		</table>
	</div>
</td>
</tr>
</table>
<div id="movie_info">
<h3>Dredd 3D</h3> (2012)<br>
In a violent, futuristic city where the police have the authority to act as judge, jury and executioner.<br>
Director: <a href="https://www.blu-ray.com/Pete-Travis/69545/">Pete Travis</a><br>
Writers: <a href="https://www.blu-ray.com/Alex-Garland/68175/">Alex Garland</a>, <a href="https://www.blu-ray.com/John-Wagner/483781/">John Wagner</a><br>
Starring: <a href="https://www.blu-ray.com/Karl-Urban/68904/">Karl Urban</a>, <a href="https://www.blu-ray.com/Olivia-Thirlby/69530/">Olivia Thirlby</a><br>
</div>
</body>
</html>`

const sampleMovieHTML = `<!DOCTYPE html>
<html>
<body>
<div class="blackbox">
	<h1 class="eurostile">Dredd</h1>
	<font class="oswald">2012</font>
</div>
<div id="content_overview">
	<div class="genreappeal"><div>Action</div></div>
	<div class="genreappeal"><div>Sci-Fi</div></div>
	<table>
		<tr><td class="specmenu">Studio</td><td class="specitem"><a href="#">Lionsgate</a></td></tr>
		<tr><td class="specmenu">Country</td><td class="specitem">UK</td></tr>
		<tr><td class="specmenu">Language</td><td class="specitem">English</td></tr>
		<tr><td class="specmenu">Runtime</td><td class="specitem">95 min</td></tr>
		<tr><td class="specmenu">Rated</td><td class="specitem">R</td></tr>
		<tr><td class="specmenu">Technical details</td><td class="specitem">2.39:1<br><span>Native 3D, 95 mins</span><br></td></tr>
	</table>
	<div style="width: 700px;">
		<div id="longsynopsis">In a violent, futuristic city where the police have the authority to act as judge, jury and executioner. (less)</div>
		Director: <a href="https://www.blu-ray.com/Pete-Travis/69545/">Pete Travis</a><br>
		Starring: <a href="https://www.blu-ray.com/Karl-Urban/68904/">Karl Urban</a><br>
	</div>
</div>
</body>
</html>`

const sampleReleasesAllHTML = `<table><tr><td>
<h3>4K Blu-ray Editions (1)</h3><br>
<img src="https://images.static-bluray.com/flags/DE.png" title="Germany">
<a href="https://www.blu-ray.com/movies/Dredd-4K-Blu-ray/229773/" title="Dredd 4K">Dredd 4K</a>
<small style="color: #999999">Universum Film</small> <small style="color: green"></small><br>
<h3>3D Blu-ray Editions (2)</h3><br>
<img src="https://images.static-bluray.com/flags/US.png" title="United States">
<a href="https://www.blu-ray.com/movies/Dredd-3D-Blu-ray/56292/" title="Dredd 3D">Dredd 3D</a>
<small style="color: #999999">Lionsgate Films</small> <small style="color: green">$8.73</small><br>
<img src="https://images.static-bluray.com/flags/UK.png" title="United Kingdom">
<a href="https://www.blu-ray.com/movies/Dredd-3D-Blu-ray/56160/" title="Dredd 3D">Dredd 3D</a>
<small style="color: #999999">Entertainment in Video</small> <small style="color: green"></small><br>
</td></tr></table>`

const sampleTheatricalSearchHTML = `<!DOCTYPE html>
<html>
<head><title>Blu-ray.com - Search</title></head>
<body>
<h4>2 matches for "Dredd"</h4>
<div class="figure">
	<a class="alphaborder hoverlink" data-globalproductid="128351" href="https://www.blu-ray.com/Dredd/128351/" alt="Dredd (2012)"></a>
	<div class="figurecaptiontop"><span class="fa fa-star"> 7.7</span></div>
	<div class="figurecaptionbottom"><div>Dredd</div><div>2012</div></div>
</div>
<div class="figure">
	<a class="alphaborder hoverlink" data-globalproductid="27267" href="https://www.blu-ray.com/Judge-Dredd/27267/" alt="Judge Dredd (1995)"></a>
	<div class="figurecaptiontop"><span class="fa fa-star"> 5.8</span></div>
	<div class="figurecaptionbottom"><div>Judge Dredd</div><div>1995</div></div>
</div>
</body>
</html>`

const sampleQuickSearchHTML = `<ul>
<li id="match0"><span>Jan 08, 2013</span><img src="https://images.static-bluray.com/flags/US.png">&nbsp;Dredd 3D (2012)</li>
</ul>
<script type="text/javascript">
maxpos = 1;
var ids = new Array('56292');
var urls = new Array('https://www.blu-ray.com/movies/Dredd-3D-Blu-ray/56292/');
var images = new Array('https://images.static-bluray.com/movies/covers/56292_medium.jpg');
var ratings = new Array('8');
var countrycodes = new Array('US');
</script>`

func TestBluraycomParsers(t *testing.T) {
	// 1. ParseSearchResults
	page, err := ParseSearchResults([]byte(sampleSearchFullHTML), CountryUS, 0)
	if err != nil {
		t.Fatalf("ParseSearchResults failed: %v", err)
	}
	if page.TotalMatches != 1145 || !page.HasNextPage || len(page.Results) != 1 {
		t.Fatalf("unexpected search page metadata: %+v", page)
	}
	r0 := page.Results[0]
	if r0.ID != "56292" || r0.Title != "Dredd 3D Blu-ray" || !r0.Is3D || r0.Year != 2012 || r0.RuntimeMins != 96 || r0.OverallRating != 4.2 {
		t.Errorf("unexpected parsed search item: %+v", r0)
	}

	// 2. ParseReleaseDetails
	rel, err := ParseReleaseDetails([]byte(sampleReleaseHTML), "https://www.blu-ray.com/movies/Dredd-3D-Blu-ray/56292/")
	if err != nil {
		t.Fatalf("ParseReleaseDetails failed: %v", err)
	}
	if rel.ID != "56292" || rel.ParentMovieID != "128351" || !rel.Video.Is3D || rel.Video.Codec != "MPEG-4 MVC (27.50 Mbps)" || rel.Codec != "MPEG-4 MVC" {
		t.Errorf("unexpected release video specs: %+v", rel)
	}
	if len(rel.Audio) != 2 || rel.Audio[0].Language != "English" || len(rel.Subtitles) != 3 {
		t.Errorf("unexpected audio/subtitles: audio=%+v, subs=%+v", rel.Audio, rel.Subtitles)
	}
	if !rel.Discs.Is3D || rel.Discs.Count != 1 || rel.Ratings.Review3D != 4.0 || rel.Price.CurrentPrice != "$8.73" {
		t.Errorf("unexpected discs/ratings/price: discs=%+v, ratings=%+v, price=%+v", rel.Discs, rel.Ratings, rel.Price)
	}
	if rel.Director != "Pete Travis" || len(rel.Starring) != 2 {
		t.Errorf("unexpected crew: director=%q, starring=%+v", rel.Director, rel.Starring)
	}

	// 3. ParseMovieDetails & ParseMovieReleasesAll
	mov, err := ParseMovieDetails([]byte(sampleMovieHTML), "https://www.blu-ray.com/Dredd/128351/")
	if err != nil {
		t.Fatalf("ParseMovieDetails failed: %v", err)
	}
	if mov.ID != "128351" || mov.Title != "Dredd" || mov.Year != 2012 || !mov.IsNative3D {
		t.Errorf("unexpected movie details: %+v", mov)
	}

	rels, err := ParseMovieReleasesAll([]byte(sampleReleasesAllHTML), "128351")
	if err != nil {
		t.Fatalf("ParseMovieReleasesAll failed: %v", err)
	}
	if !rels.Has3D() || len(rels.BluRay3D) != 2 || len(rels.BluRay4K) != 1 {
		t.Errorf("unexpected movie releases: %+v", rels)
	}

	// 4. ParseQuickSearch
	qs, err := ParseQuickSearch([]byte(sampleQuickSearchHTML))
	if err != nil {
		t.Fatalf("ParseQuickSearch failed: %v", err)
	}
	if len(qs) != 1 || qs[0].ID != "56292" || !qs[0].Is3D || qs[0].Year != 2012 {
		t.Errorf("unexpected quicksearch result: %+v", qs)
	}
}

func TestClientBrowserSimulationAndWorkflows(t *testing.T) {
	var hits atomic.Int32
	var sawWarmup atomic.Bool
	var sawChromeHeaders atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.Contains(r.Header.Get("User-Agent"), "Chrome/") && r.Header.Get("Sec-Ch-Ua") != "" {
			sawChromeHeaders.Store(true)
		}

		switch r.URL.Path {
		case "/":
			sawWarmup.Store(true)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html><body>Home</body></html>"))
		case "/search/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(sampleTheatricalSearchHTML))
		case "/Movie/128351/", "/Dredd/128351/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(sampleMovieHTML))
		case "/products/menu_ajax.php":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(sampleReleasesAllHTML))
		case "/movies/search.php":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(sampleSearchFullHTML))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	memCache := NewMemoryCache(5 * time.Minute)
	client, err := NewClient(
		WithBaseURL(srv.URL),
		WithWarmup(true),
		WithRateLimit(5*time.Millisecond, 5*time.Millisecond),
		WithCache(memCache),
	)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx := context.Background()
	check, err := client.CheckMovieHas3D(ctx, "Dredd", CountryUS)
	if err != nil {
		t.Fatalf("CheckMovieHas3D failed: %v", err)
	}
	if !sawWarmup.Load() {
		t.Errorf("expected automatic homepage warmup request")
	}
	if !sawChromeHeaders.Load() {
		t.Errorf("expected Chrome User-Agent and Sec-Ch-Ua headers")
	}
	if !check.Has3D || len(check.Releases3D) != 1 || check.Releases3D[0].ID != "56292" {
		t.Errorf("expected 1 US 3D release for Dredd, got: %+v", check)
	}

	// Second call should be served completely from cache without hitting server
	beforeHits := hits.Load()
	check2, err := client.CheckMovieHas3D(ctx, "Dredd", CountryUS)
	if err != nil || !check2.Has3D {
		t.Fatalf("cached CheckMovieHas3D failed: %v", err)
	}
	if hits.Load() != beforeHits {
		t.Errorf("expected cached response to avoid server hits: before=%d, after=%d", beforeHits, hits.Load())
	}

	// Test DiskCache
	tmpDir, err := os.MkdirTemp("", "bluraycache-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dc, err := NewDiskCache(filepath.Join(tmpDir, "sub"), time.Minute)
	if err != nil {
		t.Fatalf("NewDiskCache failed: %v", err)
	}
	dc.Set("k1", []byte("hello"))
	if got, ok := dc.Get("k1"); !ok || string(got) != "hello" {
		t.Errorf("unexpected disk cache value: %q, ok=%v", string(got), ok)
	}
}

func TestEmbedded3DCatalog(t *testing.T) {
	cat, err := Embedded3DCatalog()
	if err != nil {
		t.Fatalf("Embedded3DCatalog failed: %v", err)
	}
	if cat.Len() < 14000 {
		t.Fatalf("expected > 14000 3D releases in embedded catalog, got %d", cat.Len())
	}

	// Lookup Dredd (2012) worldwide
	dreddAll := cat.LookupTitle("Dredd", 2012, CountryAll)
	if len(dreddAll) == 0 {
		t.Fatal("expected Dredd (2012) to be found in embedded 3D catalog")
	}

	// Lookup by ID
	if rel, ok := cat.LookupID("56292"); !ok {
		t.Error("expected ID 56292 (Dredd US 3D) in embedded catalog")
	} else if !rel.Is3D || rel.Year != 2012 {
		t.Errorf("unexpected Dredd 56292 record: %+v", rel)
	}
}
