// Command request-movie pushes a movie title to a pipeliner on-demand
// pipeline from anywhere: a laptop, a phone SSH client, a cron job.
//
// It POSTs {"title": "..."} to /api/ingest/{queue}?pipeline={pipeline},
// authenticated with the ingest bearer token (PIPELINER_INGEST_TOKEN on the
// daemon side). Pair it with a pipeline like configs/ondemand-request.star:
// a webhook source draining the queue into a Jackett search, followed by the
// usual quality gates and a download client.
//
// Usage:
//
//	request-movie [flags] <movie title...>
//	request-movie "Heat 1995"
//	request-movie -url https://pipeliner.example.com "The Matrix" 1999
//
// The ingest token is resolved, in order, from:
//
//  1. the -token flag
//  2. the PIPELINER_INGEST_TOKEN environment variable
//  3. the first line of ~/.config/pipeliner/ingest-token
//
// The server URL comes from -url, then PIPELINER_URL, then
// http://localhost:8080.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const tokenFileRel = ".config/pipeliner/ingest-token"

func main() {
	var (
		urlFlag   = flag.String("url", "", "pipeliner base URL (default: $PIPELINER_URL, then http://localhost:8080)")
		token     = flag.String("token", "", "ingest bearer token (default: $PIPELINER_INGEST_TOKEN, then ~/"+tokenFileRel+")")
		queue     = flag.String("queue", "movies", "ingest queue name (the webhook source's 'queue' config)")
		pipelineF = flag.String("pipeline", "movies-ondemand", "pipeline to trigger immediately after queueing")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [flags] <movie title...>\n\nflags:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	title := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if title == "" {
		flag.Usage()
		os.Exit(2)
	}

	home, _ := os.UserHomeDir()
	tok, source, err := resolveToken(*token, os.Getenv("PIPELINER_INGEST_TOKEN"), filepath.Join(home, tokenFileRel))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	base := resolveURL(*urlFlag, os.Getenv("PIPELINER_URL"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := push(ctx, http.DefaultClient, base, tok, *queue, *pipelineF, title)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	fmt.Printf("requested %q → queued=%d dropped=%d rejected=%d triggered=%v (token from %s)\n",
		title, resp.Queued, resp.Dropped, resp.Rejected, resp.Triggered, source)
	if !resp.Triggered {
		fmt.Println("note: pipeline was not triggered — check the -pipeline name matches a pipeline in the config")
	}
}

// resolveToken picks the ingest token: flag, then environment, then the
// token file. The returned source names where it came from, for the
// confirmation line.
func resolveToken(flagVal, envVal, tokenFile string) (token, source string, err error) {
	if flagVal != "" {
		return flagVal, "flag", nil
	}
	if envVal != "" {
		return envVal, "$PIPELINER_INGEST_TOKEN", nil
	}
	data, ferr := os.ReadFile(tokenFile)
	if ferr == nil {
		line, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
		if line != "" {
			return strings.TrimSpace(line), tokenFile, nil
		}
	}
	return "", "", fmt.Errorf("no ingest token: pass -token, set PIPELINER_INGEST_TOKEN, or write it to %s", tokenFile)
}

// resolveURL picks the server base URL: flag, then environment, then the
// local default.
func resolveURL(flagVal, envVal string) string {
	switch {
	case flagVal != "":
		return strings.TrimRight(flagVal, "/")
	case envVal != "":
		return strings.TrimRight(envVal, "/")
	}
	return "http://localhost:8080"
}

// ingestResponse mirrors the /api/ingest/{queue} reply.
type ingestResponse struct {
	Queued    int  `json:"queued"`
	Dropped   int  `json:"dropped"`
	Rejected  int  `json:"rejected"`
	Triggered bool `json:"triggered"`
}

// push sends one title to the ingest queue and asks for an immediate
// pipeline run.
func push(ctx context.Context, hc *http.Client, base, token, queue, pipeline, title string) (*ingestResponse, error) {
	body, err := json.Marshal(map[string]string{"title": title})
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/api/ingest/%s?pipeline=%s", base, queue, pipeline)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server answered %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	var out ingestResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("unexpected response: %s", strings.TrimSpace(string(payload)))
	}
	return &out, nil
}
