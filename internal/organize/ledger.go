package organize

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The creation ledger guards organize's creations against duplication. A
// temp id carries no identity the substrate knows, so re-applying a document
// whose `+` boxes already landed — a verbatim re-run of `-apply <file>`, or a
// re-apply after a LATER write failed — would create every object again
// (caldav mints a fresh UID each time). The ledger records, as each creation
// lands and before any later write runs, `(_base digest, ledger key) → the
// created node's URI and box id`; planning consults it and SKIPS a recorded
// creation instead of creating it twice.
//
// Storage: one small JSON record per pinned base, keyed by the base digest, in
// cutting-garden's XDG STATE dir — `$XDG_STATE_HOME/cutting-garden/
// organize-creations/<base digest>.json` (captures.log's home). It is keyed,
// mutable bookkeeping, not content: the madder store is content-addressed and
// cannot be looked up by (base, temp id), and the base blob itself must stay
// the byte-exact document it certifies.
//
// Scope: the `_base` digest. A NEW generation (a new `_base`) that reuses a
// `+name` creates a genuinely new object.

// ledgerDirName is the ledger's directory under cutting-garden's XDG state.
const ledgerDirName = "organize-creations"

// ledgerEntry is one recorded creation.
type ledgerEntry struct {
	// Key is the ledger key (creationLedgerKeys): `+<id>` for a named temp
	// id, `+#<content hash>#<n>` for a bare `+`.
	Key string `json:"key"`
	// TempID is the temp id as the document spelled it.
	TempID string `json:"temp_id"`
	// URI is the created node's URI; ID its box id against the anchor.
	URI string `json:"uri"`
	ID  string `json:"id"`
}

type ledgerRecord struct {
	Base      string        `json:"base"`
	Creations []ledgerEntry `json:"creations"`
}

// creationLedger is the on-disk ledger rooted at dir; the zero value (empty
// dir) records nothing and finds nothing.
type creationLedger struct {
	dir string
}

func (l creationLedger) path(base string) string {
	return filepath.Join(l.dir, base+".json")
}

// entries returns the recorded creations for base (none when the record is
// absent). A record that exists but does not parse is an error: a guard that
// silently forgets would duplicate.
func (l creationLedger) entries(base string) ([]ledgerEntry, error) {
	if l.dir == "" || base == "" {
		return nil, nil
	}
	data, err := os.ReadFile(l.path(base))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrapf(err, "organize: read creation ledger")
	}
	var record ledgerRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, errors.ErrorWithStackf(
			"organize: creation ledger %s is unreadable: %s", l.path(base), err,
		)
	}
	return record.Creations, nil
}

// lookup finds the recorded creation for (base, key).
func (l creationLedger) lookup(base, key string) (ledgerEntry, bool, error) {
	entries, err := l.entries(base)
	if err != nil {
		return ledgerEntry{}, false, err
	}
	for _, e := range entries {
		if e.Key == key {
			return e, true, nil
		}
	}
	return ledgerEntry{}, false, nil
}

// record adds (or replaces) one creation for base, writing the record
// atomically (temp file + rename).
func (l creationLedger) record(base string, entry ledgerEntry) error {
	if l.dir == "" || base == "" {
		return nil
	}
	entries, err := l.entries(base)
	if err != nil {
		return err
	}
	kept := entries[:0]
	for _, e := range entries {
		if e.Key != entry.Key {
			kept = append(kept, e)
		}
	}
	data, err := json.MarshalIndent(ledgerRecord{Base: base, Creations: append(kept, entry)}, "", "  ")
	if err != nil {
		return errors.Wrap(err)
	}
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return errors.Wrapf(err, "organize: create creation ledger dir")
	}
	tmp, err := os.CreateTemp(l.dir, ".ledger-*")
	if err != nil {
		return errors.Wrapf(err, "organize: write creation ledger")
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return errors.Wrapf(err, "organize: write creation ledger")
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return errors.Wrap(err)
	}
	if err := os.Rename(tmp.Name(), l.path(base)); err != nil {
		_ = os.Remove(tmp.Name())
		return errors.Wrapf(err, "organize: write creation ledger")
	}
	return nil
}

