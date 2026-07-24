package main

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// mdLink matches an inline markdown link's target: the (...) part of [text](target).
// Titles ([text](target "title")) and reference-style links are not used by the
// two READMEs this tool handles, so a simple form suffices.
var mdLink = regexp.MustCompile(`\]\(([^)]+)\)`)

// rewriteLinks rewrites repo-relative markdown links in content that would
// escape the release archive into absolute GitHub URLs at tag. fileDir is the
// file's directory relative to the repo root ("." for the top README,
// "configs" for configs/README.md). Links that resolve to a file also present
// in the archive — the sample .star configs and the two bundled READMEs — are
// left relative so they resolve inside the extracted archive and offline.
func rewriteLinks(content, fileDir, tag string) string {
	return mdLink.ReplaceAllStringFunc(content, func(m string) string {
		target := m[2 : len(m)-1] // strip "](" and ")"
		rewritten := rewriteTarget(target, fileDir, tag)
		if rewritten == target {
			return m
		}
		return "](" + rewritten + ")"
	})
}

// rewriteTarget returns the archive-safe form of a single link target.
func rewriteTarget(target, fileDir, tag string) string {
	// Leave anchors, absolute URLs, mailto:, and protocol-relative links alone.
	if target == "" || strings.HasPrefix(target, "#") ||
		strings.HasPrefix(target, "//") || strings.Contains(target, "://") ||
		strings.HasPrefix(target, "mailto:") {
		return target
	}

	// Split off any in-page anchor so it survives the rewrite.
	link, anchor := target, ""
	if i := strings.IndexByte(target, '#'); i >= 0 {
		link, anchor = target[:i], target[i:]
	}

	resolved := path.Clean(path.Join(fileDir, link))
	if inArchive(resolved) {
		return target // resolves inside the extracted archive — keep relative
	}
	return fmt.Sprintf("%s/%s/%s%s", repoBase, tag, resolved, anchor)
}

// inArchive reports whether a repo-relative path is one of the files bundled
// into the release archives: the two READMEs and the sample .star configs.
func inArchive(p string) bool {
	switch p {
	case "README.md", "configs/README.md":
		return true
	}
	return path.Dir(p) == "configs" && path.Ext(p) == ".star"
}
