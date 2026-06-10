package cutting_garden_plugin_optical

import (
	"strings"
	"testing"
)

// parseID3ForTest is a minimal ID3v2.4 reader for asserting the
// builder's output: returns frame id → decoded text value (TXXX values
// keep their NUL-separated description prefix replaced by "desc=").
func parseID3ForTest(t *testing.T, tag []byte) map[string]string {
	t.Helper()
	if len(tag) < 10 || string(tag[:3]) != "ID3" || tag[3] != 0x04 {
		t.Fatalf("not an ID3v2.4 tag: % x", tag[:min(len(tag), 10)])
	}
	total := unsyncsafe(tag[6:10])
	if total != len(tag)-10 {
		t.Fatalf("header size %d != frames length %d", total, len(tag)-10)
	}

	frames := map[string]string{}
	rest := tag[10:]
	for len(rest) >= 10 {
		id := string(rest[:4])
		size := unsyncsafe(rest[4:8])
		payload := rest[10 : 10+size]
		if payload[0] != utf8Encoding {
			t.Fatalf("frame %s: encoding byte %#x, want UTF-8 (0x03)", id, payload[0])
		}
		value := string(payload[1:])
		if id == "TXXX" {
			desc, v, ok := strings.Cut(value, "\x00")
			if !ok {
				t.Fatalf("TXXX missing NUL separator: %q", value)
			}
			value = desc + "=" + v
		}
		frames[id] = value
		rest = rest[10+size:]
	}
	if len(rest) != 0 {
		t.Fatalf("%d trailing bytes after last frame", len(rest))
	}
	return frames
}

func unsyncsafe(b []byte) int {
	return int(b[0])<<21 | int(b[1])<<14 | int(b[2])<<7 | int(b[3])
}

func TestTrackID3_FullMetadata(t *testing.T) {
	meta := discMeta{
		CDDBDiscID: "0c020e02",
		CDDB: &cddbMeta{
			Artist: "Fake Artist",
			Album:  "Fake Album",
			Year:   "1994",
			Genre:  "Rock",
		},
		Tracks: make([]trackMeta, 2),
	}
	tag := trackID3(meta, trackMeta{Number: 2, Title: "Second Song", Artist: "Fake Artist"})

	frames := parseID3ForTest(t, tag)
	want := map[string]string{
		"TIT2": "Second Song",
		"TPE1": "Fake Artist",
		"TALB": "Fake Album",
		"TDRC": "1994",
		"TCON": "Rock",
		"TRCK": "2/2",
		"TXXX": "CDDB_DISC_ID=0c020e02",
	}
	for id, value := range want {
		if frames[id] != value {
			t.Errorf("frame %s = %q, want %q", id, frames[id], value)
		}
	}
	if len(frames) != len(want) {
		t.Errorf("frames = %v, want exactly %d", frames, len(want))
	}
}

func TestTrackID3_TOCOnlyFallback(t *testing.T) {
	meta := discMeta{
		CDDBDiscID: "0c020e02",
		Tracks:     make([]trackMeta, 2),
	}
	tag := trackID3(meta, trackMeta{Number: 1})

	frames := parseID3ForTest(t, tag)
	if frames["TIT2"] != "Track 01" {
		t.Errorf("TIT2 = %q, want generic fallback", frames["TIT2"])
	}
	if frames["TRCK"] != "1/2" {
		t.Errorf("TRCK = %q, want 1/2", frames["TRCK"])
	}
	if frames["TXXX"] != "CDDB_DISC_ID=0c020e02" {
		t.Errorf("TXXX = %q", frames["TXXX"])
	}
	// No album/artist/year/genre frames without CDDB — and crucially no
	// empty frames.
	for _, id := range []string{"TALB", "TPE1", "TDRC", "TCON"} {
		if v, ok := frames[id]; ok {
			t.Errorf("unexpected frame %s = %q without cddb data", id, v)
		}
	}
}

func TestID3Filename(t *testing.T) {
	if got := id3Filename(3); got != "track03.id3" {
		t.Errorf("id3Filename(3) = %q, want track03.id3", got)
	}
}
