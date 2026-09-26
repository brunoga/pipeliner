package torrentclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// hostPort splits an httptest server URL into Config Host/Port values.
func hostPort(t *testing.T, srvURL string) (string, int) {
	t.Helper()
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse server port: %v", err)
	}
	return u.Hostname(), port
}

// trRequest is the decoded JSON-RPC request body received by the mock server.
type trRequest struct {
	Method    string         `json:"method"`
	Arguments map[string]any `json:"arguments"`
}

// newTransmissionServer builds a mock Transmission RPC endpoint that enforces
// the 409 session-id handshake and dispatches on method name.
func newTransmissionServer(t *testing.T, handler func(req trRequest) any) (*httptest.Server, *[]trRequest) {
	t.Helper()
	var calls []trRequest
	const sessionID = "test-session-id"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") != sessionID {
			w.Header().Set("X-Transmission-Session-Id", sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		var req trRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode rpc request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls = append(calls, req)
		resp := handler(req)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode rpc response: %v", err)
		}
	}))
	return srv, &calls
}

func transmissionListResponse(torrents []map[string]any) map[string]any {
	return map[string]any{
		"result": "success",
		"arguments": map[string]any{
			"torrents": torrents,
		},
	}
}

func TestTransmissionListTorrents(t *testing.T) {
	added := time.Now().Add(-24 * time.Hour).Unix()
	activity := time.Now().Add(-6 * time.Hour).Unix()
	srv, calls := newTransmissionServer(t, func(req trRequest) any {
		if req.Method != "torrent-get" {
			t.Errorf("unexpected method %q", req.Method)
		}
		return transmissionListResponse([]map[string]any{
			{
				"hashString": "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
				"name":       "Show.S01E01.720p", "status": 6,
				"error": 0, "errorString": "",
				"isStalled": false, "percentDone": 1.0,
				"uploadRatio": 2.5, "secondsSeeding": 3600,
				"addedDate": added, "activityDate": activity,
				"downloadDir": "/data/tv",
			},
			{
				"hashString": "1111111111111111111111111111111111111111",
				"name":       "Dead.Torrent", "status": 4,
				"error": 3, "errorString": "no data found",
				"isStalled": true, "percentDone": 0.0,
				"uploadRatio": -1.0, "secondsSeeding": 0,
				"addedDate": added, "activityDate": 0,
				"downloadDir": "/data/tv",
			},
			{
				"hashString": "2222222222222222222222222222222222222222",
				"name":       "Stalled.Torrent", "status": 4,
				"error": 0, "errorString": "",
				"isStalled": true, "percentDone": 0.25,
				"uploadRatio": 0.0, "secondsSeeding": 0,
				"addedDate": added, "activityDate": activity,
				"downloadDir": "/data/tv",
			},
			{
				"hashString": "3333333333333333333333333333333333333333",
				"name":       "Paused.Torrent", "status": 0,
				"error": 0, "errorString": "",
				"isStalled": false, "percentDone": 0.5,
				"uploadRatio": 0.1, "secondsSeeding": 10,
				"addedDate": added, "activityDate": activity,
				"downloadDir": "/data/tv",
			},
			{
				"hashString": "4444444444444444444444444444444444444444",
				"name":       "Checking.Torrent", "status": 2,
				"error": 0, "errorString": "",
				"isStalled": false, "percentDone": 0.5,
				"uploadRatio": 0.0, "secondsSeeding": 0,
				"addedDate": added, "activityDate": activity,
				"downloadDir": "/data/tv",
			},
			{
				"hashString": "5555555555555555555555555555555555555555",
				"name":       "Active.Download", "status": 4,
				"error": 0, "errorString": "",
				"isStalled": false, "percentDone": 0.75,
				"uploadRatio": 0.0, "secondsSeeding": 0,
				"addedDate": added, "activityDate": activity,
				"downloadDir": "/data/tv",
			},
		})
	})
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	c, err := New(BackendTransmission, Config{Host: host, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	torrents, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(torrents) != 6 {
		t.Fatalf("got %d torrents, want 6", len(torrents))
	}

	seeding := torrents[0]
	if seeding.Hash != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Errorf("hash not lowercased: %q", seeding.Hash)
	}
	if seeding.State != StateSeeding {
		t.Errorf("seeding state = %q", seeding.State)
	}
	if seeding.Ratio != 2.5 {
		t.Errorf("ratio = %v", seeding.Ratio)
	}
	if seeding.SeedTime != time.Hour {
		t.Errorf("seed time = %v", seeding.SeedTime)
	}
	if seeding.Progress != 100 {
		t.Errorf("progress = %v", seeding.Progress)
	}
	if seeding.DownloadDir != "/data/tv" {
		t.Errorf("download dir = %q", seeding.DownloadDir)
	}
	if seeding.AddedAt.Unix() != added {
		t.Errorf("added at = %v", seeding.AddedAt)
	}
	if seeding.LastActivity.Unix() != activity {
		t.Errorf("last activity = %v", seeding.LastActivity)
	}

	errored := torrents[1]
	if errored.State != StateErrored {
		t.Errorf("errored state = %q", errored.State)
	}
	if errored.Error != "no data found" {
		t.Errorf("error message = %q", errored.Error)
	}
	if errored.Ratio != 0 {
		t.Errorf("negative ratio not clamped: %v", errored.Ratio)
	}
	if !errored.LastActivity.IsZero() {
		t.Errorf("zero activityDate should map to zero time, got %v", errored.LastActivity)
	}

	if torrents[2].State != StateStalled {
		t.Errorf("stalled state = %q", torrents[2].State)
	}
	if torrents[3].State != StatePaused {
		t.Errorf("paused state = %q", torrents[3].State)
	}
	if torrents[4].State != StateChecking {
		t.Errorf("checking state = %q", torrents[4].State)
	}
	if torrents[5].State != StateDownloading {
		t.Errorf("downloading state = %q", torrents[5].State)
	}

	// The 409 handshake means the first accepted request is the only call.
	if len(*calls) != 1 {
		t.Errorf("server saw %d rpc calls, want 1", len(*calls))
	}
}

func TestTransmissionControlOps(t *testing.T) {
	srv, calls := newTransmissionServer(t, func(req trRequest) any {
		return map[string]any{"result": "success", "arguments": map[string]any{}}
	})
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	c, err := New(BackendTransmission, Config{Host: host, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hashes := []string{"aaaa", "bbbb"}

	if err := c.Remove(ctx, hashes, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := c.Pause(ctx, hashes); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := c.Reannounce(ctx, hashes); err != nil {
		t.Fatalf("Reannounce: %v", err)
	}

	if len(*calls) != 3 {
		t.Fatalf("server saw %d calls, want 3", len(*calls))
	}
	remove := (*calls)[0]
	if remove.Method != "torrent-remove" {
		t.Errorf("call 0 method = %q", remove.Method)
	}
	if del, _ := remove.Arguments["delete-local-data"].(bool); !del {
		t.Errorf("delete-local-data not set: %v", remove.Arguments)
	}
	if ids, _ := remove.Arguments["ids"].([]any); len(ids) != 2 {
		t.Errorf("ids = %v", remove.Arguments["ids"])
	}
	if (*calls)[1].Method != "torrent-stop" {
		t.Errorf("call 1 method = %q", (*calls)[1].Method)
	}
	if (*calls)[2].Method != "torrent-reannounce" {
		t.Errorf("call 2 method = %q", (*calls)[2].Method)
	}
}

func TestTransmissionControlErrorResult(t *testing.T) {
	srv, _ := newTransmissionServer(t, func(req trRequest) any {
		return map[string]any{"result": "invalid torrent id", "arguments": map[string]any{}}
	})
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	c, err := New(BackendTransmission, Config{Host: host, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(context.Background(), []string{"aaaa"}, false); err == nil {
		t.Fatal("expected error from non-success result")
	}
}

func TestTransmissionEmptyHashesNoCalls(t *testing.T) {
	srv, calls := newTransmissionServer(t, func(req trRequest) any {
		return map[string]any{"result": "success", "arguments": map[string]any{}}
	})
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	c, err := New(BackendTransmission, Config{Host: host, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Remove(ctx, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := c.Pause(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Reannounce(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("server saw %d calls, want 0", len(*calls))
	}
}

func TestNewUnsupportedBackend(t *testing.T) {
	if _, err := New("rtorrent", Config{}); err == nil {
		t.Fatal("expected error for unsupported backend")
	}
}

// TestTransmissionRichFields covers the session detail added for janitor
// reporting. Two Transmission specifics are asserted: Peers is derived
// (peersConnected counts seeds too, so the non-seed count is the
// difference), and a negative eta is the "not available"/"unknown"
// sentinel rather than a real estimate.
func TestTransmissionRichFields(t *testing.T) {
	srv, _ := newTransmissionServer(t, func(req trRequest) any {
		return transmissionListResponse([]map[string]any{
			{
				"hashString": "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
				"name":       "rich", "status": 4, "error": 0, "errorString": "",
				"isStalled": false, "percentDone": 0.5,
				"uploadRatio": 0.3, "secondsSeeding": 60,
				"addedDate": 1700000000, "activityDate": 1700003600,
				"downloadDir":      "/downloads",
				"totalSize":        15_400_000_000,
				"downloadedEver":   7_700_000_000,
				"uploadedEver":     1_000_000_000,
				"rateDownload":     2_500_000,
				"rateUpload":       300_000,
				"peersConnected":   15,
				"peersSendingToUs": 4,
				"eta":              2560,
				"doneDate":         1700007200,
				"labels":           []string{"movies", "3d"},
				"trackerStats":     []map[string]any{{"host": "tracker.example.org"}},
			},
			{
				"hashString": "1111111111111111111111111111111111111111",
				"name":       "unknown-eta", "status": 4, "error": 0, "errorString": "",
				"isStalled": true, "percentDone": 0.0,
				"uploadRatio": -1, "secondsSeeding": 0,
				"addedDate": 1700000000, "activityDate": 0,
				"downloadDir": "/downloads",
				"eta":         -1,
				"doneDate":    0,
			},
		})
	})
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	c, err := New(BackendTransmission, Config{Host: host, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	list, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatalf("ListTorrents: %v", err)
	}

	rich := list[0]
	if rich.Size != 15_400_000_000 {
		t.Errorf("size: got %d", rich.Size)
	}
	if rich.Downloaded != 7_700_000_000 || rich.Uploaded != 1_000_000_000 {
		t.Errorf("counters: down=%d up=%d", rich.Downloaded, rich.Uploaded)
	}
	if rich.DownloadRate != 2_500_000 || rich.UploadRate != 300_000 {
		t.Errorf("rates: down=%d up=%d", rich.DownloadRate, rich.UploadRate)
	}
	if rich.Seeds != 4 {
		t.Errorf("seeds: got %d, want 4 (peersSendingToUs)", rich.Seeds)
	}
	if rich.Peers != 11 {
		t.Errorf("peers: got %d, want 11 (connected minus sending)", rich.Peers)
	}
	if rich.ETA != 2560*time.Second {
		t.Errorf("eta: got %v", rich.ETA)
	}
	if rich.Label != "movies" {
		t.Errorf("label: got %q, want the first label", rich.Label)
	}
	if rich.Tracker != "tracker.example.org" {
		t.Errorf("tracker: got %q", rich.Tracker)
	}
	if rich.CompletedAt.Unix() != 1700007200 {
		t.Errorf("completed at: got %v", rich.CompletedAt.Unix())
	}

	unknown := list[1]
	if unknown.ETA != 0 {
		t.Errorf("negative eta sentinel: got %v, want 0", unknown.ETA)
	}
	if !unknown.CompletedAt.IsZero() {
		t.Errorf("doneDate 0: got %v, want zero", unknown.CompletedAt)
	}
	if unknown.Label != "" || unknown.Tracker != "" {
		t.Errorf("absent optionals leaked: label=%q tracker=%q", unknown.Label, unknown.Tracker)
	}
}

// TestTransmissionPeersNeverNegative guards the derived Peers count: a
// backend reporting more seeds than total connections must clamp to zero,
// not wrap into a negative peer count.
func TestTransmissionPeersNeverNegative(t *testing.T) {
	srv, _ := newTransmissionServer(t, func(req trRequest) any {
		return transmissionListResponse([]map[string]any{{
			"hashString": "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
			"name":       "odd", "status": 4, "percentDone": 0.5,
			"addedDate": 1700000000, "downloadDir": "/downloads",
			"peersConnected": 2, "peersSendingToUs": 5,
		}})
	})
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	c, err := New(BackendTransmission, Config{Host: host, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	list, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatalf("ListTorrents: %v", err)
	}
	if list[0].Peers != 0 {
		t.Errorf("peers: got %d, want 0", list[0].Peers)
	}
}
