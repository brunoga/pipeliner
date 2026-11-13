// Package decompress provides an output plugin that extracts archives (RAR,
// ZIP, 7z) to a destination directory.
//
// RAR and ZIP are extracted in-process (tool "builtin", the default): RAR with
// github.com/nwaples/rardecode, which reads RAR 1.5 to 5 and follows a
// multi-volume set by itself, and ZIP with archive/zip. 7z needs the system
// "7z" tool. "unrar", "7z" and "unar" can still be forced for everything.
//
// An existing file is never overwritten: the builtin extractor skips it, and
// the system tools are run with their skip-existing flag. A file is written
// under a ".partial" name and renamed when complete, so an interrupted run
// leaves nothing that a later run would mistake for a finished file.
//
// Config keys:
//
//	to             - destination directory (required)
//	mirror_from    - a root the archive's directory is mapped from onto 'to'
//	keep_dirs      - preserve internal directory structure (default: true)
//	delete_archive - remove archive file(s) after successful extraction (default: false)
//	tool           - "builtin" (default), "unrar", "7z", or "unar"
package decompress

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "decompress",
		Description: "extract archive files (RAR, ZIP, 7z) to a destination directory",
		Role:        plugin.RoleSink,
		Requires:    plugin.RequireAny(entry.FieldFileLocation, entry.FieldTorrentDownloadDir),
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "to", Type: plugin.FieldTypeString, Required: true, Hint: "Destination directory for extracted files"},
			{Key: "mirror_from", Type: plugin.FieldTypeString, Hint: "Map the archive's directory, relative to this root, onto 'to' (staging/Show/Season 01 -> to/Show/Season 01)"},
			{Key: "keep_dirs", Type: plugin.FieldTypeBool, Hint: "Preserve internal directory structure (default true)"},
			{Key: "delete_archive", Type: plugin.FieldTypeBool, Hint: "Delete archive file after extraction (default false)"},
			{Key: "tool", Type: plugin.FieldTypeEnum, Enum: supportedTools, Hint: "Extractor: builtin (RAR and ZIP in-process, 7z via the 7z tool) or a system tool"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "to", "decompress"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptEnum(cfg, "tool", "decompress", supportedTools...); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "decompress", "to", "mirror_from", "keep_dirs", "delete_archive", "tool")...)
	return errs
}

// supportedTools lists the extractors; builtin is the default.
var supportedTools = []string{"builtin", "unrar", "7z", "unar"}

type decompressPlugin struct {
	to            string
	mirrorFrom    string
	keepDirs      bool
	deleteArchive bool
	tool          string // "builtin", "unrar", "7z", or "unar"
	toolPath      string // absolute path to a system tool's binary
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	to, _ := cfg["to"].(string)
	if to == "" {
		return nil, fmt.Errorf("decompress: 'to' destination directory is required")
	}
	mirrorFrom, _ := cfg["mirror_from"].(string)

	keepDirs := true
	if v, ok := cfg["keep_dirs"].(bool); ok {
		keepDirs = v
	}
	deleteArchive, _ := cfg["delete_archive"].(bool)

	tool, _ := cfg["tool"].(string)
	if tool == "" {
		tool = "builtin"
	}
	toolPath, err := resolveTool(tool)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}

	return &decompressPlugin{
		to:            filepath.Clean(to),
		mirrorFrom:    mirrorFrom,
		keepDirs:      keepDirs,
		deleteArchive: deleteArchive,
		tool:          tool,
		toolPath:      toolPath,
	}, nil
}

func (p *decompressPlugin) Name() string { return "decompress" }

func (p *decompressPlugin) Consume(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	if tc.DryRun {
		return nil
	}
	for _, e := range entries {
		if err := p.extractEntry(ctx, tc, e); err != nil {
			tc.Logger.Error("decompress: extraction failed", "entry", e.Title, "err", err)
			e.Fail(err.Error())
		}
	}
	return nil
}

// extractEntry extracts every archive set the entry points at into its
// destination. One set failing does not stop the others, but fails the entry.
func (p *decompressPlugin) extractEntry(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	root, base := sourceOf(e)
	if root == "" {
		return fmt.Errorf("no archive found on entry (need file_location, torrent_download_dir, or a URL ending in .rar/.zip/.7z)")
	}
	dest, err := p.destination(base)
	if err != nil {
		return err
	}
	archives, err := findArchives(root)
	if err != nil {
		return err
	}
	if len(archives) == 0 {
		return fmt.Errorf("no archive in %s", root)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	var errs []error
	for _, a := range archives {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, skipped, err := p.extractOne(ctx, a, dest)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(a), err))
			continue
		}
		tc.Logger.Info("decompress: extracted", "entry", e.Title, "archive", filepath.Base(a),
			"to", dest, "files", n, "skipped_existing", skipped)
		if p.deleteArchive {
			removeArchive(a)
		}
	}
	return errors.Join(errs...)
}

func (p *decompressPlugin) extractOne(ctx context.Context, archive, dest string) (written, skipped int, err error) {
	if p.tool == "builtin" {
		switch kindOf(archive) {
		case "rar":
			return extractRAR(archive, dest, p.keepDirs)
		case "zip":
			return extractZIP(archive, dest, p.keepDirs)
		}
		// 7z has no in-process reader here; the system tool does it.
		path, err := exec.LookPath("7z")
		if err != nil {
			return 0, 0, fmt.Errorf("a 7z archive needs the 7z tool on PATH")
		}
		return 0, 0, runTool(ctx, "7z", path, buildArgs("7z", archive, dest, p.keepDirs))
	}
	return 0, 0, runTool(ctx, p.tool, p.toolPath, buildArgs(p.tool, archive, dest, p.keepDirs))
}

