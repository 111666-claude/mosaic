package audit

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// FormatText renders a human-readable, deterministic audit report. It is the
// format used by the `mosaic audit` CLI.
func (r *Report) FormatText() string {
	var b strings.Builder

	fmt.Fprintf(&b, "Mosaic Packaging Audit\n")
	fmt.Fprintf(&b, "Format:   %s\n", strings.ToUpper(r.Format))
	fmt.Fprintf(&b, "Root:     %s\n", r.RootDir)
	fmt.Fprintf(&b, "Manifests: %d | Renditions: %d | Issues: %d (%d errors, %d warnings)\n",
		len(r.Manifests), len(r.Renditions), len(r.Issues), r.ErrorCount, r.WarningCount)
	b.WriteString("\nRenditions:\n")
	if len(r.Renditions) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, rr := range r.Renditions {
		fmt.Fprintf(&b, "  - [%s] %s: %d segment(s), %.3fs", rr.Track, rr.Name, rr.SegmentCount, rr.Duration)
		if rr.InitSegment != "" {
			fmt.Fprintf(&b, ", init=%s", rr.InitSegment)
		}
		b.WriteByte('\n')
	}

	b.WriteString("\nIssues:\n")
	if len(r.Issues) == 0 {
		b.WriteString("  none — package is consistent\n")
		return b.String()
	}
	for _, issue := range r.Issues {
		fmt.Fprintf(&b, "  [%s] %s", strings.ToUpper(string(issue.Severity)), issue.Code)
		if issue.Manifest != "" {
			fmt.Fprintf(&b, " (%s", issue.Manifest)
			if issue.Rendition != "" {
				fmt.Fprintf(&b, " #%s", issue.Rendition)
			}
			b.WriteByte(')')
		}
		if issue.Time > 0 {
			fmt.Fprintf(&b, " @%.3fs", issue.Time)
		}
		if issue.Path != "" {
			fmt.Fprintf(&b, " path=%s", issue.Path)
		}
		fmt.Fprintf(&b, ": %s\n", issue.Message)
	}
	return b.String()
}

// FormatJSON renders the report as indented JSON.
func (r *Report) FormatJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Codes returns the sorted distinct issue codes present in the report.
func (r *Report) Codes() []Code {
	seen := make(map[Code]struct{})
	var codes []Code
	for _, issue := range r.Issues {
		if _, ok := seen[issue.Code]; !ok {
			seen[issue.Code] = struct{}{}
			codes = append(codes, issue.Code)
		}
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	return codes
}
