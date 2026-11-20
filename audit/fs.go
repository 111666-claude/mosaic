package audit

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	hlsMasterName    = "master.m3u8"
	dashManifestName = "manifest.mpd"
)

// fileExists reports whether a regular file exists directly under root.
func (a *auditor) fileExists(name string) bool {
	info, err := os.Stat(filepath.Join(a.root, name))
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// resolveReference resolves a manifest-relative URI against the directory of
// the manifest that declares it, and verifies the target stays inside the
// audited root. It returns:
//   - abs: the cleaned absolute target path;
//   - rel: the target path relative to the audited root (for stable reports);
//   - escaped: true when the reference points outside the root or uses an
//     unsafe absolute/URL/back-slash form.
//
// References with URL schemes are treated as remote and are not resolved on
// disk; callers decide whether remote references are valid in their context.
func (a *auditor) resolveReference(manifestPath, ref string) (abs, rel string, escaped bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", true
	}
	if isRemoteURI(ref) || filepath.IsAbs(ref) || strings.HasPrefix(ref, "\\") {
		return "", ref, true
	}
	if strings.Contains(ref, "\\") {
		// POSIX output must use forward slashes; backslashes are a traversal smell.
		return "", ref, true
	}

	manifestDir := filepath.Dir(manifestPath)
	abs = filepath.Clean(filepath.Join(a.root, manifestDir, filepath.FromSlash(ref)))
	rel, err := filepath.Rel(a.root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ref, true
	}
	return abs, filepath.ToSlash(rel), false
}

// isRemoteURI reports whether ref uses an absolute URL scheme (http, https, s3, ...).
func isRemoteURI(ref string) bool {
	// A scheme is alpha-leading and separated by ':' before any '/'.
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		if c == ':' {
			return i > 0
		}
		if c == '/' || c == '?' || c == '#' {
			return false
		}
	}
	return false
}

// checkReferencedFile marks rel as referenced and verifies the target exists
// and is non-empty. It returns true when the file is present and non-empty.
func (a *auditor) checkReferencedFile(manifest, rendition, track, rel, abs string, escaped bool) bool {
	if escaped {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodePathEscape,
			Format:    a.report.Format,
			Manifest:  manifest,
			Rendition: rendition,
			Track:     track,
			Path:      rel,
			Message:   "reference escapes the output directory or uses an unsupported absolute form: " + rel,
		})
		return false
	}

	a.referenced[abs] = struct{}{}

	info, err := os.Stat(abs)
	if err != nil {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodeMissingFile,
			Format:    a.report.Format,
			Manifest:  manifest,
			Rendition: rendition,
			Track:     track,
			Path:      rel,
			Message:   "referenced file does not exist: " + rel,
		})
		return false
	}
	if info.IsDir() {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodeMissingFile,
			Format:    a.report.Format,
			Manifest:  manifest,
			Rendition: rendition,
			Track:     track,
			Path:      rel,
			Message:   "referenced path is a directory, not a file: " + rel,
		})
		return false
	}
	if info.Size() == 0 {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodeEmptyFile,
			Format:    a.report.Format,
			Manifest:  manifest,
			Rendition: rendition,
			Track:     track,
			Path:      rel,
			Message:   "referenced file is empty: " + rel,
		})
		return false
	}
	return true
}

// readManifestBytes enforces the manifest size limit before reading a file.
// A too-large or unreadable manifest is recorded as an issue and nil is
// returned so callers skip it without aborting the whole audit.
func (a *auditor) readManifestBytes(rel string) []byte {
	abs := filepath.Join(a.root, filepath.FromSlash(rel))
	info, err := os.Stat(abs)
	if err != nil {
		a.addIssue(Issue{
			Severity: SeverityError,
			Code:     CodeManifestUnreadable,
			Format:   a.report.Format,
			Manifest: rel,
			Path:     rel,
			Message:  "manifest cannot be read: " + err.Error(),
		})
		return nil
	}
	if info.Size() > a.options.MaxManifestBytes {
		a.addIssue(Issue{
			Severity: SeverityError,
			Code:     CodeManifestTooLarge,
			Format:   a.report.Format,
			Manifest: rel,
			Path:     rel,
			Message:  "manifest exceeds size limit",
		})
		return nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		a.addIssue(Issue{
			Severity: SeverityError,
			Code:     CodeManifestUnreadable,
			Format:   a.report.Format,
			Manifest: rel,
			Path:     rel,
			Message:  "manifest cannot be read: " + err.Error(),
		})
		return nil
	}
	if int64(len(data)) > a.options.MaxManifestBytes {
		a.addIssue(Issue{
			Severity: SeverityError,
			Code:     CodeManifestTooLarge,
			Format:   a.report.Format,
			Manifest: rel,
			Path:     rel,
			Message:  "manifest exceeds size limit",
		})
		return nil
	}
	a.referenced[abs] = struct{}{}
	return data
}

// collectDiskFiles walks the root once and records every regular file's
// absolute path, so unreferenced leftovers can be detected after manifest
// analysis. Walk errors on individual entries do not abort the audit.
func (a *auditor) collectDiskFiles() {
	_ = filepath.WalkDir(a.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if abs, e := filepath.Abs(path); e == nil {
			a.seenFiles[abs] = struct{}{}
		}
		return nil
	})
}

// checkUnreferenced flags media-like files on disk that no manifest references.
// Manifests and playlists themselves are excluded: only media container
// extensions count, which keeps source/sidecar files from producing noise.
func (a *auditor) checkUnreferenced() {
	a.collectDiskFiles()

	var leftovers []string
	for abs := range a.seenFiles {
		if _, ok := a.referenced[abs]; ok {
			continue
		}
		rel, err := filepath.Rel(a.root, abs)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if !isMediaFile(rel) {
			continue
		}
		leftovers = append(leftovers, rel)
	}
	sort.Strings(leftovers)
	for _, rel := range leftovers {
		a.addIssue(Issue{
			Severity: SeverityWarning,
			Code:     CodeUnreferencedSegment,
			Format:   a.report.Format,
			Path:     rel,
			Message:  "media file on disk is not referenced by any manifest: " + rel,
		})
	}
}

// isMediaFile reports whether a relative path looks like a packaged media
// segment, init segment, or subtitle payload.
func isMediaFile(rel string) bool {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".m4s", ".mp4", ".ts", ".m4a", ".vtt", ".aac", ".webm", ".mkv":
		return true
	default:
		return false
	}
}
