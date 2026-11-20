package audit

import (
	"strconv"
	"strings"
)

// trailingSegmentNumber extracts the final numeric component of a segment URI,
// e.g. "seg_0_2.m4s" -> 2 and "chunk-stream1-3.m4s" -> 3. It returns false
// when the file name does not end in a numeric token before its extension.
func trailingSegmentNumber(uri string) (int, bool) {
	name := uri
	if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
		name = name[slash+1:]
	}
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		name = name[:dot]
	}
	end := len(name)
	start := end
	for start > 0 {
		c := name[start-1]
		if c < '0' || c > '9' {
			break
		}
		start--
	}
	if start == end {
		return 0, false
	}
	n, err := strconv.Atoi(name[start:end])
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseISO8601Duration parses the subset of ISO 8601 durations used by DASH
// manifests: P[n]DT[n]H[n]M[n]S, with optional fractional seconds/minutes.
// It returns the duration in seconds.
func parseISO8601Duration(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != 'P' {
		return 0, false
	}

	var total float64
	var current strings.Builder
	inTime := false
	valid := false

	flush := func(unit byte) bool {
		if current.Len() == 0 {
			return false
		}
		n, err := strconv.ParseFloat(current.String(), 64)
		if err != nil {
			return false
		}
		current.Reset()
		switch unit {
		case 'D':
			total += n * 86400
		case 'H':
			total += n * 3600
		case 'M':
			if inTime {
				total += n * 60
			} else {
				return false
			}
		case 'S':
			total += n
		default:
			return false
		}
		valid = true
		return true
	}

	for i := 1; i < len(value); i++ {
		c := value[i]
		switch {
		case c == 'T':
			inTime = true
		case c >= '0' && c <= '9', c == '.':
			current.WriteByte(c)
		case c == 'D', c == 'H', c == 'M', c == 'S':
			if !flush(c) {
				return 0, false
			}
		default:
			return 0, false
		}
	}
	if current.Len() > 0 {
		return 0, false
	}
	if !valid {
		return 0, false
	}
	return total, true
}