func runTool(ctx context.Context, tool, path string, args []string) error {
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w\n%s", tool, err, stderr.String())
	}
	return nil
}

// destination is where an archive found under base is extracted. Without
// mirror_from it is 'to'. With it, base's path below mirror_from is kept
// below 'to', so staging/Show/Season 01 extracts into to/Show/Season 01; a
// base outside mirror_from is refused rather than guessed at.
func (p *decompressPlugin) destination(base string) (string, error) {
	if p.mirrorFrom == "" {
		return p.to, nil
	}
	rel, err := filepath.Rel(filepath.Clean(p.mirrorFrom), filepath.Clean(base))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is not under mirror_from %s", base, p.mirrorFrom)
	}
	return filepath.Join(p.to, rel), nil
}

// buildArgs is a system tool's command line. Every one is told to skip a file
// that exists rather than overwrite it.
func buildArgs(tool, archivePath, dest string, keepDirs bool) []string {
	switch tool {
	case "unrar":
		cmd := "e"
		if keepDirs {
			cmd = "x"
		}
		return []string{cmd, "-o-", archivePath, dest + string(filepath.Separator)}
	case "7z":
		cmd := "e"
		if keepDirs {
			cmd = "x"
		}
		return []string{cmd, "-aos", "-o" + dest, archivePath}
	case "unar":
		args := []string{archivePath, "-output-directory", dest, "-skip"}
		if !keepDirs {
			args = append(args, "-no-directory")
		}
		return args
	}
	return nil
}

// sourceOf returns what to look for archives in (a file or a directory) and
// the directory that maps to the destination under mirror_from.
//
// A torrent from torrent_session is its download directory plus its name: a
// single-file torrent is that file, a folder torrent that folder. Its base is
// the download directory, which is the path the pipeline that grabbed it
// chose ("staging/Show/Season 01"); the torrent's own folder name is the
// release's and is not carried into the library.
func sourceOf(e *entry.Entry) (root, base string) {
	if loc := e.GetString(entry.FieldFileLocation); loc != "" {
		return loc, filepath.Dir(loc)
	}
	if dir := e.GetString(entry.FieldTorrentDownloadDir); dir != "" && e.Title != "" {
		return filepath.Join(dir, e.Title), dir
	}
	lower := strings.ToLower(e.URL)
	for _, ext := range []string{".rar", ".zip", ".7z"} {
		if strings.HasSuffix(lower, ext) {
			return e.URL, filepath.Dir(e.URL)
		}
	}
	return "", ""
}

var (
	// rePartVolume is a new-style RAR volume; only part1 (part01, part001)
	// starts a set.
	rePartVolume = regexp.MustCompile(`(?i)\.part(\d+)\.rar$`)
	reOldVolume  = regexp.MustCompile(`(?i)\.r\d{2,3}$`)
	re7zVolume   = regexp.MustCompile(`(?i)\.7z\.(\d{3})$`)
)

// kindOf names an archive's format by its file name, or "" for a file that
// does not start an archive set: a later volume, a sample, anything else.
func kindOf(path string) string {
	name := strings.ToLower(filepath.Base(path))
	if m := rePartVolume.FindStringSubmatch(name); m != nil {
		if strings.TrimLeft(m[1], "0") != "1" {
			return ""
		}
		return "rar"
	}
	if m := re7zVolume.FindStringSubmatch(name); m != nil {
		if m[1] != "001" {
			return ""
		}
		return "7z"
	}
	switch filepath.Ext(name) {
	case ".rar":
		return "rar"
	case ".zip":
		return "zip"
	case ".7z":
		return "7z"
	}
	return ""
}

// findArchives lists the first volume of every archive set at root, which
// may be one file or a directory searched recursively. A season pack is often
// one RAR set per episode, each in its own folder, so one is not enough.
// Sample folders and files are skipped: they hold a clip, not the episode.
func findArchives(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if kindOf(root) == "" {
			return nil, fmt.Errorf("%s does not start an archive", root)
		}
		return []string{root}, nil
	}
	var out []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(strings.ToLower(d.Name()), "sample") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && kindOf(path) != "" {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// removeArchive deletes an archive set: the file itself, its old-style .rNN
// volumes, and the rest of a .partN.rar or .7z.NNN set.
func removeArchive(first string) {
	dir := filepath.Dir(first)
	name := filepath.Base(first)
	lower := strings.ToLower(name)
	var stem string
	switch {
	case rePartVolume.MatchString(lower):
		stem = name[:rePartVolume.FindStringIndex(lower)[0]]
	case re7zVolume.MatchString(lower):
		stem = name[:re7zVolume.FindStringIndex(lower)[0]]
	default:
		stem = strings.TrimSuffix(name, filepath.Ext(name))
	}
	siblings, _ := os.ReadDir(dir)
	for _, s := range siblings {
		n := s.Name()
		if s.IsDir() || !strings.HasPrefix(n, stem) {
			continue
		}
		rest := strings.ToLower(n[len(stem):])
		if rest == strings.ToLower(filepath.Ext(name)) || rePartVolume.MatchString(rest) ||
			re7zVolume.MatchString(rest) || reOldVolume.MatchString(rest) {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
}

// resolveTool checks a system tool is on PATH; builtin needs nothing.
func resolveTool(tool string) (string, error) {
	if !slices.Contains(supportedTools, tool) {
		return "", fmt.Errorf("unsupported tool %q; choose from: %s", tool, strings.Join(supportedTools, ", "))
	}
	if tool == "builtin" {
		return "", nil
	}
	path, err := exec.LookPath(tool)
	if err != nil {
		return "", fmt.Errorf("tool %q not found on PATH", tool)
	}
	return path, nil
}
