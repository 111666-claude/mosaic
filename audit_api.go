package mosaic

import (
	"github.com/farshidrezaei/mosaic/audit"
)

// AuditReport is the result of auditing an existing HLS/DASH output directory.
// It is an alias of audit.Report so the full report API stays in the audit
// package while remaining reachable from the root mosaic package.
type AuditReport = audit.Report

// AuditIssue describes one packaging defect found during an audit.
type AuditIssue = audit.Issue

// AuditOptions configures a packaging audit.
type AuditOptions = audit.Options

// AuditOption customizes a packaging audit.
type AuditOption = audit.Option

// Re-exported audit functional options.
var (
	// AuditWithFormat forces the packaging format ("hls" or "dash").
	AuditWithFormat = audit.WithFormat
	// AuditWithMaxManifestBytes overrides the per-manifest size limit.
	AuditWithMaxManifestBytes = audit.WithMaxManifestBytes
	// AuditWithMaxRenditions overrides the rendition count limit.
	AuditWithMaxRenditions = audit.WithMaxRenditions
	// AuditWithMaxSegmentsPerRendition overrides the per-rendition segment limit.
	AuditWithMaxSegmentsPerRendition = audit.WithMaxSegmentsPerRendition
	// AuditWithMaxIssues overrides the issue collection cap.
	AuditWithMaxIssues = audit.WithMaxIssues
	// AuditWithTimelineTolerance overrides the timeline discontinuity tolerance.
	AuditWithTimelineTolerance = audit.WithTimelineTolerance
	// AuditWithDurationDriftTolerance overrides the cross-rendition drift tolerance.
	AuditWithDurationDriftTolerance = audit.WithDurationDriftTolerance
)

// AuditPackage inspects an existing HLS or DASH output directory without
// re-encoding or modifying anything. It verifies manifest references,
// initialization segments, media segments, timeline continuity, and track
// declarations, returning a deterministically ordered report.
//
// Setup failures (missing/empty directory, no recognizable manifest) return
// the audit package's sentinel errors, accessible via errors.Is.
func AuditPackage(rootDir string, opts ...AuditOption) (*AuditReport, error) {
	return audit.Audit(rootDir, opts...)
}