// creationLedgerKeys computes each temp-id object's LEDGER key, keyed by its
// merge key. A named temp id is its own key (`+wrap-bug`). A bare `+` has no
// name, so it is keyed by a hash of its box content (type, tags, atoms) and
// its whitespace-collapsed trailer, plus its occurrence among identical bare
// boxes in document order (`+#<hash>#<n>`) — stable across a verbatim
// re-apply and a buffer whose other lines moved; a bare box whose content was
// edited is a new creation.
func creationLedgerKeys(
	appearances map[string][]creationAppearance, order []string,
) map[string]string {
	keys := make(map[string]string, len(order))
	seen := map[string]int{}
	for _, mergeKey := range order {
		ln := appearances[mergeKey][0].line
		if ln.ID != "" {
			keys[mergeKey] = "+" + ln.ID
			continue
		}
		hash := barePlusContentHash(ln)
		seen[hash]++
		keys[mergeKey] = fmt.Sprintf("+#%s#%d", hash, seen[hash])
	}
	return keys
}

func barePlusContentHash(ln objectLine) string {
	lit := trellis.Literal{New: true, Type: ln.Type, Tags: ln.Tags}
	for _, f := range ln.Fields {
		lit.Atoms = append(lit.Atoms, trellis.Atom{Name: f.Name, Value: f.Value})
	}
	var b strings.Builder
	trellis.WriteLiteral(&b, lit)
	b.WriteByte(0)
	b.WriteString(collapseWhitespace(ln.Desc))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// rewriteLandedTempIDs rewrites, in an organize document's text, the box id of
// every appearance of each landed creation — `+wrap-bug`, `+"wrap bug"`, a
// bare `+` — to the created object's real box id, leaving every other byte
// alone. landed maps ledger keys to box ids. The interactive editor path runs
// it over its temp buffer after a failed apply, so the buffer names the
// objects that already exist and re-applying it cannot create them again.
func rewriteLandedTempIDs(text string, landed map[string]string) (string, error) {
	doc, err := parseDocument(text)
	if err != nil {
		return "", err
	}
	appearances, order, _, err := splitCreations(doc)
	if err != nil {
		return "", err
	}
	_, _, _, body, err := splitEnvelope(text)
	if err != nil {
		return "", err
	}

	lines := strings.Split(text, "\n")
	offset := len(lines) - len(strings.Split(body, "\n"))
	keys := creationLedgerKeys(appearances, order)
	for _, mergeKey := range order {
		id, ok := landed[keys[mergeKey]]
		if !ok {
			continue
		}
		for _, app := range appearances[mergeKey] {
			index := offset + app.line.Line - 1
			if index < 0 || index >= len(lines) {
				continue
			}
			lines[index] = replaceTempIDToken(lines[index], trellis.QuoteIfNeeded(id))
		}
	}
	return strings.Join(lines, "\n"), nil
}

// replaceTempIDToken replaces the temp-id token opening the box on line (the
// `+` and its bare or quoted opaque part) with replacement.
func replaceTempIDToken(line, replacement string) string {
	runes := []rune(line)
	i := strings.IndexRune(line, '[')
	if i < 0 {
		return line
	}
	pos := len([]rune(line[:i])) + 1
	for pos < len(runes) && (runes[pos] == ' ' || runes[pos] == '\t') {
		pos++
	}
	if pos >= len(runes) || runes[pos] != '+' {
		return line
	}
	start, end := pos, pos+1
	switch {
	case end < len(runes) && (runes[end] == '"' || runes[end] == '\''):
		quote := runes[end]
		end++
		for end < len(runes) && runes[end] != quote {
			if runes[end] == '\\' {
				end++
			}
			end++
		}
		end++
	default:
		for end < len(runes) && trellis.IsIdentRuneAt(runes, end) {
			end++
		}
	}
	if end > len(runes) {
		end = len(runes)
	}
	return string(runes[:start]) + replacement + string(runes[end:])
}
