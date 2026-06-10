package cutting_garden_plugin_optical

import (
	"bytes"
	"fmt"
)

// id3Filename is the per-track tag sidecar name, numbered to pair with
// cdparanoia's trackNN.cdda.wav output.
func id3Filename(trackNumber int) string {
	return fmt.Sprintf("track%02d.id3", trackNumber)
}

// trackID3 renders one track's metadata as a standalone ID3v2.4 tag
// blob: title/artist from CDDB (with "Track NN" / album-artist
// fallbacks when the lookup found nothing), album/year/genre when
// known, the track position, and the CDDB disc id as a TXXX frame so
// the tag alone identifies the source disc. Encoders prepend exactly
// this byte shape to an MP3/FLAC transcode, so the blob is directly
// usable downstream.
func trackID3(meta discMeta, t trackMeta) []byte {
	b := &id3Builder{}

	title := t.Title
	if title == "" {
		title = fmt.Sprintf("Track %02d", t.Number)
	}
	b.text("TIT2", title)
	b.text("TPE1", t.Artist)

	if meta.CDDB != nil {
		b.text("TALB", meta.CDDB.Album)
		b.text("TDRC", meta.CDDB.Year)
		b.text("TCON", meta.CDDB.Genre)
	}

	b.text("TRCK", fmt.Sprintf("%d/%d", t.Number, len(meta.Tracks)))
	b.userText("CDDB_DISC_ID", meta.CDDBDiscID)

	return b.bytes()
}

// id3Builder accumulates ID3v2.4 frames. v2.4 is used (over the more
// common v2.3) for its UTF-8 text encoding — CDDB proto level 6
// responses are UTF-8, so titles round-trip without transliteration.
type id3Builder struct {
	frames bytes.Buffer
}

// utf8Encoding is the ID3v2.4 text-encoding marker byte for UTF-8.
const utf8Encoding = 0x03

// text appends a standard text-information frame (TIT2, TPE1, …).
// Empty values are skipped — ID3 forbids empty text frames.
func (b *id3Builder) text(id, value string) {
	if value == "" {
		return
	}
	payload := append([]byte{utf8Encoding}, value...)
	b.frame(id, payload)
}

// userText appends a TXXX user-defined frame: encoding,
// NUL-terminated description, value.
func (b *id3Builder) userText(description, value string) {
	if value == "" {
		return
	}
	payload := []byte{utf8Encoding}
	payload = append(payload, description...)
	payload = append(payload, 0x00)
	payload = append(payload, value...)
	b.frame("TXXX", payload)
}

// frame appends one frame: 4-char id, syncsafe payload size, two zero
// flag bytes, payload.
func (b *id3Builder) frame(id string, payload []byte) {
	b.frames.WriteString(id)
	b.frames.Write(syncsafe(len(payload)))
	b.frames.Write([]byte{0x00, 0x00})
	b.frames.Write(payload)
}

// bytes renders the complete tag: the 10-byte header ("ID3", version
// 2.4.0, no flags, syncsafe total frame size) followed by the frames.
func (b *id3Builder) bytes() []byte {
	var out bytes.Buffer
	out.WriteString("ID3")
	out.Write([]byte{0x04, 0x00, 0x00})
	out.Write(syncsafe(b.frames.Len()))
	out.Write(b.frames.Bytes())
	return out.Bytes()
}

// syncsafe encodes n as ID3's 4-byte syncsafe integer (7 bits per
// byte, high bit always clear, so the value never looks like an MPEG
// sync pattern).
func syncsafe(n int) []byte {
	return []byte{
		byte(n>>21) & 0x7F,
		byte(n>>14) & 0x7F,
		byte(n>>7) & 0x7F,
		byte(n) & 0x7F,
	}
}
