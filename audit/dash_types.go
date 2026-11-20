package audit

// dashManifest is the minimal parsed view of an MPD needed for auditing.
type dashManifest struct {
	// presentationDuration is the raw mediaPresentationDuration attribute,
	// empty when the MPD omits it.
	presentationDuration string
	adaptationSets       []dashAdaptationSet
}

// dashAdaptationSet groups one or more representations of the same content kind.
type dashAdaptationSet struct {
	id          string
	contentType string
	mimeType    string
	// template may be set at AdaptationSet level and inherited by representations.
	template        *dashSegmentTemplate
	representations []dashRepresentation
	// baseURLs are BaseURL elements declared at AdaptationSet level.
	baseURLs []string
}

// dashRepresentation is one encoded track inside an AdaptationSet.
type dashRepresentation struct {
	id       string
	template *dashSegmentTemplate // overrides the AdaptationSet template
	baseURLs []string             // BaseURL elements declared at Representation level
}

// dashSegmentTemplate mirrors the MPD SegmentTemplate element. Mosaic output
// always carries a SegmentTimeline, which is what auditing relies on.
// dashSegmentTemplate mirrors the MPD SegmentTemplate element. Mosaic output
// always carries a SegmentTimeline, which is what auditing relies on.
type dashSegmentTemplate struct {
	initialization string
	media          string
	timeline       []dashTimelineEntry
	timescale      float64
	startNumber    int
}

// dashTimelineEntry is one <S> element, including its implicit repeats.
type dashTimelineEntry struct {
	t int64
	d int64
	r int // -1 means "repeat to the end" which Mosaic never emits; treated as error
}
