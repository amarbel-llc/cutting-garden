package cutting_garden_plugin_optical

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startFakeCDDBServer serves the two-step freedb HTTP protocol for the
// fake shim's TOC (disc id fakeTOCDiscID): an exact-match query
// response and an xmcd read body for a two-track album. Returns the
// server URL (for CG_OPTICAL_CDDB_URL); shutdown is hooked to t.
func startFakeCDDBServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		cmd := req.URL.Query().Get("cmd")
		switch {
		case strings.HasPrefix(cmd, "cddb query "):
			w.Write([]byte("200 rock " + fakeTOCDiscID + " Fake Artist / Fake Album\n"))
		case strings.HasPrefix(cmd, "cddb read "):
			w.Write([]byte("210 rock " + fakeTOCDiscID + "\n" +
				"# xmcd\n" +
				"DISCID=" + fakeTOCDiscID + "\n" +
				"DTITLE=Fake Artist / Fake Album\n" +
				"DYEAR=1994\n" +
				"DGENRE=Rock\n" +
				"TTITLE0=First Song\n" +
				"TTITLE1=Second Song\n" +
				".\n"))
		default:
			w.Write([]byte("500 unrecognized command\n"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// fakeTOCLines is the parsed-side mirror of the shim's -Q output.
var fakeTOCLines = []string{
	"cdparanoia III release 10.2 (September 11, 2008)",
	"",
	"Table of contents (audio tracks only):",
	"track        length       begin        copy pre ch",
	"===========================================================",
	"  1.    19502 [04:20.02]        0 [00:00.00]    no   no  2",
	"  2.    19952 [04:26.02]    19502 [04:20.02]    no   no  2",
	"TOTAL   39454 [08:46.04]    (audio only)",
}

func TestParseTOC(t *testing.T) {
	tracks, err := parseTOC(fakeTOCLines)
	if err != nil {
		t.Fatalf("parseTOC: %v", err)
	}
	want := []tocTrack{
		{Number: 1, BeginSector: 0, LengthSectors: 19502},
		{Number: 2, BeginSector: 19502, LengthSectors: 19952},
	}
	if len(tracks) != len(want) {
		t.Fatalf("tracks = %d, want %d: %+v", len(tracks), len(want), tracks)
	}
	for i, w := range want {
		if tracks[i] != w {
			t.Errorf("tracks[%d] = %+v, want %+v", i, tracks[i], w)
		}
	}
}

func TestParseTOC_NoTracks(t *testing.T) {
	_, err := parseTOC([]string{"cdparanoia III release 10.2", "TOTAL 0"})
	if err == nil {
		t.Fatal("parseTOC returned nil error for trackless output")
	}
	if !strings.Contains(err.Error(), "no audio tracks") {
		t.Errorf("error %q missing 'no audio tracks'", err.Error())
	}
}

func TestCDDBDiscID(t *testing.T) {
	tracks, err := parseTOC(fakeTOCLines)
	if err != nil {
		t.Fatalf("parseTOC: %v", err)
	}
	// Hand-computed vector: start seconds 150/75=2 and 19652/75=262;
	// digit sums 2 + (2+6+2) = 12 = 0x0c. Total seconds
	// (39454+150)/75 − 2 = 526 = 0x020e. Track count 2.
	if got := cddbDiscID(tracks); got != fakeTOCDiscID {
		t.Errorf("cddbDiscID = %q, want %q", got, fakeTOCDiscID)
	}
	if got := discTotalSeconds(tracks); got != 526 {
		t.Errorf("discTotalSeconds = %d, want 526", got)
	}
}

func TestParseCDDBQuery(t *testing.T) {
	cases := []struct {
		name      string
		lines     []string
		wantCat   string
		wantFound bool
		wantErr   bool
	}{
		{
			name:      "exact match",
			lines:     []string{"200 rock 0c020e02 Artist / Album"},
			wantCat:   "rock",
			wantFound: true,
		},
		{
			name: "multiple matches takes first",
			lines: []string{
				"211 close matches found",
				"misc 0c020e02 Artist / Album",
				"rock 0c020e02 Artist / Album",
				".",
			},
			wantCat:   "misc",
			wantFound: true,
		},
		{
			name:      "no match",
			lines:     []string{"202 no match"},
			wantFound: false,
		},
		{
			name:    "server error status",
			lines:   []string{"500 internal"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat, found, err := parseCDDBQuery(tc.lines)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got cat=%q found=%v", cat, found)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCDDBQuery: %v", err)
			}
			if found != tc.wantFound || cat != tc.wantCat {
				t.Errorf("got (%q, %v), want (%q, %v)", cat, found, tc.wantCat, tc.wantFound)
			}
		})
	}
}

func TestParseCDDBRead(t *testing.T) {
	res, err := parseCDDBRead([]string{
		"210 rock 0c020e02",
		"# xmcd",
		"DISCID=0c020e02",
		"DTITLE=Fake Artist / Fake Album",
		"DYEAR=1994",
		"DGENRE=Rock",
		"TTITLE0=First Song",
		"TTITLE1=Second Song",
		".",
	})
	if err != nil {
		t.Fatalf("parseCDDBRead: %v", err)
	}
	if res.Artist != "Fake Artist" || res.Album != "Fake Album" {
		t.Errorf("artist/album = %q/%q", res.Artist, res.Album)
	}
	if res.Year != "1994" || res.Genre != "Rock" {
		t.Errorf("year/genre = %q/%q", res.Year, res.Genre)
	}
	if len(res.TrackTitles) != 2 ||
		res.TrackTitles[0] != "First Song" || res.TrackTitles[1] != "Second Song" {
		t.Errorf("track titles = %v", res.TrackTitles)
	}
}

func TestParseCDDBRead_SelfTitledDTITLE(t *testing.T) {
	res, err := parseCDDBRead([]string{
		"210 rock x",
		"DTITLE=Eponymous",
		".",
	})
	if err != nil {
		t.Fatalf("parseCDDBRead: %v", err)
	}
	if res.Artist != "Eponymous" || res.Album != "Eponymous" {
		t.Errorf("artist/album = %q/%q, want self-titled convention", res.Artist, res.Album)
	}
}

func TestWriteAudioMetadata_CDDBMatch(t *testing.T) {
	installFakeBin(t, cdparanoiaBin, fakeCdparanoiaScript)
	t.Setenv(cddbURLEnvVar, startFakeCDDBServer(t))
	dir := t.TempDir()

	rep := &recordingReporter{}
	if err := writeAudioMetadata(context.Background(), dir, "/dev/sr0", rep); err != nil {
		t.Fatalf("writeAudioMetadata: %v", err)
	}

	var meta discMeta
	body, err := os.ReadFile(filepath.Join(dir, tocFilename))
	if err != nil {
		t.Fatalf("read %s: %v", tocFilename, err)
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatalf("unmarshal %s: %v", tocFilename, err)
	}

	if meta.CDDBDiscID != fakeTOCDiscID {
		t.Errorf("disc id = %q, want %q", meta.CDDBDiscID, fakeTOCDiscID)
	}
	if meta.Device != "/dev/sr0" || meta.TotalSeconds != 526 {
		t.Errorf("device/total = %q/%d", meta.Device, meta.TotalSeconds)
	}
	if meta.CDDB == nil {
		t.Fatal("meta.CDDB is nil with a matching server")
	}
	if meta.CDDB.Artist != "Fake Artist" || meta.CDDB.Album != "Fake Album" ||
		meta.CDDB.Year != "1994" || meta.CDDB.Genre != "Rock" {
		t.Errorf("cddb fields = %+v", meta.CDDB)
	}
	if len(meta.Tracks) != 2 {
		t.Fatalf("tracks = %d, want 2", len(meta.Tracks))
	}
	if meta.Tracks[0].Title != "First Song" || meta.Tracks[1].Title != "Second Song" {
		t.Errorf("track titles = %q, %q", meta.Tracks[0].Title, meta.Tracks[1].Title)
	}
	if meta.Tracks[0].Artist != "Fake Artist" {
		t.Errorf("track artist = %q, want album artist", meta.Tracks[0].Artist)
	}

	// The raw CDDB response is preserved verbatim.
	raw, err := os.ReadFile(filepath.Join(dir, cddbFilename))
	if err != nil {
		t.Fatalf("read %s: %v", cddbFilename, err)
	}
	if !strings.Contains(string(raw), "DTITLE=Fake Artist / Fake Album") {
		t.Errorf("%s missing verbatim DTITLE line:\n%s", cddbFilename, raw)
	}

	// Per-track ID3 sidecars exist and carry the CDDB titles.
	for n, title := range map[int]string{1: "First Song", 2: "Second Song"} {
		tag, err := os.ReadFile(filepath.Join(dir, id3Filename(n)))
		if err != nil {
			t.Fatalf("read id3 %d: %v", n, err)
		}
		if !bytes.HasPrefix(tag, []byte("ID3\x04\x00\x00")) {
			t.Errorf("track %d tag missing ID3v2.4 header: % x", n, tag[:10])
		}
		if !bytes.Contains(tag, []byte(title)) {
			t.Errorf("track %d tag missing title %q", n, title)
		}
		if !bytes.Contains(tag, []byte("Fake Album")) {
			t.Errorf("track %d tag missing album", n)
		}
		if !bytes.Contains(tag, []byte(fakeTOCDiscID)) {
			t.Errorf("track %d tag missing CDDB disc id", n)
		}
	}
}

func TestWriteAudioMetadata_CDDBUnreachable_DegradesToTOC(t *testing.T) {
	installFakeBin(t, cdparanoiaBin, fakeCdparanoiaScript)
	// A server that closes immediately: lookup fails, capture continues.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(cddbURLEnvVar, srv.URL)
	dir := t.TempDir()

	if err := writeAudioMetadata(context.Background(), dir, "/dev/sr0", nil); err != nil {
		t.Fatalf("writeAudioMetadata: %v (cddb failure must not be fatal)", err)
	}

	var meta discMeta
	body, err := os.ReadFile(filepath.Join(dir, tocFilename))
	if err != nil {
		t.Fatalf("read %s: %v", tocFilename, err)
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if meta.CDDB != nil {
		t.Errorf("meta.CDDB = %+v, want nil on lookup failure", meta.CDDB)
	}
	if _, err := os.Stat(filepath.Join(dir, cddbFilename)); !os.IsNotExist(err) {
		t.Errorf("%s exists without a successful lookup", cddbFilename)
	}

	// Fallback ID3 tags still exist, with generic titles.
	tag, err := os.ReadFile(filepath.Join(dir, id3Filename(1)))
	if err != nil {
		t.Fatalf("read fallback id3: %v", err)
	}
	if !bytes.Contains(tag, []byte("Track 01")) {
		t.Errorf("fallback tag missing generic title; tag: % x", tag)
	}
}

func TestApplyCDDB_CompilationTitleSplit(t *testing.T) {
	meta := newDiscMeta("/dev/sr0", []tocTrack{
		{Number: 1, BeginSector: 0, LengthSectors: 19502},
	})
	applyCDDB(&meta, &cddbResult{
		Artist:      "Various",
		Album:       "Sampler",
		TrackTitles: []string{"Some Band / Some Song"},
	})
	if meta.Tracks[0].Artist != "Some Band" || meta.Tracks[0].Title != "Some Song" {
		t.Errorf("compilation split = %q/%q, want Some Band/Some Song",
			meta.Tracks[0].Artist, meta.Tracks[0].Title)
	}
}
