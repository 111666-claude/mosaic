package audit

import (
	"bufio"
	"bytes"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// hlsMasterEntry is one declaration parsed from master.m3u8.
type hlsMasterEntry struct {
	uri   string
	track string // "video", "audio", or "subtitle"
}

// auditHLS verifies the HLS master playlist and every media playlist it
// declares, including subtitle playlists referenced via EXT-X-MEDIA.
func (a *auditor) auditHLS() {
	masterRel := hlsMasterName
	a.report.Manifests = append(a.report.Manifests, masterRel)
	masterBytes := a.readManifestBytes(masterRel)
	if masterBytes == nil {
		return
	}

	entries, ok := a.parseHLSMaster(masterRel, masterBytes)
	if !ok {
		return
	}

	if len(entries) == 0 {
		a.addIssue(Issue{
			Severity: SeverityError,
			Code:     CodeManifestInvalid,
			Format:   FormatHLS,
			Manifest: masterRel,
			Message:  "master playlist declares no variant streams",
		})
		return
	}
	if len(entries) > a.options.MaxRenditions {
		a.addIssue(Issue{
			Severity: SeverityError,
			Code:     CodeRenditionLimitExceeded,
			Format:   FormatHLS,
			Manifest: masterRel,
			Message:  "rendition count exceeds limit",
		})
	}

	durations := make(map[string][]float64) // track kind -> total durations

	for _, entry := range entries {
		abs, rel, escaped := a.resolveReference(masterRel, entry.uri)
		if !a.checkReferencedFile(masterRel, entry.uri, entry.track, rel, abs, escaped) {
			continue
		}
		playlistBytes := a.readManifestBytes(rel)
		if playlistBytes == nil {
			continue
		}
		a.report.Manifests = append(a.report.Manifests, rel)

		rr := a.auditHLSPlaylist(rel, playlistBytes, entry)
		a.report.Renditions = append(a.report.Renditions, rr)
		if rr.SegmentCount > 0 || rr.Duration > 0 {
			durations[entry.track] = append(durations[entry.track], rr.Duration)
		}
	}

	a.checkDurationDrift(FormatHLS, masterRel, durations)
}

var (
	hlsMediaTagRe    = regexp.MustCompile(`#EXT-X-MEDIA:.*`)
	hlsAttrURIRe     = regexp.MustCompile(`URI="([^"]*)"`)
	hlsAttrTypeRe    = regexp.MustCompile(`TYPE=([A-Z]+)`)
	hlsStreamInfRe   = regexp.MustCompile(`#EXT-X-STREAM-INF:`)
	hlsMapURIRe      = regexp.MustCompile(`URI="([^"]+)"`)
	hlsMediaSequence = regexp.MustCompile(`#EXT-X-MEDIA-SEQUENCE:(\d+)`)
)

// parseHLSMaster extracts variant and subtitle declarations from a master
// playlist, recording malformed declarations as issues.
func (a *auditor) parseHLSMaster(masterRel string, data []byte) ([]hlsMasterEntry, bool) {
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("#EXTM3U")) {
		a.addIssue(Issue{
			Severity: SeverityError,
			Code:     CodeManifestInvalid,
			Format:   FormatHLS,
			Manifest: masterRel,
			Message:  "master playlist is missing the #EXTM3U header",
		})
		return nil, false
	}

	var entries []hlsMasterEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), int(a.options.MaxManifestBytes))
	var pendingStreamInf bool

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if hlsMediaTagRe.MatchString(line) {
			entry, valid := a.parseHLSMediaTag(masterRel, line)
			if valid {
				entries = append(entries, entry)
			}
			continue
		}

		if hlsStreamInfRe.MatchString(line) {
			pendingStreamInf = true
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		// Plain URI line.
		if pendingStreamInf {
			entries = append(entries, hlsMasterEntry{
				uri:   line,
				track: "video",
			})
			pendingStreamInf = false
			continue
		}

		// A bare URI without a preceding STREAM-INF is invalid in a master.
		a.addIssue(Issue{
			Severity: SeverityError,
			Code:     CodeTrackDeclarationInvalid,
			Format:   FormatHLS,
			Manifest: masterRel,
			Path:     line,
			Message:  "master playlist contains an undeclared URI: " + line,
		})
	}
	return entries, true
}

