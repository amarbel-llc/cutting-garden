package cutting_garden_plugin_optical

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// defaultCDDBServer is the freedb-protocol HTTP endpoint queried for
// disc metadata. gnudb is the maintained freedb successor; the lookup
// is best-effort, so an outage degrades to TOC-only metadata.
const defaultCDDBServer = "http://gnudb.gnudb.org/~cddb/cddb.cgi"

// cddbURLEnvVar overrides the CDDB server: a URL points the lookup at
// another freedb-protocol mirror; the literal "off" disables the
// lookup entirely (no network touched).
const (
	cddbURLEnvVar = "CG_OPTICAL_CDDB_URL"
	cddbOff       = "off"
)

// cddbHTTPTimeout bounds each CDDB HTTP round-trip. The lookup is
// advisory; a slow server must not stall a capture for long.
const cddbHTTPTimeout = 10 * time.Second

// cddbMaxResponseBytes caps how much of a CDDB response is read. Real
// xmcd records are under a few KiB; the cap keeps a misbehaving server
// from ballooning memory (the timeout bounds time, not size).
const cddbMaxResponseBytes = 1 << 20

// cddbServer resolves the env override: (base URL, true) when the
// lookup should run, ("", false) when disabled via "off".
func cddbServer() (string, bool) {
	base := os.Getenv(cddbURLEnvVar)
	switch base {
	case cddbOff:
		return "", false
	case "":
		return defaultCDDBServer, true
	}
	return base, true
}

// cddbResult is the parsed outcome of a successful query+read pair.
// TrackTitles is indexed by track number - 1 (TTITLE0 = track 1).
type cddbResult struct {
	Category    string
	Artist      string
	Album       string
	Year        string
	Genre       string
	TrackTitles []string
}

// cddbLookup runs the two-step freedb HTTP protocol: `cddb query`
// resolves the disc id to a (category, discid) database key, `cddb
// read` fetches the xmcd record. Returns (nil, "", nil) on a clean
// no-match — only transport/protocol trouble is an error. raw is the
// verbatim read-response body, preserved as the disc.cddb provenance
// blob.
func cddbLookup(
	ctx context.Context,
	base string,
	discID string,
	tracks []tocTrack,
) (res *cddbResult, raw string, err error) {
	queryLines, _, err := cddbCommand(ctx, base, cddbQueryCmd(discID, tracks))
	if err != nil {
		return nil, "", err
	}
	category, found, err := parseCDDBQuery(queryLines)
	if err != nil {
		return nil, "", err
	}
	if !found {
		return nil, "", nil
	}

	readLines, readRaw, err := cddbCommand(ctx, base, "cddb read "+category+" "+discID)
	if err != nil {
		return nil, "", err
	}
	res, err = parseCDDBRead(readLines)
	if err != nil {
		return nil, "", err
	}
	res.Category = category
	return res, readRaw, nil
}

// cddbQueryCmd builds the `cddb query` command string: disc id, track
// count, each track's start frame (lead-in included), and the total
// playing time in seconds.
func cddbQueryCmd(discID string, tracks []tocTrack) string {
	parts := []string{"cddb", "query", discID, strconv.Itoa(len(tracks))}
	for _, t := range tracks {
		parts = append(parts, strconv.FormatInt(t.BeginSector+leadInSectors, 10))
	}
	parts = append(parts, strconv.FormatInt(
		(leadOutSector(tracks)+leadInSectors)/sectorsPerSecond, 10,
	))
	return strings.Join(parts, " ")
}

// cddbCommand issues one freedb-over-HTTP command (proto level 6 =
// UTF-8) and returns the response body as lines plus verbatim. The
// request honors ctx; the client timeout caps a stalled server.
func cddbCommand(ctx context.Context, base, cmd string) (lines []string, raw string, err error) {
	v := url.Values{}
	v.Set("cmd", cmd)
	v.Set("hello", "cutting-garden localhost cutting-garden 0")
	v.Set("proto", "6")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+v.Encode(), nil)
	if err != nil {
		return nil, "", errors.Wrap(err)
	}

	client := &http.Client{Timeout: cddbHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", errors.Wrap(err)
	}
	defer errors.DeferredCloser(&err, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, "", errors.ErrorWithStackf(
			"optical plugin: cddb server %s returned HTTP %d", base, resp.StatusCode,
		)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, cddbMaxResponseBytes))
	if err != nil {
		return nil, "", errors.Wrap(err)
	}

	raw = string(body)
	for _, line := range strings.Split(raw, "\n") {
		lines = append(lines, strings.TrimRight(line, "\r"))
	}
	return lines, raw, nil
}

// parseCDDBQuery decodes a `cddb query` response. 200 is an exact
// match; 210/211 list candidates (take the first); 202 is a clean
// no-match. Anything else is a protocol error.
func parseCDDBQuery(lines []string) (category string, found bool, err error) {
	if len(lines) == 0 {
		return "", false, errors.ErrorWithStackf("optical plugin: empty cddb query response")
	}
	fields := strings.Fields(lines[0])
	if len(fields) == 0 {
		return "", false, errors.ErrorWithStackf(
			"optical plugin: malformed cddb query response %q", lines[0],
		)
	}
	switch fields[0] {
	case "200":
		if len(fields) < 2 {
			return "", false, errors.ErrorWithStackf(
				"optical plugin: malformed cddb exact match %q", lines[0],
			)
		}
		return fields[1], true, nil
	case "210", "211":
		for _, line := range lines[1:] {
			if line == "." {
				break
			}
			if cf := strings.Fields(line); len(cf) >= 2 {
				return cf[0], true, nil
			}
		}
		return "", false, nil
	case "202":
		return "", false, nil
	}
	return "", false, errors.ErrorWithStackf(
		"optical plugin: unexpected cddb query status %q", lines[0],
	)
}

// parseCDDBRead decodes a `cddb read` xmcd body: DTITLE ("artist /
// album", possibly continued across repeated lines), DYEAR, DGENRE,
// and TTITLEn per track. Comment lines (#) and unknown keywords are
// skipped.
func parseCDDBRead(lines []string) (*cddbResult, error) {
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "210") {
		first := ""
		if len(lines) > 0 {
			first = lines[0]
		}
		return nil, errors.ErrorWithStackf(
			"optical plugin: unexpected cddb read status %q", first,
		)
	}

	res := &cddbResult{}
	var dtitle string
	titles := map[int]string{}
	maxTitle := -1

	for _, line := range lines[1:] {
		if line == "." {
			break
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch {
		case key == "DTITLE":
			dtitle += value
		case key == "DYEAR":
			res.Year = value
		case key == "DGENRE":
			res.Genre = value
		case strings.HasPrefix(key, "TTITLE"):
			n, err := strconv.Atoi(key[len("TTITLE"):])
			if err != nil {
				continue
			}
			titles[n] += value
			if n > maxTitle {
				maxTitle = n
			}
		}
	}

	if artist, album, ok := strings.Cut(dtitle, " / "); ok {
		res.Artist, res.Album = artist, album
	} else {
		// Self-titled convention: a DTITLE without the separator names
		// both the artist and the album.
		res.Artist, res.Album = dtitle, dtitle
	}

	res.TrackTitles = make([]string, maxTitle+1)
	for n, title := range titles {
		res.TrackTitles[n] = title
	}
	return res, nil
}

// cutCompilationTitle splits a CDDB compilation-style track title
// ("artist / title") into its parts. found is false for plain titles.
func cutCompilationTitle(title string) (artist, rest string, found bool) {
	return strings.Cut(title, " / ")
}
