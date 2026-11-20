package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/farshidrezaei/mosaic/audit"
)

func TestRunAudit_CleanHLS(t *testing.T) {
	root := hlsCLIFixture(t)

	var stdout, stderr bytes.Buffer
	code := runAudit([]string{root}, &stdout, &stderr)
	if code != auditExitOK {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Mosaic Packaging Audit") {
		t.Errorf("text report missing header:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "none — package is consistent") {
		t.Errorf("clean package should report no issues:\n%s", stdout.String())
	}
}

func TestRunAudit_IssuesExitOne(t *testing.T) {
	root := hlsCLIFixture(t)
	removeCLIFIxtureFile(t, root, "seg_0_1.m4s")

	var stdout, stderr bytes.Buffer
	code := runAudit([]string{root}, &stdout, &stderr)
	if code != auditExitIssues {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), string(audit.CodeMissingFile)) {
		t.Errorf("report should mention missing_file:\n%s", stdout.String())
	}
}

func TestRunAudit_SetupFailureExitTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runAudit([]string{"/nonexistent/path/xyz"}, &stdout, &stderr)
	if code != auditExitSetup {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "setup failed") {
		t.Errorf("stderr should explain setup failure: %s", stderr.String())
	}
}

func TestRunAudit_MissingArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runAudit(nil, &stdout, &stderr); code != auditExitSetup {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestRunAudit_JSONOutput(t *testing.T) {
	root := hlsCLIFixture(t)

	var stdout, stderr bytes.Buffer
	code := runAudit([]string{"--json", root}, &stdout, &stderr)
	if code != auditExitOK {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}

	var report audit.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if report.Format != audit.FormatHLS {
		t.Errorf("format = %q, want hls", report.Format)
	}
}

func TestRunAudit_ForcedFormatAndLimits(t *testing.T) {
	root := hlsCLIFixture(t)

	var stdout, stderr bytes.Buffer
	code := runAudit([]string{"--format", "hls", "--max-segments", "1", root}, &stdout, &stderr)
	if code != auditExitIssues {
		t.Fatalf("exit = %d, want 1 when segment limit exceeded", code)
	}
	if !strings.Contains(stdout.String(), string(audit.CodeSegmentLimitExceeded)) {
		t.Errorf("expected segment limit issue:\n%s", stdout.String())
	}
}

func TestRunAudit_ForcedWrongFormat(t *testing.T) {
	root := hlsCLIFixture(t)
	var stdout, stderr bytes.Buffer
	if code := runAudit([]string{"--format", "dash", root}, &stdout, &stderr); code != auditExitSetup {
		t.Fatalf("exit = %d, want 2 for format mismatch", code)
	}
}