// parseHLSMediaTag parses one #EXT-X-MEDIA declaration. Only subtitle tracks
// carry a URI in the packages Mosaic produces; audio groups without a URI are
// valid (audio is muxed into the variants) and are ignored.
func (a *auditor) parseHLSMediaTag(masterRel, line string) (hlsMasterEntry, bool) {
	typeMatch := hlsAttrTypeRe.FindStringSubmatch(line)
	uriMatch := hlsAttrURIRe.FindStringSubmatch(line)
	if typeMatch == nil {
		a.addIssue(Issue{
			Severity: SeverityError,
			Code:     CodeTrackDeclarationInvalid,
			Format:   FormatHLS,
			Manifest: masterRel,
			Message:  "EXT-X-MEDIA tag is missing TYPE attribute",
		})
		return hlsMasterEntry{}, false
	}

	mediaType := typeMatch[1]
	entry := hlsMasterEntry{}

	switch mediaType {
	case "SUBTITLES":
		entry.track = "subtitle"
		if uriMatch == nil || strings.TrimSpace(uriMatch[1]) == "" {
			a.addIssue(Issue{
				Severity: SeverityError,
				Code:     CodeTrackDeclarationInvalid,
				Format:   FormatHLS,
				Manifest: masterRel,
				Message:  "subtitle EXT-X-MEDIA declaration is missing its URI",
			})
			return hlsMasterEntry{}, false
		}
		entry.uri = uriMatch[1]
		return entry, true
	case "AUDIO", "VIDEO":
		// Rendition groups without a URI are valid; muxed audio/video lives
		// inside the variant playlists and is audited there.
		if uriMatch == nil {
			return hlsMasterEntry{}, false
		}
		entry.uri = uriMatch[1]
		entry.track = strings.ToLower(mediaType)
		return entry, true
	default:
		a.addIssue(Issue{
			Severity: SeverityError,
			Code:     CodeTrackDeclarationInvalid,
			Format:   FormatHLS,
			Manifest: masterRel,
			Message:  "unsupported EXT-X-MEDIA TYPE: " + mediaType,
		})
		return hlsMasterEntry{}, false
	}
}

