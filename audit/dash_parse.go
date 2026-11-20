package audit

import (
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

// localName strips an XML namespace prefix, e.g. "dash:MPD" -> "MPD".
func localName(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// parseDASHManifest tokenizes and projects an MPD into dashManifest. It uses
// the stdlib decoder rather than struct unmarshalling so that unknown
// namespaces and attribute ordering never get in the way, and so malformed
// documents can be reported with precise errors.
func parseDASHManifest(data []byte) (*dashManifest, error) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = true

	manifest := &dashManifest{}
	var set *dashAdaptationSet
	var rep *dashRepresentation
	var templateTarget *dashSegmentTemplate // where the next parsed S elements belong

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			name := localName(t.Name.Local)
			attr := make(map[string]string, len(t.Attr))
			for _, a := range t.Attr {
				attr[localName(a.Name.Local)] = a.Value
			}

			switch name {
			case "MPD":
				manifest.presentationDuration = attr["mediaPresentationDuration"]

			case "AdaptationSet":
				set = &dashAdaptationSet{
					id:          attr["id"],
					contentType: strings.ToLower(attr["contentType"]),
					mimeType:    attr["mimeType"],
				}
				templateTarget = set.template
				manifest.adaptationSets = append(manifest.adaptationSets, *set)

			case "Representation":
				if set == nil {
					return nil, malformed("Representation outside AdaptationSet")
				}
				rep = &dashRepresentation{id: attr["id"]}

			case "BaseURL":
				text, err := decodedText(dec)
				if err != nil {
					return nil, err
				}
				switch {
				case rep != nil:
					rep.baseURLs = append(rep.baseURLs, text)
				case set != nil:
					set.baseURLs = append(set.baseURLs, text)
				}

			case "SegmentTemplate":
				tmpl := parseSegmentTemplate(attr)
				templateTarget = tmpl
				if rep != nil {
					rep.template = tmpl
				} else if set != nil {
					set.template = tmpl
				}
			case "S":
				if templateTarget == nil {
					return nil, malformed("SegmentTimeline S outside SegmentTemplate")
				}
				entry := dashTimelineEntry{r: 0}
				if v, ok := attr["t"]; ok {
					n, err := strconv.ParseInt(v, 10, 64)
					if err != nil {
						return nil, malformed("invalid SegmentTimeline t attribute")
					}
					entry.t = n
				}
				n, err := strconv.ParseInt(attr["d"], 10, 64)
				if err != nil || n <= 0 {
					return nil, malformed("invalid SegmentTimeline d attribute")
				}
				entry.d = n
				if v, ok := attr["r"]; ok {
					rv, err := strconv.Atoi(v)
					if err != nil {
						return nil, malformed("invalid SegmentTimeline r attribute")
					}
					entry.r = rv
				}
				templateTarget.timeline = append(templateTarget.timeline, entry)
			}

		case xml.EndElement:
			name := localName(t.Name.Local)
			switch name {
			case "AdaptationSet":
				set = nil
				rep = nil
				templateTarget = nil
			case "Representation":
				if set != nil && rep != nil {
					idx := len(manifest.adaptationSets) - 1
					manifest.adaptationSets[idx].representations = append(
						manifest.adaptationSets[idx].representations, *rep)
				}
				rep = nil
			}
		}
	}

	if len(manifest.adaptationSets) == 0 {
		return nil, malformed("MPD contains no AdaptationSet")
	}
	return manifest, nil
}

// parseSegmentTemplate builds a dashSegmentTemplate from StartElement attributes.
func parseSegmentTemplate(attr map[string]string) *dashSegmentTemplate {
	tmpl := &dashSegmentTemplate{
		timescale:      1,
		initialization: attr["initialization"],
		media:          attr["media"],
		startNumber:    1,
	}
	if v, ok := attr["timescale"]; ok {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			tmpl.timescale = n
		}
	}
	if v, ok := attr["startNumber"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tmpl.startNumber = n
		}
	}
	return tmpl
}

// decodedText consumes the character data of the current element.
func decodedText(dec *xml.Decoder) (string, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			return strings.TrimSpace(string(t)), nil
		case xml.EndElement:
			return "", nil
		}
	}
}

type parseError struct{ msg string }

func (e *parseError) Error() string { return e.msg }

func malformed(msg string) error { return &parseError{msg: "malformed MPD: " + msg} }
