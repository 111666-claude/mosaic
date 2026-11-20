package audit

import (
	"strconv"
	"strings"
)

// auditDASH verifies manifest.mpd: AdaptationSet/Representation declarations,
// initialization segments, SegmentTemplate media segments, timeline
// continuity, and subtitle BaseURL payloads.
func (a *auditor) auditDASH() {
	mpdRel := dashManifestName
	a.report.Manifests = append(a.report.Manifests, mpdRel)
	data := a.readManifestBytes(mpdRel)
	if data == nil {
		return
	}

	manifest, err := parseDASHManifest(data)
	if err != nil {
		a.addIssue(Issue{
			Severity: SeverityError,
			Code:     CodeManifestInvalid,
			Format:   FormatDASH,
			Manifest: mpdRel,
			Message:  err.Error(),
		})
		return
	}

	// Rendition limit counts every Representation, including text tracks.
	repCount := 0
	for _, set := range manifest.adaptationSets {
		repCount += len(set.representations)
	}
	if repCount > a.options.MaxRenditions {
		a.addIssue(Issue{
			Severity: SeverityError,
			Code:     CodeRenditionLimitExceeded,
			Format:   FormatDASH,
			Manifest: mpdRel,
			Message:  "representation count exceeds limit",
		})
	}

	durations := make(map[string][]float64)

	for _, set := range manifest.adaptationSets {
		track := dashTrackKind(set)
		if track == "" {
			a.addIssue(Issue{
				Severity: SeverityError,
				Code:     CodeTrackDeclarationInvalid,
				Format:   FormatDASH,
				Manifest: mpdRel,
				Message:  "AdaptationSet declares an unknown or empty content type",
			})
			continue
		}

		if len(set.representations) == 0 && track != "subtitle" {
			a.addIssue(Issue{
				Severity: SeverityError,
				Code:     CodeEmptyRendition,
				Format:   FormatDASH,
				Manifest: mpdRel,
				Track:    track,
				Message:  "AdaptationSet contains no Representation",
			})
			continue
		}

		for _, rep := range set.representations {
			rr := RenditionReport{
				Name:     rep.id,
				Track:    track,
				Manifest: mpdRel,
			}

			if track == "subtitle" {
				a.auditDASHSubtitle(mpdRel, set, rep, &rr)
			} else {
				a.auditDASHRepresentation(mpdRel, set, rep, &rr)
			}

			a.report.Renditions = append(a.report.Renditions, rr)
			if rr.Duration > 0 {
				durations[track] = append(durations[track], rr.Duration)
			}
		}
	}

	a.checkDurationDrift(FormatDASH, mpdRel, durations)
	a.checkPresentationDuration(mpdRel, manifest, durations)
}

// checkPresentationDuration compares the MPD's declared
// mediaPresentationDuration against the durations derived from each track's
// SegmentTimeline. A malformed attribute is an error; a material mismatch is a
// warning because players normally derive playback range from the timeline.
func (a *auditor) checkPresentationDuration(mpdRel string, manifest *dashManifest, durations map[string][]float64) {
	if strings.TrimSpace(manifest.presentationDuration) == "" {
		return
	}
	declared, ok := parseISO8601Duration(manifest.presentationDuration)
	if !ok {
		a.addIssue(Issue{
			Severity: SeverityError,
			Code:     CodeManifestInvalid,
			Format:   FormatDASH,
			Manifest: mpdRel,
			Message:  "mediaPresentationDuration is not a valid ISO 8601 duration: " + manifest.presentationDuration,
		})
		return
	}
	for track, values := range durations {
		if len(values) == 0 {
			continue
		}
		// Compare against the longest rendition of each track.
		max := values[0]
		for _, v := range values[1:] {
			if v > max {
				max = v
			}
		}
		if d := declared - max; d > a.options.DurationDriftTolerance || d < -a.options.DurationDriftTolerance {
			a.addIssue(Issue{
				Severity: SeverityWarning,
				Code:     CodeDurationMismatch,
				Format:   FormatDASH,
				Manifest: mpdRel,
				Track:    track,
				Time:     max,
				Message:  "mediaPresentationDuration does not match the segment timeline",
			})
		}
	}
}

// dashTrackKind normalizes an AdaptationSet into video/audio/subtitle.
func dashTrackKind(set dashAdaptationSet) string {
	switch set.contentType {
	case "video", "audio":
		return set.contentType
	case "text":
		return "subtitle"
	}
	mime := strings.ToLower(set.mimeType)
	switch {
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case strings.HasPrefix(mime, "text/") || strings.Contains(mime, "vtt"):
		return "subtitle"
	}
	return ""
}

