package audit

import "testing"

func TestTrailingSegmentNumber(t *testing.T) {
	tests := []struct {
		uri  string
		want int
		ok   bool
	}{
		{"seg_0_2.m4s", 2, true},
		{"chunk-stream1-3.m4s", 3, true},
		{"nested/dir/seg_10.m4s", 10, true},
		{"segment.ts", 0, false},
		{"sub_en.vtt", 0, false},
		{".m4s", 0, false},
	}
	for _, tt := range tests {
		got, ok := trailingSegmentNumber(tt.uri)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("trailingSegmentNumber(%q) = (%d, %v), want (%d, %v)", tt.uri, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseISO8601Duration(t *testing.T) {
	tests := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"PT12.0S", 12.0, true},
		{"PT5.0S", 5.0, true},
		{"PT1M30S", 90.0, true},
		{"PT1H", 3600.0, true},
		{"P1DT2H", 93600.0, true},
		{"PT0.5S", 0.5, true},
		{"12S", 0, false}, // missing P
		{"P", 0, false},   // no designators
		{"PTX", 0, false}, // non-numeric
		{"PT12S-extra", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseISO8601Duration(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("parseISO8601Duration(%q) = (%v, %v), want (%v, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestIsRemoteURI(t *testing.T) {
	tests := map[string]bool{
		"https://cdn.example.com/a.m3u8": true,
		"http://x/y":                     true,
		"s3://bucket/key":                true,
		"seg_0_0.m4s":                    false,
		"../init.mp4":                    false,
		"/abs/path":                      false,
	}
	for uri, want := range tests {
		if got := isRemoteURI(uri); got != want {
			t.Errorf("isRemoteURI(%q) = %v, want %v", uri, got, want)
		}
	}
}

func TestExpandTemplate(t *testing.T) {
	tests := []struct {
		want    string
		pattern string
		repID   string
		time    int64
		number  int
		ok      bool
	}{
		{pattern: "chunk-$RepresentationID$-$Number$.m4s", repID: "7", number: 3, time: 0, want: "chunk-7-3.m4s", ok: true},
		{pattern: "seg-$Time$.m4s", repID: "0", number: 1, time: 76800, want: "seg-76800.m4s", ok: true},
		{pattern: "init-$RepresentationID$.m4s", repID: "2", number: 0, time: 0, want: "init-2.m4s", ok: true},
		{pattern: "price-$$5-$Number$.mp4", repID: "0", number: 1, time: 0, want: "price-$5-1.mp4", ok: true},
		{pattern: "bad-$Unknown$.mp4", repID: "0", number: 1, time: 0, want: "", ok: false},
	}
	for _, tt := range tests {
		got, ok := expandTemplate(tt.pattern, tt.repID, tt.number, tt.time)
		if ok != tt.ok || got != tt.want {
			t.Errorf("expandTemplate(%q) = (%q, %v), want (%q, %v)", tt.pattern, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseDASHManifest_RealShape(t *testing.T) {
	// Use a minimal standalone document so the test does not depend on t.TempDir.
	doc := []byte(`<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" mediaPresentationDuration="PT12.0S">
  <Period>
    <AdaptationSet id="0" contentType="video">
      <Representation id="0" bandwidth="1000">
        <SegmentTemplate timescale="1000" media="c-$Number$.m4s" initialization="i-$RepresentationID$.m4s" startNumber="1">
          <SegmentTimeline>
            <S t="0" d="5000" r="1"/>
            <S d="2000"/>
          </SegmentTimeline>
        </SegmentTemplate>
      </Representation>
    </AdaptationSet>
  </Period>
</MPD>`)
	m, err := parseDASHManifest(doc)
	if err != nil {
		t.Fatalf("parseDASHManifest: %v", err)
	}
	if len(m.adaptationSets) != 1 || len(m.adaptationSets[0].representations) != 1 {
		t.Fatalf("unexpected structure: %+v", m.adaptationSets)
	}
	rep := m.adaptationSets[0].representations[0]
	if rep.id != "0" || rep.template == nil {
		t.Fatalf("bad representation: %+v", rep)
	}
	if rep.template.timescale != 1000 || rep.template.startNumber != 1 {
		t.Errorf("template attrs wrong: %+v", rep.template)
	}
	if len(rep.template.timeline) != 2 || rep.template.timeline[0].r != 1 {
		t.Errorf("timeline wrong: %+v", rep.template.timeline)
	}
}

func TestParseDASHManifest_Errors(t *testing.T) {
	cases := map[string][]byte{
		"malformed xml":     []byte(`<MPD><Period>`),
		"no adaptation set": []byte(`<MPD xmlns="x"><Period/></MPD>`),
		"bad timeline d":    []byte(`<MPD xmlns="x"><Period><AdaptationSet contentType="video"><Representation id="0"><SegmentTemplate media="a"><SegmentTimeline><S d="zero"/></SegmentTimeline></SegmentTemplate></Representation></AdaptationSet></Period></MPD>`),
	}
	for name, doc := range cases {
		if _, err := parseDASHManifest(doc); err == nil {
			t.Errorf("%s: expected parse error, got nil", name)
		}
	}
}

func TestOptionsNormalize(t *testing.T) {
	o := Options{}
	o.normalize()
	if o.MaxManifestBytes != DefaultMaxManifestBytes ||
		o.MaxRenditions != DefaultMaxRenditions ||
		o.MaxSegmentsPerRendition != DefaultMaxSegmentsPerRendition ||
		o.MaxIssues != DefaultMaxIssues ||
		o.TimelineTolerance != DefaultTimelineTolerance ||
		o.DurationDriftTolerance != DefaultDurationDriftTolerance {
		t.Errorf("defaults not applied: %+v", o)
	}

	custom := Options{
		MaxManifestBytes:        10,
		MaxRenditions:           2,
		MaxSegmentsPerRendition: 3,
		MaxIssues:               4,
		TimelineTolerance:       0.1,
		DurationDriftTolerance:  0.2,
		Format:                  " HLS ",
	}
	custom.normalize()
	if custom.Format != FormatHLS || custom.MaxManifestBytes != 10 || custom.MaxIssues != 4 {
		t.Errorf("custom options not preserved: %+v", custom)
	}
}
