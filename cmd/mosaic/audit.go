package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/farshidrezaei/mosaic/audit"
)

// auditExitOK / auditExitIssues / auditExitSetup are the documented exit codes
// for `mosaic audit`.
const (
	auditExitOK     = 0
	auditExitIssues = 1
	auditExitSetup  = 2
)

// auditConfig holds the parsed `mosaic audit` flag values.
type auditConfig struct {
	dir               string
	format            string
	jsonOutput        bool
	maxManifestBytes  int64
	maxRenditions     int
	maxSegments       int
	maxIssues         int
	timelineTolerance float64
	durationTolerance float64
}

// writeOut writes to an output writer, ignoring write errors. Audit reports
// are informational and a broken pipe (e.g. piped to `head`) must not turn
// into a noisy error path; the returned exit code still reflects the findings.
func writeOut(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// runAudit executes the audit subcommand against the given arguments and
// returns the process exit code. All output goes to stdout/stderr writers so
// the function is fully testable without touching the real process state.
func runAudit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var cfg auditConfig
	fs.StringVar(&cfg.format, "format", "", "Force packaging format: hls or dash (default: auto-detect)")
	fs.BoolVar(&cfg.jsonOutput, "json", false, "Emit the report as JSON instead of text")
	fs.Int64Var(&cfg.maxManifestBytes, "max-manifest-bytes", audit.DefaultMaxManifestBytes, "Maximum accepted manifest size in bytes")
	fs.IntVar(&cfg.maxRenditions, "max-renditions", audit.DefaultMaxRenditions, "Maximum accepted rendition count")
	fs.IntVar(&cfg.maxSegments, "max-segments", audit.DefaultMaxSegmentsPerRendition, "Maximum accepted segments per rendition")
	fs.IntVar(&cfg.maxIssues, "max-issues", audit.DefaultMaxIssues, "Maximum issues collected in the report")
	fs.Float64Var(&cfg.timelineTolerance, "timeline-tolerance", audit.DefaultTimelineTolerance, "Timeline discontinuity tolerance in seconds")
	fs.Float64Var(&cfg.durationTolerance, "duration-tolerance", audit.DefaultDurationDriftTolerance, "Cross-rendition duration drift tolerance in seconds")

	fs.Usage = func() {
		writeOut(stderr, `Usage:
  mosaic audit [options] <stream-directory>

Audits an existing HLS/DASH output directory without re-encoding. Verifies
manifest references, init segments, media segments, timeline continuity, and
track declarations. Exits 0 when no errors are found (warnings are advisory),
1 when error-severity issues are found, 2 on setup failure (missing/empty
directory or no manifest).

Options:
  --format string              Force "hls" or "dash" (default: auto-detect)
  --json                       Emit JSON instead of the text report
  --max-manifest-bytes int     Manifest size limit (default 1048576)
  --max-renditions int         Rendition count limit (default 32)
  --max-segments int           Segments per rendition limit (default 10000)
  --max-issues int             Collected issue cap (default 1000)
  --timeline-tolerance float   Gap/overlap tolerance in seconds (default 0.25)
  --duration-tolerance float   Rendition drift tolerance in seconds (default 1)
`)
	}

	if err := fs.Parse(args); err != nil {
		return auditExitSetup
	}
	if fs.NArg() != 1 {
		writeOut(stderr, "Error: audit requires exactly one stream directory argument.\n")
		fs.Usage()
		return auditExitSetup
	}
	cfg.dir = fs.Arg(0)

	opts := []audit.Option{
		audit.WithFormat(cfg.format),
		audit.WithMaxManifestBytes(cfg.maxManifestBytes),
		audit.WithMaxRenditions(cfg.maxRenditions),
		audit.WithMaxSegmentsPerRendition(cfg.maxSegments),
		audit.WithMaxIssues(cfg.maxIssues),
		audit.WithTimelineTolerance(cfg.timelineTolerance),
		audit.WithDurationDriftTolerance(cfg.durationTolerance),
	}

	report, err := audit.Audit(cfg.dir, opts...)
	if err != nil {
		writeOut(stderr, "Audit setup failed: %v\n", err)
		return auditExitSetup
	}

	if cfg.jsonOutput {
		data, marshalErr := report.FormatJSON()
		if marshalErr != nil {
			writeOut(stderr, "Failed to render JSON report: %v\n", marshalErr)
			return auditExitSetup
		}
		writeOut(stdout, "%s\n", string(data))
	} else {
		writeOut(stdout, "%s", report.FormatText())
	}

	if report.HasErrors() {
		return auditExitIssues
	}
	return auditExitOK
}

// handleAudit is the CLI entry point for the audit subcommand.
func handleAudit(args []string) {
	os.Exit(runAudit(args, os.Stdout, os.Stderr))
}