// auditDASHSubtitle verifies a text AdaptationSet representation, which in
// Mosaic output references its WebVTT payload through a BaseURL element.
func (a *auditor) auditDASHSubtitle(mpdRel string, set dashAdaptationSet, rep dashRepresentation, rr *RenditionReport) {
	baseURLs := append(append([]string{}, set.baseURLs...), rep.baseURLs...)
	if len(baseURLs) == 0 {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodeTrackDeclarationInvalid,
			Format:    FormatDASH,
			Manifest:  mpdRel,
			Rendition: rep.id,
			Track:     "subtitle",
			Message:   "subtitle representation does not declare a BaseURL payload",
		})
		return
	}
	for _, u := range baseURLs {
		abs, rel, escaped := a.resolveReference(mpdRel, u)
		if a.checkReferencedFile(mpdRel, rep.id, "subtitle", rel, abs, escaped) {
			rr.MediaSegments = append(rr.MediaSegments, rel)
		}
	}
	rr.SegmentCount = len(rr.MediaSegments)
}

// auditDASHRepresentation verifies one video/audio representation end to end.
func (a *auditor) auditDASHRepresentation(mpdRel string, set dashAdaptationSet, rep dashRepresentation, rr *RenditionReport) {
	tmpl := rep.template
	if tmpl == nil {
		tmpl = set.template
	}
	if tmpl == nil {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodeManifestInvalid,
			Format:    FormatDASH,
			Manifest:  mpdRel,
			Rendition: rep.id,
			Track:     rr.Track,
			Message:   "representation has no SegmentTemplate",
		})
		return
	}
	if tmpl.initialization == "" {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodeMissingInitSegment,
			Format:    FormatDASH,
			Manifest:  mpdRel,
			Rendition: rep.id,
			Track:     rr.Track,
			Message:   "SegmentTemplate declares no initialization segment",
		})
	} else {
		initURI, ok1 := expandTemplate(tmpl.initialization, rep.id, 0, 0)
		if !ok1 {
			a.addIssue(Issue{
				Severity:  SeverityError,
				Code:      CodeManifestInvalid,
				Format:    FormatDASH,
				Manifest:  mpdRel,
				Rendition: rep.id,
				Track:     rr.Track,
				Message:   "initialization template contains unsupported tokens",
			})
		} else {
			abs, rel, escaped := a.resolveReference(mpdRel, initURI)
			if a.checkReferencedFile(mpdRel, rep.id, rr.Track, rel, abs, escaped) {
				rr.InitSegment = rel
			}
		}
	}

	if tmpl.media == "" {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodeManifestInvalid,
			Format:    FormatDASH,
			Manifest:  mpdRel,
			Rendition: rep.id,
			Track:     rr.Track,
			Message:   "SegmentTemplate declares no media pattern",
		})
		return
	}

	if len(tmpl.timeline) > 0 {
		a.auditDASHTimeline(mpdRel, rep, rr, tmpl)
	} else {
		a.addIssue(Issue{
			Severity:  SeverityWarning,
			Code:      CodeManifestInvalid,
			Format:    FormatDASH,
			Manifest:  mpdRel,
			Rendition: rep.id,
			Track:     rr.Track,
			Message:   "SegmentTemplate has no SegmentTimeline; segment existence cannot be verified",
		})
	}
}

