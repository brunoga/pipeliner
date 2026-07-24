// Command releasedocs stages the top-level README and configs/README for
// inclusion in the release archives. Both are written for GitHub browsing and
// use repo-relative links; inside a binary archive (which ships only the
// sample configs and these two READMEs) any link that points outside the
// archive — everything under plugins/ — would 404. This tool copies the two
// files into a staging directory, rewriting escaping links to absolute GitHub
// URLs pinned to the release tag while leaving in-archive links (the sample
// .star configs and the two READMEs) relative so they resolve offline.
//
// Run from the repo root by GoReleaser's before hook:
//
//	go run ./internal/tools/releasedocs -tag {{ .Tag }} -out .release-docs
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const repoBase = "https://github.com/brunoga/pipeliner/blob"

func main() {
	tag := flag.String("tag", "", "release tag to pin absolute links to (e.g. v1.13.0)")
	out := flag.String("out", ".release-docs", "staging directory to write rewritten docs into")
	flag.Parse()

	if *tag == "" {
		// Snapshot builds have no tag; fall back to main so links still resolve.
		*tag = "main"
	}

	jobs := []struct {
		src, dst, dir string
	}{
		{src: "README.md", dst: filepath.Join(*out, "README.md"), dir: "."},
		{src: filepath.Join("configs", "README.md"), dst: filepath.Join(*out, "configs", "README.md"), dir: "configs"},
	}

	for _, j := range jobs {
		raw, err := os.ReadFile(j.src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "releasedocs: read %s: %v\n", j.src, err)
			os.Exit(1)
		}
		rewritten := rewriteLinks(string(raw), j.dir, *tag)
		if err := os.MkdirAll(filepath.Dir(j.dst), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "releasedocs: mkdir %s: %v\n", filepath.Dir(j.dst), err)
			os.Exit(1)
		}
		if err := os.WriteFile(j.dst, []byte(rewritten), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "releasedocs: write %s: %v\n", j.dst, err)
			os.Exit(1)
		}
		fmt.Printf("releasedocs: staged %s -> %s\n", j.src, j.dst)
	}
}
