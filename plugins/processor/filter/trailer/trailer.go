// Package trailer provides a filter plugin that detects trailers, teasers and
// similar short-form releases in entry titles and either rejects them (default)
// or keeps only them.
package trailer

import (
	"context"
	"fmt"
	"regexp"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

const (
	modeReject = "reject"
	modeAccept = "accept"
)

// reTrailer matches release-title tokens that identify a trailer or other
// short-form clip. Word boundaries keep movies whose own title contains words
// like "Greatest" or "Featuring" from triggering. The separator class allows
// the dot/space/dash/underscore characters scene releases use between tokens.
var reTrailer = regexp.MustCompile(`(?i)\b(trailer|teaser|sneak[\s\.\-_]?peek|featurette|behind[\s\.\-_]?the[\s\.\-_]?scenes|BTS)\b`)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "trailer",
		Description: "detect trailer/teaser/featurette entries and accept or reject them",
		Role:        plugin.RoleProcessor,
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{
				Key:     "mode",
				Type:    plugin.FieldTypeEnum,
				Enum:    []string{modeReject, modeAccept},
				Default: modeReject,
				Hint:    "What to do with trailers: 'reject' drops them, 'accept' keeps only them",
			},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptEnum(cfg, "mode", "trailer", modeReject, modeAccept); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "trailer", "mode")...)
	return errs
}

type trailerPlugin struct {
	mode string
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	mode, _ := cfg["mode"].(string)
	if mode == "" {
		mode = modeReject
	}
	return &trailerPlugin{mode: mode}, nil
}

func (p *trailerPlugin) Name() string { return "trailer" }

func (p *trailerPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		isTrailer := reTrailer.MatchString(e.Title)
		switch p.mode {
		case modeReject:
			if isTrailer {
				e.Reject(fmt.Sprintf("trailer detected in title: %q", e.Title))
			}
		case modeAccept:
			if isTrailer {
				e.Accept("trailer")
			} else {
				e.Reject("not a trailer")
			}
		}
	}
	return entry.PassThrough(entries), nil
}