// auditDASHTimeline expands a SegmentTimeline into concrete media segments,
// verifies each file, and checks entry-to-entry continuity.
func (a *auditor) auditDASHTimeline(mpdRel string, rep dashRepresentation, rr *RenditionReport, tmpl *dashSegmentTemplate) {
	var (
		seenURIs = make(map[string]struct{})
		number   = tmpl.startNumber
		cursor   int64 // timeline position in timescale units
		tolUnits = a.options.TimelineTolerance * tmpl.timescale
	)

	for entryIdx, entry := range tmpl.timeline {
		if entry.r < 0 {
			a.addIssue(Issue{
				Severity:  SeverityError,
				Code:      CodeManifestInvalid,
				Format:    FormatDASH,
				Manifest:  mpdRel,
				Rendition: rep.id,
				Track:     rr.Track,
				Message:   "open-ended SegmentTimeline repeat is not supported in VOD packages",
			})
			return
		}

		if entryIdx > 0 {
			// An explicit t on a later entry must continue the timeline. When
			// t is omitted the entry implicitly starts at the previous cursor.
			start := entry.t
			if start == 0 {
				start = cursor
			}
			gap := start - cursor
			if gap > int64(tolUnits) {
				a.addIssue(Issue{
					Severity:  SeverityError,
					Code:      CodeTimelineGap,
					Format:    FormatDASH,
					Manifest:  mpdRel,
					Rendition: rep.id,
					Track:     rr.Track,
					Time:      float64(cursor) / tmpl.timescale,
					Message:   "SegmentTimeline leaves a gap before a later entry",
				})
			} else if gap < -int64(tolUnits) {
				a.addIssue(Issue{
					Severity:  SeverityError,
					Code:      CodeTimelineOverlap,
					Format:    FormatDASH,
					Manifest:  mpdRel,
					Rendition: rep.id,
					Track:     rr.Track,
					Time:      float64(start) / tmpl.timescale,
					Message:   "SegmentTimeline entry starts before the previous entry ends",
				})
			}
			cursor = start
		} else {
			cursor = entry.t
		}

		repeats := entry.r + 1
		for k := 0; k < repeats; k++ {
			uri, ok := expandTemplate(tmpl.media, rep.id, number, cursor)
			if !ok {
				a.addIssue(Issue{
					Severity:  SeverityError,
					Code:      CodeManifestInvalid,
					Format:    FormatDASH,
					Manifest:  mpdRel,
					Rendition: rep.id,
					Track:     rr.Track,
					Time:      float64(cursor) / tmpl.timescale,
					Message:   "media template contains unsupported tokens",
				})
				return
			}

			if _, dup := seenURIs[uri]; dup {
				a.addIssue(Issue{
					Severity:  SeverityError,
					Code:      CodeDuplicateSegment,
					Format:    FormatDASH,
					Manifest:  mpdRel,
					Rendition: rep.id,
					Track:     rr.Track,
					Time:      float64(cursor) / tmpl.timescale,
					Path:      uri,
					Message:   "segment URI is generated more than once: " + uri,
				})
			}
			seenURIs[uri] = struct{}{}

			durSeconds := float64(entry.d) / tmpl.timescale
			if durSeconds <= 0 {
				a.addIssue(Issue{
					Severity:  SeverityError,
					Code:      CodeInvalidDuration,
					Format:    FormatDASH,
					Manifest:  mpdRel,
					Rendition: rep.id,
					Track:     rr.Track,
					Time:      float64(cursor) / tmpl.timescale,
					Path:      uri,
					Message:   "segment has non-positive duration",
				})
			}

			abs, rel, escaped := a.resolveReference(mpdRel, uri)
			if a.checkReferencedFile(mpdRel, rep.id, rr.Track, rel, abs, escaped) {
				rr.MediaSegments = append(rr.MediaSegments, rel)
			}
			rr.Duration += durSeconds

			cursor += entry.d
			number++
		}
	}

	rr.SegmentCount = len(rr.MediaSegments)
	if rr.SegmentCount == 0 {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodeEmptyRendition,
			Format:    FormatDASH,
			Manifest:  mpdRel,
			Rendition: rep.id,
			Track:     rr.Track,
			Message:   "representation declares no media segments",
		})
	}
	if rr.SegmentCount > a.options.MaxSegmentsPerRendition {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodeSegmentLimitExceeded,
			Format:    FormatDASH,
			Manifest:  mpdRel,
			Rendition: rep.id,
			Track:     rr.Track,
			Message:   "segment count exceeds limit",
		})
	}
}

// expandTemplate substitutes the DASH template tokens $RepresentationID$,
// $Number$ and $Time$, and honors the $$ escape. It returns false when an
// unknown $-token is present.
func expandTemplate(pattern, repID string, number int, timeUnits int64) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c != '$' {
			b.WriteByte(c)
			continue
		}
		rest := pattern[i+1:]
		switch {
		case strings.HasPrefix(rest, "$"):
			b.WriteByte('$')
			i++
		case strings.HasPrefix(rest, "RepresentationID$"):
			b.WriteString(repID)
			i += len("RepresentationID$")
		case strings.HasPrefix(rest, "Number$"):
			b.WriteString(strconv.Itoa(number))
			i += len("Number$")
		case strings.HasPrefix(rest, "Time$"):
			b.WriteString(strconv.FormatInt(timeUnits, 10))
			i += len("Time$")
		default:
			return "", false
		}
	}
	return b.String(), true
}
