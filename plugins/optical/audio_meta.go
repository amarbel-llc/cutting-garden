package cutting_garden_plugin_optical

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_events"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Audio-mode metadata sidecar filenames, written into the capture
// tempdir alongside cdparanoia's trackNN.cdda.wav rips so the artifact
// walk streams them as ordinary file entries. Per-track ID3 tags are
// named trackNN.id3 (see id3Filename).
const (
	// tocFilename is the parsed disc metadata: TOC, CDDB disc-id, and
	// (when the lookup succeeded) the parsed CDDB fields per track.
	tocFilename = "disc.toc.json"
	// cddbFilename is the raw CDDB read response, captured verbatim as
	// provenance for the parsed fields in disc.toc.json. Only written
	// when the lookup found a match.
	cddbFilename = "disc.cddb"
)

// Audio-CD addressing constants: 75 sectors (frames) per second, and
// the 150-sector (2 s) lead-in that MSF addresses — and therefore the
// CDDB disc-id algorithm — include.
const (
	sectorsPerSecond = 75
	leadInSectors    = 150
)

// tocTrack is one audio track from `cdparanoia -Q`'s table of
// contents: its number plus begin/length in sectors (LBA, lead-in
// excluded — cdparanoia prints track 1 beginning at 0).
type tocTrack struct {
	Number        int
	BeginSector   int64
	LengthSectors int64
}

// tocLineRE matches one track row of cdparanoia -Q output, e.g.
// "  1.    19502 [04:20.02]        0 [00:00.00]    no   no  2".
// Captures: track number, length sectors, begin sectors. The header,
// separator, and TOTAL rows don't match (no `N.` prefix).
var tocLineRE = regexp.MustCompile(`^\s*(\d+)\.\s+(\d+)\s+\[[\d:.]+\]\s+(\d+)\s+\[`)

// parseTOC extracts the track table from cdparanoia -Q output lines
// (both streams, any interleaving — track rows stay ordered within
// stderr and are re-sorted by number anyway).
func parseTOC(lines []string) ([]tocTrack, error) {
	var tracks []tocTrack
	for _, line := range lines {
		m := tocLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		num, _ := strconv.Atoi(m[1])
		length, _ := strconv.ParseInt(m[2], 10, 64)
		begin, _ := strconv.ParseInt(m[3], 10, 64)
		tracks = append(tracks, tocTrack{
			Number:        num,
			BeginSector:   begin,
			LengthSectors: length,
		})
	}
	if len(tracks) == 0 {
		return nil, errors.ErrorWithStackf(
			"optical plugin: no audio tracks in cdparanoia -Q output (%d lines)\n"+
				"hint: is an audio CD in the drive?",
			len(lines),
		)
	}
	sort.Slice(tracks, func(i, j int) bool { return tracks[i].Number < tracks[j].Number })
	return tracks, nil
}

// leadOutSector is the disc's lead-out address: where the last track
// ends.
func leadOutSector(tracks []tocTrack) int64 {
	last := tracks[len(tracks)-1]
	return last.BeginSector + last.LengthSectors
}

// cddbDiscID computes the classic 8-hex-digit CDDB/freedb disc id:
//
//	(digit-sum-of-start-seconds % 0xFF) << 24 | total-seconds << 8 | track-count
//
// where start seconds include the 150-sector lead-in. This is the id
// servers key their databases on; it also serves as a stable disc
// identity in disc.toc.json even when no server is reachable.
func cddbDiscID(tracks []tocTrack) string {
	var n int64
	for _, t := range tracks {
		n += digitSum((t.BeginSector + leadInSectors) / sectorsPerSecond)
	}
	total := discTotalSeconds(tracks)
	id := uint32(n%0xFF)<<24 | uint32(total)<<8 | uint32(len(tracks))
	return fmt.Sprintf("%08x", id)
}

// discTotalSeconds is the playing time the CDDB algorithm (and query
// command) uses: lead-out minus first-track start, in whole seconds.
func discTotalSeconds(tracks []tocTrack) int64 {
	first := (tracks[0].BeginSector + leadInSectors) / sectorsPerSecond
	leadOut := (leadOutSector(tracks) + leadInSectors) / sectorsPerSecond
	return leadOut - first
}

func digitSum(n int64) int64 {
	var sum int64
	for n > 0 {
		sum += n % 10
		n /= 10
	}
	return sum
}

// discMeta is the disc.toc.json shape: device identity, the computed
// CDDB disc id, per-track TOC data, and — when the CDDB lookup
// succeeded — the album-level fields plus per-track titles.
type discMeta struct {
	Device       string      `json:"device"`
	CDDBDiscID   string      `json:"cddb_disc_id"`
	TotalSeconds int64       `json:"total_seconds"`
	Tracks       []trackMeta `json:"tracks"`
	CDDB         *cddbMeta   `json:"cddb,omitempty"`
}

type trackMeta struct {
	Number          int     `json:"number"`
	BeginSector     int64   `json:"begin_sector"`
	LengthSectors   int64   `json:"length_sectors"`
	DurationSeconds float64 `json:"duration_seconds"`
	Title           string  `json:"title,omitempty"`
	Artist          string  `json:"artist,omitempty"`
}