// auditHLSPlaylist verifies one media playlist and returns its summary.
func (a *auditor) auditHLSPlaylist(rel string, data []byte, entry hlsMasterEntry) RenditionReport {
	rr := RenditionReport{
		Name:     filepath.Base(rel),
		Track:    entry.track,
		Manifest: rel,
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), int(a.options.MaxManifestBytes))

	var (
		targetDuration  float64
		hasHeader       bool
		hasEndList      bool
		isVOD           bool
		pendingInf      bool
		pendingInfValue float64
		pendingInfValid bool
		mediaSequence   int // explicit EXT-X-MEDIA-SEQUENCE baseline
		sequenceKnown   bool
		hasMap          bool
		seenURIs        = make(map[string]struct{})
		cursor          float64 // timeline cursor in seconds
		lastSegNum      = -1
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if line == "#EXTM3U" {
			hasHeader = true
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-TARGETDURATION:") {
			if v, err := strconv.ParseFloat(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:"), 64); err == nil {
				targetDuration = v
			}
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-PLAYLIST-TYPE:") {
			isVOD = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(line, "#EXT-X-PLAYLIST-TYPE:")), "VOD")
			continue
		}

		if line == "#EXT-X-ENDLIST" {
			hasEndList = true
			continue
		}

		if m := hlsMediaSequence.FindStringSubmatch(line); m != nil {
			if v, err := strconv.Atoi(m[1]); err == nil {
				mediaSequence = v
				sequenceKnown = true
			}
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-MAP:") {
			m := hlsMapURIRe.FindStringSubmatch(line)
			if m == nil {
				a.addIssue(Issue{
					Severity:  SeverityError,
					Code:      CodeMissingInitSegment,
					Format:    FormatHLS,
					Manifest:  rel,
					Rendition: rr.Name,
					Track:     rr.Track,
					Message:   "EXT-X-MAP tag is missing a URI",
				})
				continue
			}
			abs, segRel, escaped := a.resolveReference(rel, m[1])
			if a.checkReferencedFile(rel, rr.Name, rr.Track, segRel, abs, escaped) {
				rr.InitSegment = segRel
			}
			hasMap = true
			continue
		}

		if strings.HasPrefix(line, "#EXTINF:") {
			value := strings.TrimPrefix(line, "#EXTINF:")
			if comma := strings.IndexByte(value, ','); comma >= 0 {
				value = value[:comma]
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				a.addIssue(Issue{
					Severity:  SeverityError,
					Code:      CodeInvalidDuration,
					Format:    FormatHLS,
					Manifest:  rel,
					Rendition: rr.Name,
					Track:     rr.Track,
					Time:      cursor,
					Message:   "unparsable EXTINF duration: " + line,
				})
				pendingInf, pendingInfValid = false, false
				continue
			}
			pendingInf = true
			pendingInfValue = v
			pendingInfValid = true
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		// Plain URI: either a media segment or a subtitle payload.
		uri := line
		if entry.track == "subtitle" {
			// Subtitle playlists point at .vtt payload files, not segments.
			abs, segRel, escaped := a.resolveReference(rel, uri)
			a.checkReferencedFile(rel, rr.Name, rr.Track, segRel, abs, escaped)
			if pendingInfValid {
				rr.Duration += pendingInfValue
			}
			rr.MediaSegments = append(rr.MediaSegments, segRel)
			pendingInf, pendingInfValid = false, false
			continue
		}

		if !pendingInf {
			a.addIssue(Issue{
				Severity:  SeverityError,
				Code:      CodeManifestInvalid,
				Format:    FormatHLS,
				Manifest:  rel,
				Rendition: rr.Name,
				Track:     rr.Track,
				Path:      uri,
				Message:   "media URI without a preceding EXTINF tag: " + uri,
			})
			continue
		}

		// Duration validity: zero/negative durations and target-duration overflow.
		if pendingInfValue <= 0 {
			a.addIssue(Issue{
				Severity:  SeverityError,
				Code:      CodeInvalidDuration,
				Format:    FormatHLS,
				Manifest:  rel,
				Rendition: rr.Name,
				Track:     rr.Track,
				Time:      cursor,
				Path:      uri,
				Message:   "segment has non-positive duration",
			})
		} else if targetDuration > 0 && pendingInfValue > targetDuration+DefaultTimelineTolerance {
			a.addIssue(Issue{
				Severity:  SeverityError,
				Code:      CodeInvalidDuration,
				Format:    FormatHLS,
				Manifest:  rel,
				Rendition: rr.Name,
				Track:     rr.Track,
				Time:      cursor,
				Path:      uri,
				Message:   "segment duration exceeds target duration",
			})
		}

		// Duplicate segment references.
		if _, dup := seenURIs[uri]; dup {
			a.addIssue(Issue{
				Severity:  SeverityError,
				Code:      CodeDuplicateSegment,
				Format:    FormatHLS,
				Manifest:  rel,
				Rendition: rr.Name,
				Track:     rr.Track,
				Time:      cursor,
				Path:      uri,
				Message:   "segment URI is referenced more than once: " + uri,
			})
		}
		seenURIs[uri] = struct{}{}

		// Sequence-number continuity. In a VOD playlist every entry must be
		// adjacent: a skipped number is a deleted segment (gap), a backwards
		// number re-occupies an earlier interval (overlap). The authoritative
		// number is the segment's URI suffix, anchored by EXT-X-MEDIA-SEQUENCE
		// when the playlist declares one.
		num, hasNum := trailingSegmentNumber(uri)
		if hasNum {
			expectedNum := num
			if lastSegNum >= 0 {
				expectedNum = lastSegNum + 1
			} else if sequenceKnown {
				expectedNum = mediaSequence
			}
			if lastSegNum >= 0 || sequenceKnown {
				switch {
				case num > expectedNum:
					a.addIssue(Issue{
						Severity:  SeverityError,
						Code:      CodeTimelineGap,
						Format:    FormatHLS,
						Manifest:  rel,
						Rendition: rr.Name,
						Track:     rr.Track,
						Time:      cursor,
						Path:      uri,
						Message:   "segment numbering skips one or more segments",
					})
				case num < expectedNum:
					a.addIssue(Issue{
						Severity:  SeverityError,
						Code:      CodeTimelineOverlap,
						Format:    FormatHLS,
						Manifest:  rel,
						Rendition: rr.Name,
						Track:     rr.Track,
						Time:      cursor,
						Path:      uri,
						Message:   "segment numbering goes backwards, timeline overlaps",
					})
				}
			}
			lastSegNum = num
		}

		abs, segRel, escaped := a.resolveReference(rel, uri)
		if a.checkReferencedFile(rel, rr.Name, rr.Track, segRel, abs, escaped) {
			rr.MediaSegments = append(rr.MediaSegments, segRel)
		}
		rr.Duration += pendingInfValue
		cursor += pendingInfValue
		pendingInf, pendingInfValid = false, false
	}

	if !hasHeader {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodeManifestInvalid,
			Format:    FormatHLS,
			Manifest:  rel,
			Rendition: rr.Name,
			Track:     rr.Track,
			Message:   "media playlist is missing the #EXTM3U header",
		})
	}

	// fMP4 renditions (Mosaic default) must declare an init segment via EXT-X-MAP.
	if entry.track != "subtitle" && !hasMap {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodeMissingInitSegment,
			Format:    FormatHLS,
			Manifest:  rel,
			Rendition: rr.Name,
			Track:     rr.Track,
			Message:   "fragmented MP4 playlist does not declare an init segment",
		})
	}

	if isVOD && !hasEndList {
		a.addIssue(Issue{
			Severity:  SeverityWarning,
			Code:      CodeManifestInvalid,
			Format:    FormatHLS,
			Manifest:  rel,
			Rendition: rr.Name,
			Track:     rr.Track,
			Message:   "VOD playlist is missing #EXT-X-ENDLIST",
		})
	}

	rr.SegmentCount = len(rr.MediaSegments)
	if rr.SegmentCount == 0 {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodeEmptyRendition,
			Format:    FormatHLS,
			Manifest:  rel,
			Rendition: rr.Name,
			Track:     rr.Track,
			Message:   "rendition declares no media segments",
		})
	}
	if rr.SegmentCount > a.options.MaxSegmentsPerRendition {
		a.addIssue(Issue{
			Severity:  SeverityError,
			Code:      CodeSegmentLimitExceeded,
			Format:    FormatHLS,
			Manifest:  rel,
			Rendition: rr.Name,
			Track:     rr.Track,
			Message:   "segment count exceeds limit",
		})
	}
	return rr
}

// checkDurationDrift compares total declared durations of renditions sharing a
// track kind and reports material mismatches.
func (a *auditor) checkDurationDrift(format, manifest string, durations map[string][]float64) {
	for track, values := range durations {
		if len(values) < 2 {
			continue
		}
		min, max := values[0], values[0]
		for _, v := range values[1:] {
			min = math.Min(min, v)
			max = math.Max(max, v)
		}
		if max-min > a.options.DurationDriftTolerance {
			a.addIssue(Issue{
				Severity: SeverityWarning,
				Code:     CodeDurationMismatch,
				Format:   format,
				Manifest: manifest,
				Track:    track,
				Message:  "rendition durations drift beyond tolerance",
			})
		}
	}
}
