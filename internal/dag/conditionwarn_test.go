package dag

import (
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/plugin"
)

func TestConditionMissingRejectWarning(t *testing.T) {
	cd := &plugin.Descriptor{PluginName: "condition"}
	other := &plugin.Descriptor{PluginName: "metainfo_file"}

	cases := []struct {
		name   string
		d      *plugin.Descriptor
		cfg    map[string]any
		wantOK bool // true = no warning
	}{
		{"non_condition_plugin_no_warn", other, map[string]any{"accept": "x"}, true},
		{"nil_descriptor_no_warn", nil, map[string]any{"accept": "x"}, true},
		{"empty_cfg_warns", cd, map[string]any{}, false},
		{"accept_only_warns", cd, map[string]any{"accept": "bluray_3d_release == \"true\""}, false},
		{"reject_only_ok", cd, map[string]any{"reject": "video_rating < 6.5"}, true},
		{"accept_plus_reject_ok", cd, map[string]any{"accept": "video_rating >= 7.0", "reject": "video_rating < 6.5"}, true},
		{"rules_accept_only_warns", cd, map[string]any{
			"rules": []any{
				map[string]any{"accept": "video_rating >= 7.0"},
			},
		}, false},
		{"rules_with_reject_ok", cd, map[string]any{
			"rules": []any{
				map[string]any{"reject": "video_source == \"CAM\""},
				map[string]any{"accept": "video_rating >= 7.0"},
			},
		}, true},
		{"rules_catch_all_reject_ok", cd, map[string]any{
			"rules": []any{
				map[string]any{"accept": "bluray_3d_release == \"true\""},
				map[string]any{"reject": "true"},
			},
		}, true},
		// A tautological accept has no non-matching entries, so the advice
		// to add a reject rule is moot. This is the "the entry reaching
		// this node is itself the decision" idiom behind a webhook.
		{"accept_true_ok", cd, map[string]any{"accept": "true"}, true},
		{"accept_true_uppercase_ok", cd, map[string]any{"accept": "TRUE"}, true},
		{"accept_true_parens_ok", cd, map[string]any{"accept": "(true)"}, true},
		{"accept_true_padded_ok", cd, map[string]any{"accept": "  true  "}, true},
		{"accept_nonzero_number_ok", cd, map[string]any{"accept": "1"}, true},
		{"rules_accept_true_ok", cd, map[string]any{
			"rules": []any{
				map[string]any{"accept": "true"},
			},
		}, true},
		// accept="false" matches nothing, so entries really do leak through
		// as Undecided — the warning is correct there.
		{"accept_false_warns", cd, map[string]any{"accept": "false"}, false},
		{"accept_zero_warns", cd, map[string]any{"accept": "0"}, false},
		// Only a literal counts. A tautology the parser would have to reason
		// about still warns; a false negative leaves the advice in place.
		{"accept_tautology_expr_warns", cd, map[string]any{"accept": "1 == 1"}, false},
		// An unparseable accept is reported by the expression validator; this
		// warning does not get suppressed by it.
		{"accept_unparseable_warns", cd, map[string]any{"accept": "x =="}, false},
		// rules present empty → still warns (no reject anywhere)
		{"rules_empty_warns", cd, map[string]any{"rules": []any{}}, false},
		// When rules is set, top-level reject is IGNORED by the condition plugin
		// (rules takes precedence) — so a top-level reject alongside a rules
		// list of accept-only items should still warn, matching runtime behavior.
		{"rules_supersedes_toplevel_reject_warns", cd, map[string]any{
			"reject": "video_rating < 6.5",
			"rules": []any{
				map[string]any{"accept": "video_rating >= 7.0"},
			},
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := &Node{ID: "cond_1", PluginName: "condition", Config: tc.cfg}
			got := conditionMissingRejectWarning(n, tc.d)
			if tc.wantOK {
				if got != nil {
					t.Errorf("want no warning, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want warning, got nil")
			}
			msg := got.Error()
			for _, want := range []string{"cond_1", "condition", "accept-only", "reject"} {
				if !strings.Contains(msg, want) {
					t.Errorf("warning %q missing substring %q", msg, want)
				}
			}
		})
	}
}