// cddbMeta is the parsed album-level CDDB result embedded in
// disc.toc.json. The raw server response lives separately in
// disc.cddb.
type cddbMeta struct {
	Category string `json:"category"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Year     string `json:"year,omitempty"`
	Genre    string `json:"genre,omitempty"`
}

// newDiscMeta builds the TOC-only metadata (no CDDB fields yet).
func newDiscMeta(device string, tracks []tocTrack) discMeta {
	meta := discMeta{
		Device:       device,
		CDDBDiscID:   cddbDiscID(tracks),
		TotalSeconds: discTotalSeconds(tracks),
		Tracks:       make([]trackMeta, len(tracks)),
	}
	for i, t := range tracks {
		meta.Tracks[i] = trackMeta{
			Number:          t.Number,
			BeginSector:     t.BeginSector,
			LengthSectors:   t.LengthSectors,
			DurationSeconds: float64(t.LengthSectors) / sectorsPerSecond,
		}
	}
	return meta
}

// applyCDDB folds a CDDB read result into meta: album fields plus
// per-track titles. A track title containing " / " follows the CDDB
// compilation convention (per-track "artist / title"); split it so the
// ID3 TPE1 frame carries the track artist rather than "Various".
func applyCDDB(meta *discMeta, res *cddbResult) {
	meta.CDDB = &cddbMeta{
		Category: res.Category,
		Artist:   res.Artist,
		Album:    res.Album,
		Year:     res.Year,
		Genre:    res.Genre,
	}
	for i := range meta.Tracks {
		if i >= len(res.TrackTitles) {
			break
		}
		title := res.TrackTitles[i]
		artist := res.Artist
		if a, t, found := cutCompilationTitle(title); found {
			artist, title = a, t
		}
		meta.Tracks[i].Title = title
		meta.Tracks[i].Artist = artist
	}
}

// writeAudioMetadata is the audio-mode pre-rip phase: read the TOC via
// `cdparanoia -Q`, compute the CDDB disc id, best-effort look the disc
// up on a CDDB server, and write the metadata sidecars (disc.toc.json,
// disc.cddb, trackNN.id3) into outDir so the post-rip artifact walk
// streams them into the blob store as ordinary entries.
//
// TOC failure is fatal (an unreadable disc would fail the rip anyway,
// and the tool's stderr tail is the best diagnostic). CDDB failure —
// network trouble, no match, lookup disabled — degrades to TOC-only
// metadata with a Log line; the ID3 tags then carry fallback titles.
func writeAudioMetadata(
	ctx context.Context,
	outDir string,
	device string,
	r cutting_garden_plugins.Reporter,
) error {
	// Nil-safe even when called directly (e.g. in tests): a nil
	// reporter becomes a no-op.
	r = cutting_garden_plugins.ReporterOrNop(r)

	r.PhaseStart("read toc " + device)

	failPhase := func(err error) error {
		r.PhaseEnd(capture_events.Verdict{
			OK:         false,
			Diagnostic: map[string]any{"error": err.Error()},
		})
		return err
	}

	// Accumulate every output line for the TOC parse while still
	// forwarding to the live Log stream. runExternal invokes onLog from
	// two concurrent scanner goroutines, so the accumulator locks.
	var (
		mu    sync.Mutex
		lines []string
	)
	onLog := func(line string) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
		r.Log("%s", line)
	}

	if err := runExternal(ctx, outDir, cdparanoiaBin, []string{"-Q", "-d", device}, onLog); err != nil {
		return failPhase(err)
	}

	tracks, err := parseTOC(lines)
	if err != nil {
		return failPhase(err)
	}

	meta := newDiscMeta(device, tracks)

	if base, enabled := cddbServer(); !enabled {
		r.Log("optical plugin: cddb lookup disabled via %s", cddbURLEnvVar)
	} else if res, raw, lookupErr := cddbLookup(ctx, base, meta.CDDBDiscID, tracks); lookupErr != nil {
		r.Log("optical plugin: cddb lookup failed (continuing with toc-only metadata): %v", lookupErr)
	} else if res == nil {
		r.Log("optical plugin: no cddb match for disc id %s", meta.CDDBDiscID)
	} else {
		applyCDDB(&meta, res)
		if err := os.WriteFile(filepath.Join(outDir, cddbFilename), []byte(raw), 0o644); err != nil {
			return failPhase(errors.Wrap(err))
		}
	}

	body, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return failPhase(errors.Wrap(err))
	}
	if err := os.WriteFile(filepath.Join(outDir, tocFilename), append(body, '\n'), 0o644); err != nil {
		return failPhase(errors.Wrap(err))
	}

	for _, t := range meta.Tracks {
		if err := os.WriteFile(
			filepath.Join(outDir, id3Filename(t.Number)),
			trackID3(meta, t),
			0o644,
		); err != nil {
			return failPhase(errors.Wrap(err))
		}
	}

	r.PhaseEnd(capture_events.Verdict{OK: true})
	return nil
}
