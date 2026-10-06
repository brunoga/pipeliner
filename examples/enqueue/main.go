// Command enqueue pushes a title to a pipeliner pipeline from anywhere: a
// laptop, a phone SSH client, a cron job. Movies, shows, discs — whatever the
// pipeline on the other end searches for.
//
// It POSTs {"title": "..."} to /api/ingest?pipeline={pipeline}, authenticated
// with the ingest bearer token (PIPELINER_INGEST_TOKEN on the daemon side).
// Pair it with a pipeline like configs/ondemand-request.star: a webhook source
// draining the queue into a Jackett search, followed by the usual quality
// gates and a download client.
//
// The queue is not given, because it is not independent information: it is the
// "queue" config of the pipeline's webhook source, so the daemon derives it
// from the pipeline name. -queue is only for a pipeline with more than one
// webhook source, where the daemon refuses to guess (and says which queues it
// could have meant).
//
// Usage:
//
//	enqueue [flags] <title...>
//	enqueue "Heat 1995"
//	enqueue -pipeline tvshows-favorite-add "Severance"
//	enqueue -url https://pipeliner.example.com "The Matrix" 1999
//
// The ingest token is resolved, in order, from:
//
//  1. the -token flag
//  2. the PIPELINER_INGEST_TOKEN environment variable
//  3. the "token" key of ~/.config/pipeliner/client.conf
//  4. the first line of ~/.config/pipeliner/ingest-token
//
// The server URL comes from -url, then PIPELINER_URL, then the "url" key of
// client.conf, then http://localhost:8080. A minimal client.conf:
//
//	url = https://pipeliner.example.com
//	token = your-ingest-token
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	tokenFileRel = ".config/pipeliner/ingest-token"
	confFileRel  = ".config/pipeliner/client.conf"
)

func main() {
	var (
		urlFlag   = flag.String("url", "", "pipeliner base URL (default: $PIPELINER_URL, then http://localhost:8080)")
		token     = flag.String("token", "", "ingest bearer token (default: $PIPELINER_INGEST_TOKEN, then ~/"+tokenFileRel+")")
		queue     = flag.String("queue", "", "ingest queue name; only needed when the pipeline has more than one webhook source (default: derived from -pipeline)")
		pipelineF = flag.String("pipeline", "movies-ondemand", "pipeline to queue into and trigger immediately")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [flags] <title...>\n\nflags:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	title := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if title == "" {
		flag.Usage()
		os.Exit(2)
	}

	home, _ := os.UserHomeDir()
	conf := loadClientConf(filepath.Join(home, confFileRel))
	tok, source, err := resolveToken(*token, os.Getenv("PIPELINER_INGEST_TOKEN"), conf["token"], filepath.Join(home, tokenFileRel))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	base := resolveURL(*urlFlag, os.Getenv("PIPELINER_URL"), conf["url"])

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := push(ctx, http.DefaultClient, base, tok, *queue, *pipelineF, title)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	fmt.Printf("enqueued %q → queue=%s queued=%d dropped=%d rejected=%d triggered=%v (token from %s)\n",
		title, resp.Queue, resp.Queued, resp.Dropped, resp.Rejected, resp.Triggered, source)
	if !resp.Triggered {
		fmt.Println("note: the pipeline was not triggered — nothing new was queued, or -pipeline was empty")
	}
}

// loadClientConf parses ~/.config/pipeliner/client.conf: "key = value"
// lines, # comments, unknown keys ignored. A missing file is an empty conf.
func loadClientConf(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return out
}

// resolveToken picks the ingest token: flag, then environment, then
// client.conf, then the token file. The returned source names where it came
// from, for the confirmation line.
func resolveToken(flagVal, envVal, confVal, tokenFile string) (token, source string, err error) {
	if flagVal != "" {
		return flagVal, "flag", nil
	}
	if envVal != "" {
		return envVal, "$PIPELINER_INGEST_TOKEN", nil
	}
	if confVal != "" {
		return confVal, "~/" + confFileRel, nil
	}
	data, ferr := os.ReadFile(tokenFile)
	if ferr == nil {
		line, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
		if line != "" {
			return strings.TrimSpace(line), tokenFile, nil
		}
	}
	return "", "", fmt.Errorf("no ingest token: pass -token, set PIPELINER_INGEST_TOKEN, or add token= to ~/%s", confFileRel)
}

// resolveURL picks the server base URL: flag, then environment, then
// client.conf, then the local default.
func resolveURL(flagVal, envVal, confVal string) string {
	for _, v := range []string{flagVal, envVal, confVal} {
		if v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return "http://localhost:8080"
}

// ingestResponse mirrors the /api/ingest/{queue} reply.
type ingestResponse struct {
	// Queue is the queue the daemon actually used, which is the interesting
	// field when it derived one from the pipeline name.
	Queue     string `json:"queue"`
	Queued    int    `json:"queued"`
	Dropped   int    `json:"dropped"`
	Rejected  int    `json:"rejected"`
	Triggered bool   `json:"triggered"`
}

// push sends one title to the ingest queue and asks for an immediate
// pipeline run.
func push(ctx context.Context, hc *http.Client, base, token, queue, pipeline, title string) (*ingestResponse, error) {
	body, err := json.Marshal(map[string]string{"title": title})
	if err != nil {
		return nil, err
	}
	// No queue given: let the daemon derive it from the pipeline. Both parts
	// are escaped — a queue or pipeline name is free text in the config.
	endpoint := base + "/api/ingest"
	if queue != "" {
		endpoint += "/" + neturl.PathEscape(queue)
	}
	if pipeline != "" {
		endpoint += "?pipeline=" + neturl.QueryEscape(pipeline)
	}
	url := endpoint
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
