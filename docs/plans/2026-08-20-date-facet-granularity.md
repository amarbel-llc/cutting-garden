# Prefix-granular date facets (#230) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use eng:subagent-driven-development to implement this plan task-by-task.

**Goal:** `date_start`/`date_due` become groupable, prefix-granular facet
dimensions (`--group-by date_due:month`, `--filter date_start=2026-08`),
replacing the `year`/`month` dimensions entirely.

**Architecture:** Per the approved design
(`docs/plans/2026-08-20-date-facet-granularity-design.md`): a new
`FacetKind` **`date`** whose ISO-day bucket keys coarsen by pure string
prefix truncation (framework-owned, no calendar knowledge); the caldav date
codecs' fields turn `Groupable` and their `Parse` dispatches on bucket shape
(year/month/day splice); summaries lift date dimensions at fixed month
granularity; filters prefix-match by validated value shape; organize carries
the granularity in the document's dimension heading (`date_due:month=`) so
apply coarsens live values identically without consulting config.

**Tech Stack:** Go (SDK `internal/cutting_garden_plugins` + derived
`pkgs/` facade via dagnabit), caldav plugin, `internal/organize`,
`internal/cgconfig` (tommy codegen), bats.

**Rollback:** No dual period (explicit user decision): the slice lands as one
merge; rollback is `git revert` of that merge commit + a re-merge. All known
`year`/`month` consumers are in-repo.

**Conventions (read first):**
- Run package tests with `just debug-test-pkg PKG=<pkg>` (e.g.
  `PKG=./plugins/caldav/`). Never run bare `go test` outside the devshell.
- After ANY edit under `internal/cutting_garden_plugins/`, regenerate the
  public facade: `just codemod-generate-dagnabit` (expect only
  `pkgs/cutting_garden_plugins/main.go` to change; commit stray stamp churn
  in other facades separately). caldav imports the FACADE
  (`code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins`), so it
  will not compile against new SDK symbols until the regen runs.
- After editing `internal/cgconfig/config.go`'s `//go:generate` struct, run
  `just codemod-generate` (regenerates `*_tommy.go`).
- Commits are gpg-signed automatically; if signing fails, STOP and ask the
  user to unlock piggy-agent.
- Do NOT run full `just` — the merge gate runs it.

---

### Task 1: SDK — the `date` facet kind, granularity type, prefix helpers

**Files:**
- Modify: `internal/cutting_garden_plugins/facet.go` (the `FacetKind` consts, ~line 34)
- Create: `internal/cutting_garden_plugins/facet_date.go`
- Test: `internal/cutting_garden_plugins/facet_date_test.go`

**Step 1: Write the failing test**

Create `internal/cutting_garden_plugins/facet_date_test.go`:

```go
package cutting_garden_plugins

import "testing"

// TruncateDateKey coarsens an ISO-day bucket key by pure prefix truncation —
// the framework's whole share of the date hierarchy (no calendar knowledge).
func TestTruncateDateKey(t *testing.T) {
	cases := []struct {
		key  string
		g    DateGranularity
		want string
	}{
		{"2026-08-15", GranularityDay, "2026-08-15"},
		{"2026-08-15", GranularityMonth, "2026-08"},
		{"2026-08-15", GranularityYear, "2026"},
		{"2026-08", GranularityMonth, "2026-08"}, // already coarse: unchanged
		{"2026-08", GranularityYear, "2026"},
		{"2026", GranularityMonth, "2026"}, // coarser than asked: unchanged
	}
	for _, c := range cases {
		if got := TruncateDateKey(c.key, c.g); got != c.want {
			t.Errorf("TruncateDateKey(%q, %q) = %q, want %q", c.key, c.g, got, c.want)
		}
	}
}

// ParseDateBucket classifies a filter/bucket value by its validated shape —
// the granularity a prefix filter or coarse bucket move addresses.
func TestParseDateBucket(t *testing.T) {
	cases := []struct {
		in string
		g  DateGranularity
		ok bool
	}{
		{"2026", GranularityYear, true},
		{"2026-08", GranularityMonth, true},
		{"2026-08-15", GranularityDay, true},
		{"202608", "", false},
		{"2026-13", "", false},    // month out of range
		{"2026-08-32", "", false}, // day out of range
		{"2026-8", "", false},
		{"garbage", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		g, ok := ParseDateBucket(c.in)
		if g != c.g || ok != c.ok {
			t.Errorf("ParseDateBucket(%q) = (%q, %t), want (%q, %t)", c.in, g, ok, c.g, c.ok)
		}
	}
}

// ParseDateGranularity resolves the --group-by suffix / config spelling.
func TestParseDateGranularity(t *testing.T) {
	for _, s := range []string{"year", "month", "day"} {
		if _, ok := ParseDateGranularity(s); !ok {
			t.Errorf("ParseDateGranularity(%q): want ok", s)
		}
	}
	for _, s := range []string{"week", "", "MONTH", "days"} {
		if _, ok := ParseDateGranularity(s); ok {
			t.Errorf("ParseDateGranularity(%q): want !ok", s)
		}
	}
}
```

**Step 2: Run it to verify it fails**

Run: `just debug-test-pkg PKG=./internal/cutting_garden_plugins/`
Expected: FAIL — `undefined: DateGranularity` etc.

**Step 3: Implement**

In `facet.go`, add to the `FacetKind` const block (after `FacetLabelled`):

```go
	// FacetDate is a calendar-date dimension: bucket keys are ISO days
	// ("2026-08-15"), chronologically ordered, and PREFIX-COARSENABLE — the
	// year ("2026") and month ("2026-08") buckets are string prefixes of the
	// day key, so consumers coarsen by TruncateDateKey with no calendar
	// knowledge, and filters prefix-match by validated shape (see
	// FacetFilter.Validate). Introduced for cutting-garden#230.
	FacetDate FacetKind = "date"
```

Create `facet_date.go`:

```go
package cutting_garden_plugins

// Prefix-granularity support for FacetDate dimensions (cutting-garden#230).
// The framework owns ONLY string prefix truncation over ISO-day bucket keys
// ("2026" ⊂ "2026-08" ⊂ "2026-08-15"); the plugin owns the values and the
// writes. Any plugin declaring a date-kind dimension gets granularity
// grouping and prefix filtering from these helpers.

// DateGranularity is a bucket coarseness for a FacetDate dimension: the
// --group-by suffix spelling ("date_due:month") and the config default.
type DateGranularity string

const (
	GranularityYear  DateGranularity = "year"  // "2026"
	GranularityMonth DateGranularity = "month" // "2026-08"
	GranularityDay   DateGranularity = "day"   // "2026-08-15" (the identity)
)

// granularityLen is each granularity's key length — the whole hierarchy.
var granularityLen = map[DateGranularity]int{
	GranularityYear:  len("2026"),
	GranularityMonth: len("2026-08"),
	GranularityDay:   len("2026-08-15"),
}

// ParseDateGranularity resolves a granularity spelling. Strict lowercase —
// a typo rejects loudly at the CLI/config layer.
func ParseDateGranularity(s string) (DateGranularity, bool) {
	g := DateGranularity(s)
	_, ok := granularityLen[g]
	return g, ok
}

// TruncateDateKey coarsens a date bucket key to the granularity by prefix
// truncation. A key already at or coarser than the granularity is unchanged.
func TruncateDateKey(key string, g DateGranularity) string {
	n := granularityLen[g]
	if n == 0 || len(key) <= n {
		return key
	}
	return key[:n]
}

// ParseDateBucket classifies a value by its date-bucket shape — YYYY,
// YYYY-MM, or YYYY-MM-DD with in-range month/day — reporting the granularity
// it addresses. ok == false for any other shape: the loud-rejection gate for
// prefix filters and coarse bucket moves.
func ParseDateBucket(v string) (DateGranularity, bool) {
	switch len(v) {
	case granularityLen[GranularityYear]:
		if allDateDigits(v) {
			return GranularityYear, true
		}
	case granularityLen[GranularityMonth]:
		if allDateDigits(v[:4]) && v[4] == '-' && inRange(v[5:7], "01", "12") {
			return GranularityMonth, true
		}
	case granularityLen[GranularityDay]:
		if allDateDigits(v[:4]) && v[4] == '-' &&
			inRange(v[5:7], "01", "12") && v[7] == '-' && inRange(v[8:10], "01", "31") {
			return GranularityDay, true
		}
	}
	return "", false
}

func allDateDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// inRange reports lo <= s <= hi for equal-length digit strings.
func inRange(s, lo, hi string) bool {
	return allDateDigits(s) && s >= lo && s <= hi
}
```

**Step 4: Run tests** — expect PASS.

**Step 5: Commit**

```bash
git add internal/cutting_garden_plugins/facet.go internal/cutting_garden_plugins/facet_date.go internal/cutting_garden_plugins/facet_date_test.go
git commit -m "feat(sdk): FacetDate kind + prefix-granularity helpers (#230)"
```

---

### Task 2: SDK — prefix filter matching for date dimensions

**Files:**
- Modify: `internal/cutting_garden_plugins/facet.go` — `FacetPredicate` (~line 208), `FacetFilter.Matches` (~line 220), `FacetFilter.Validate` (~line 277)
- Test: `internal/cutting_garden_plugins/facet_test.go` (append)

**Context:** `Validate(dims)` is the only place the schema is in hand, so it
does double duty: it VALIDATES a date predicate's value shape and ANNOTATES
the predicate for prefix matching. `Matches` stays schema-free. Every
consumer already calls `Validate` before `Matches` — verify with
`rg '\.Validate\(dims|filter\.Validate'` (known sites:
`internal/mcp/tools.go:1027`, `internal/mcp/resources.go:544`,
`internal/traversal_serve/server.go` near the declared-facets read, and the
`list` command — find it via `rg 'Validate' internal/list internal/command*`;
if the `list --filter` path does NOT validate, add the call there in this
task). An unvalidated filter degrades to exact matching — safe.

**Step 1: Failing test** (append to `facet_test.go`):

```go
// A date-kind predicate prefix-matches by validated shape: =2026 matches the
// year, =2026-08 the month, =2026-08-15 the day; a malformed shape rejects at
// Validate. Non-date dimensions keep exact matching.
func TestFacetFilter_DatePrefixMatching(t *testing.T) {
	dims := []NodeTypeFacets{{Tag: "t", Dimensions: []FacetDimension{
		{Key: "date_start", Kind: FacetDate},
		{Key: "status", Kind: FacetCategorical},
	}}}
	facets := map[string][]FacetValue{
		"date_start": {{Key: "2026-08-15"}},
		"status":     {{Key: "2026"}}, // exact-match control
	}

	for _, val := range []string{"2026", "2026-08", "2026-08-15"} {
		f, err := ParseFacetFilter("date_start=" + val)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Validate(dims); err != nil {
			t.Fatalf("Validate(%q): %v", val, err)
		}
		if !f.Matches(facets) {
			t.Errorf("date_start=%q should prefix-match 2026-08-15", val)
		}
	}

	f, _ := ParseFacetFilter("date_start=2026-09")
	if err := f.Validate(dims); err != nil {
		t.Fatal(err)
	}
	if f.Matches(facets) {
		t.Error("date_start=2026-09 must not match 2026-08-15")
	}

	// Malformed shape rejects loudly at Validate.
	f, _ = ParseFacetFilter("date_start=aug-2026")
	if err := f.Validate(dims); err == nil {
		t.Error("malformed date shape must fail Validate")
	}

	// Non-date dimension: exact only ("2026" must not prefix-match "2026-…").
	f, _ = ParseFacetFilter("status=202")
	if err := f.Validate(dims); err != nil {
		t.Fatal(err)
	}
	if f.Matches(facets) {
		t.Error("categorical predicate must stay exact-match")
	}
}
```

**Step 2: Run** — FAIL (Validate passes malformed date; prefix never matches).

**Step 3: Implement.** In `facet.go`:

Add to `FacetPredicate`:

```go
	// prefixMatch is set by Validate when Dimension is a FacetDate kind: the
	// predicate then matches any bucket key the (shape-validated) Value is a
	// hierarchy prefix of ("2026-08" matches "2026-08-15"). Unexported and
	// derived per side from the declared schema — it never crosses a wire.
	// An unvalidated filter (dims unknown) degrades to exact matching.
	prefixMatch bool
```

In `Validate`, inside the predicate loop after the dimension lookup succeeds
(note: `f` is a slice, so `f[i]` mutation through the value receiver reaches
the caller's backing array — iterate by index, replacing the current
`for _, pred := range f`):

```go
		if dim.Kind == FacetDate {
			if _, ok := ParseDateBucket(f[i].Value); !ok {
				return fmt.Errorf(
					"filter value %q is not a date bucket for dimension %q; "+
						"expected YYYY, YYYY-MM, or YYYY-MM-DD",
					f[i].Value, f[i].Dimension,
				)
			}
			f[i].prefixMatch = true
		}
```

Replace `containsFacetValue` usage in `Matches` with a predicate-aware check:

```go
func (f FacetFilter) Matches(facets map[string][]FacetValue) bool {
	for _, pred := range f {
		if !pred.matches(facets[pred.Dimension]) {
			return false
		}
	}
	return true
}

// matches reports whether any of the node's values satisfies the predicate —
// exact equality, or (for a Validate-annotated date predicate) hierarchy-
// prefix containment: the value must extend the predicate at a "-" boundary,
// so "2026-08" matches "2026-08-15" but never a hypothetical "2026-081".
func (p FacetPredicate) matches(values []FacetValue) bool {
	for _, v := range values {
		if v.Key == p.Value {
			return true
		}
		if p.prefixMatch && len(v.Key) > len(p.Value) &&
			strings.HasPrefix(v.Key, p.Value) && v.Key[len(p.Value)] == '-' {
			return true
		}
	}
	return false
}
```

(Keep `containsFacetValue` — `Validate`'s closed-domain check still uses it.)

**Step 4: Run** `just debug-test-pkg PKG=./internal/cutting_garden_plugins/` — PASS (including all pre-existing Validate/Matches tests).

**Step 5:** Confirm the `list --filter` CLI path validates before matching
(see Context above); add the `Validate` call + a one-line test if missing.

**Step 6: Commit**

```bash
git add internal/cutting_garden_plugins/facet.go internal/cutting_garden_plugins/facet_test.go
git commit -m "feat(sdk): date-kind facet predicates prefix-match by validated shape (#230)"
```

---

### Task 3: SDK — derive `FieldDate` → `FacetDate`; regen facade

**Files:**
- Modify: `internal/cutting_garden_plugins/facet_derive.go` (`facetKindOf`)
- Modify: `internal/cutting_garden_plugins/facet_derive_test.go` (the `facetKindOf(FieldDate)` assertion in `TestDeriveFacetDimensions`)

**Step 1:** In `facet_derive_test.go`, change the existing assertion
`facetKindOf(FieldDate) != FacetNumericBucket` to expect `FacetDate` (update
the message). Run — FAIL.

**Step 2:** In `facetKindOf`, change `case FieldDate: return FacetNumericBucket`
to `return FacetDate` and update the function's doc comment (a date field
groups as a prefix-coarsenable date dimension, cutting-garden#230). Run — PASS.

**Step 3:** Regenerate the facade: `just codemod-generate-dagnabit`.
Expected: only `pkgs/cutting_garden_plugins/main.go` changes (`git status`).

**Step 4: Commit**

```bash
git add internal/cutting_garden_plugins/facet_derive.go internal/cutting_garden_plugins/facet_derive_test.go pkgs/cutting_garden_plugins/main.go
git commit -m "feat(sdk): derive FieldDate fields as FacetDate dimensions (#230)"
```

---

### Task 4: cgconfig — `[organize] date_granularity`

**Files:**
- Modify: `internal/cgconfig/config.go`
- Test: `internal/cgconfig/config_test.go` (create if absent — check first)

**Step 1: Failing test** (in package cgconfig; follow any existing test file's
decode pattern — tommy generates `DecodeConfigV0`; read `config_tommy.go` for
the decode entry point's exact name/signature before writing this):

```go
func TestOrganizeConfig_DateGranularity(t *testing.T) {
	// Valid value decodes.
	cfg, err := <decode>(`[organize]` + "\n" + `date_granularity = "month"` + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Organize.DateGranularity != "month" {
		t.Errorf("DateGranularity = %q, want month", cfg.Organize.DateGranularity)
	}
	// Invalid value fails Validate.
	if _, err := <decode>(`[organize]` + "\n" + `date_granularity = "week"` + "\n"); err == nil {
		t.Error("date_granularity=week must fail validation")
	}
}
```

**Step 2: Run** — FAIL (`cfg.Organize` undefined).

**Step 3: Implement** in `config.go`:

```go
// OrganizeConfig configures the organize command (FDR 0023). Not a plugin
// section — organize is framework-side — so it lives here rather than being
// delegated.
type OrganizeConfig struct {
	// DateGranularity is the default bucket granularity for a bare
	// `--group-by` on a date-kind facet dimension (cutting-garden#230):
	// "year", "month", or "day". Empty means the built-in default (day).
	// A `--group-by dim:granularity` suffix always wins over this.
	DateGranularity string `toml:"date_granularity,omitempty"`
}

func (c OrganizeConfig) Validate() error {
	if c.DateGranularity == "" {
		return nil
	}
	if _, ok := cutting_garden_plugins.ParseDateGranularity(c.DateGranularity); !ok {
		return fmt.Errorf(
			"organize.date_granularity %q is not one of year, month, day",
			c.DateGranularity,
		)
	}
	return nil
}
```

Add `Organize OrganizeConfig `toml:"organize,omitempty"`` to `ConfigV0`,
wire `c.Organize.Validate()` into `ConfigV0.Validate`, and import
`cutting_garden_plugins` — CHECK LAYERING first: cgconfig may import
`internal/cutting_garden_plugins` only if that package does not import
cgconfig (it does not — verify with
`rg 'cgconfig' internal/cutting_garden_plugins/`).

**Step 4:** Regenerate tommy codegen: `just codemod-generate`. Run the
package tests — PASS.

**Step 5: Commit**

```bash
git add internal/cgconfig/
git commit -m "feat(config): [organize] date_granularity default (#230)"
```

---

### Task 5: caldav — date codec `Parse` shape dispatch; groupable date fields; delete year/month

This is the big plugin task; keep the sub-steps in order so each test run is
meaningful.

**Files:**
- Modify: `plugins/caldav/unified.go` (date codec fields + Parse; delete
  `caldavRescheduleCodec`, `activeDateStored`; per-tag sets)
- Modify: `plugins/caldav/facet.go` (constants; `facetsFromView`; delete
  `yearOf`/`monthOf`)
- Modify: `plugins/caldav/facet_apply.go` (`splicePeriod` signature)
- Modify tests: `plugins/caldav/facet_test.go`, `facet_apply_test.go`,
  `facet_write_test.go`, `unified` tests if any reference year/month

**Step 1: Write the failing tests first** (append/replace in the caldav test
files):

In `facet_apply_test.go` — replace the two month-reschedule subtests of
`TestBuildFacetWritePatch` (and their `FacetWrite` literals) with
shape-dispatch coverage through the NEW dimensions:

```go
	t.Run("task month reschedule via date_due", func(t *testing.T) {
		node := applyNode(t, "caldav://h/c/t1.ics", "VTODO", map[string]any{"due": "20260815T143000"})
		w := cutting_garden_plugins.FacetWrite{DimensionKey: "date_due", Mode: cutting_garden_plugins.FacetWriteOne, Field: "due"}
		body, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "2026-09")
		if err != nil {
			t.Fatalf("BuildFacetWritePatch: %v", err)
		}
		var got patch
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if got.Component != "VTODO" || got.Task["due"] != "20260915T143000" {
			t.Errorf("patch = %s, want task.due=20260915T143000", body)
		}
	})

	t.Run("event year reschedule via date_start", func(t *testing.T) {
		node := applyNode(t, "caldav://h/c/e1.ics", "VEVENT", map[string]any{"dtstart": "20260820T100000Z"})
		w := cutting_garden_plugins.FacetWrite{DimensionKey: "date_start", Mode: cutting_garden_plugins.FacetWriteOne, Field: "dtstart"}
		body, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "2027")
		if err != nil {
			t.Fatalf("BuildFacetWritePatch: %v", err)
		}
		var got patch
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if got.Component != "VEVENT" || got.Event["dtstart"] != "20270820T100000Z" {
			t.Errorf("patch = %s, want event.dtstart=20270820T100000Z", body)
		}
	})

	t.Run("event day reschedule via date_start preserves clock", func(t *testing.T) {
		node := applyNode(t, "caldav://h/c/e1.ics", "VEVENT", map[string]any{"dtstart": "20260820T100000Z"})
		w := cutting_garden_plugins.FacetWrite{DimensionKey: "date_start", Mode: cutting_garden_plugins.FacetWriteOne, Field: "dtstart"}
		body, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "2026-09-03")
		if err != nil {
			t.Fatalf("BuildFacetWritePatch: %v", err)
		}
		var got patch
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if got.Event["dtstart"] != "20260903T100000Z" {
			t.Errorf("patch = %s, want event.dtstart=20260903T100000Z", body)
		}
	})

	t.Run("date move on a property-less object rejects", func(t *testing.T) {
		node := applyNode(t, "caldav://h/c/t2.ics", "VTODO", map[string]any{"due": "20260815"})
		w := cutting_garden_plugins.FacetWrite{DimensionKey: "date_start", Mode: cutting_garden_plugins.FacetWriteOne, Field: "dtstart"}
		if _, err := (Plugin{}).BuildFacetWritePatch(context.Background(), node, w, "2026-09"); err == nil {
			t.Error("date_start move on a DTSTART-less task must reject (group by date_due instead)")
		}
	})
```

In `facet_test.go` — replace `TestDescribeFacets_DeclaresObjectDimensions`'s
year/month/due-band expectations: `date_start` and `date_due` are declared
`FacetDate` on VTODO; `date_start` (not `date_due`) on VEVENT/VJOURNAL;
`year`/`month` are declared NOWHERE. Delete `TestMonthOf`. Add a
`facetsFromView`-level (or FacetCounts-level, matching the existing style)
test pinning day-precise emission and the month lift:

```go
// Facet values are day-precise per property; the SUMMARY lifts date
// dimensions at fixed month granularity (design 2026-08-20 §6).
func TestFacetCounts_DateDimensionsMonthLift(t *testing.T) {
	f := newFakeCalDAV()
	f.seed("/dav/cal/e1.ics", "VEVENT",
		veventFull("e1", "Standup", "CONFIRMED", "20260224T150000Z"))
	f.seed("/dav/cal/t1.ics", "VTODO",
		"BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:t1\nSUMMARY:Due only\n"+
			"STATUS:NEEDS-ACTION\nDUE:20260101\nEND:VTODO\nEND:VCALENDAR\n")

	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	node := mustParseURL(t, "caldav:"+srv.URL+"/dav/")

	result, ok, err := Plugin{}.FacetCounts(context.Background(), node, nil)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	assertCount(t, result.Summary, "date_start", "2026-02", 1) // month key, not day
	assertCount(t, result.Summary, "date_due", "2026-01", 1)
	if _, present := result.Summary["date_start"]["2026-02-24"]; present {
		t.Error("summary must not carry day-granularity buckets")
	}
	if _, present := result.Summary["year"]; present {
		t.Error("the year dimension is retired")
	}
	if _, present := result.Summary["date_start"]["2026-01"]; present {
		t.Error("a DUE-only task contributes no date_start (per-property, no fallback)")
	}
}
```

In `facet_write_test.go` — `TestDescribeFacetWrites_ConsistentAndDeclared`:
replace `facetYear, facetMonth` in the write:one loop with
`"date_start", "date_due"`.

**Step 2: Run** `just debug-test-pkg PKG=./plugins/caldav/` — FAIL
(unknown dimensions, year/month still declared).

**Step 3: Implement**, in this order:

a. `facet.go` constants: delete `facetYear`/`facetMonth`; add

```go
	facetDateStart = "date_start" // the object's DTSTART day bucket (#230)
	facetDateDue   = "date_due"   // a task's DUE day bucket (#230)
```

b. `facet_apply.go`: change `splicePeriod(value, dimension, bucket string)`
to `splicePeriod(value string, g cutting_garden_plugins.DateGranularity, bucket string)`;
its switch becomes `case cutting_garden_plugins.GranularityMonth` /
`GranularityYear` (bucket-shape validation there can now delegate to the
splice's existing splitYearMonth/allDigits checks — keep them). Update
`TestSplicePeriod`/`TestSplicePeriod_BadBucket` call sites to pass
granularities.

c. `unified.go` date codec `Parse` — shape dispatch (replaces the current
body's date handling):

```go
func (c caldavDateCodec) Parse(
	edited map[string][]string, current map[string]any,
) (map[string]any, error) {
	cur := stringOf(current, c.storedKey)
	acc := &dateTimeEdit{}
	if v, ok := edited["date_"+c.suffix]; ok && len(v) > 0 {
		g, ok := cutting_garden_plugins.ParseDateBucket(v[0])
		if !ok {
			return nil, errors.BadRequestf(
				"%s %q is not YYYY, YYYY-MM, or YYYY-MM-DD", "date_"+c.suffix, v[0],
			)
		}
		if g == cutting_garden_plugins.GranularityDay {
			acc.date, acc.hasDate = v[0], true
		} else {
			// A coarse bucket (a --group-by date_*:month/year move, or a
			// hand-typed coarse atom edit) period-splices, preserving the
			// finer components and the clock.
			spliced, err := splicePeriod(cur, g, v[0])
			if err != nil {
				return nil, err
			}
			cur = spliced
		}
	}
	if v, ok := edited["time_"+c.suffix]; ok && len(v) > 0 {
		acc.clock, acc.hasClock = v[0], true
	}
	spliced, err := spliceDateTime(cur, acc)
	if err != nil {
		return nil, errors.Wrapf(err, "caldav plugin: %s", c.storedKey)
	}
	return map[string]any{c.storedKey: spliced}, nil
}
```

(Note `spliceDateTime(cur, empty-acc)` normalizes a coarse-splice-only result
harmlessly — splicePeriod already emits the compact form.)

d. `unified.go` field declarations: on `caldavDateCodec.Fields()`, the
`date_` field gains `Groupable: c.groupable` — add a `groupable bool` struct
field set TRUE on the `dateStart` and `dateDue` instances, FALSE on `dateEnd`
(read-only end never groups). Add a `CompletionHint` on the groupable date
fields: `"reschedule-by-move: preserves the object's clock time and time zone"`.
The `time_` field stays inline-only. Source stays `c.storedKey` (the write
target — now honest: each dimension writes its own property).

e. `unified.go` per-tag sets: delete `caldavRescheduleCodec` (type, Fields,
Format, Parse) and `activeDateStored`; remove the `caldavRescheduleCodec{...}`
entries from all three sets. Set order stays otherwise identical — the
groupable date fields now project dimensions in DATE-CODEC position, so the
derived dimension order becomes: VTODO `component, date_start, date_end?…` —
NO: `date_end` is not groupable, so VTODO derives
`component, date_start, date_due, status, due_band, timezone, priority`;
VEVENT `component, date_start, status, timezone`; VJOURNAL
`component, date_start, status`. This order change is fine (nothing pins
dimension order; describe output changes are expected in this slice).

f. `facet.go` `facetsFromView`: delete the year/month emission block and the
task `firstNonEmpty(DtStart, Due)` primary-date variable use for it; add:

```go
	// Per-property day buckets (#230): date_start from DTSTART (any
	// component), date_due from a task's DUE — no cross-property fallback;
	// a DUE-only task simply has no date_start. due_band keeps its own
	// DUE-then-DTSTART fallback (unchanged; it answers a different question).
	if key, order := dayBucketOf(dtstartOf(view)); key != "" {
		facets[facetDateStart] = []cutting_garden_plugins.FacetValue{{Key: key, Order: order}}
	}
	if view.Task != nil {
		if key, order := dayBucketOf(view.Task.Due); key != "" {
			facets[facetDateDue] = []cutting_garden_plugins.FacetValue{{Key: key, Order: order}}
		}
	}
```

with a small `dtstartOf(view objectView) string` (the component switch that
already exists for status/date — reuse/adapt it; the `status` extraction
stays) and:

```go
// dayBucketOf extracts the ISO-day bucket of an iCalendar date-time
// ("20260224T150000Z" or "2026-02-24" → key "2026-02-24", order 20260224).
// Empty key when the value has no valid leading YYYYMMDD.
func dayBucketOf(date string) (key string, order int64) {
	var digits strings.Builder
scan:
	for _, r := range date {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
			if digits.Len() == 8 {
				break scan
			}
		case r == '-':
			// tolerate a hyphenated date prefix
		default:
			break scan
		}
	}
	if digits.Len() < 8 {
		return "", 0
	}
	s := digits.String()
	if s[4:6] < "01" || s[4:6] > "12" || s[6:8] < "01" || s[6:8] > "31" {
		return "", 0
	}
	order, _ = strconv.ParseInt(s, 10, 64)
	return s[:4] + "-" + s[4:6] + "-" + s[6:8], order
}
```

Delete `yearOf` and `monthOf` (and `firstNonEmpty` if now unused — check
`rg firstNonEmpty plugins/caldav/`).

g. `facet.go` month lift: in `liftFacets`, coarsen date dimensions:

```go
// dateDimensions names the FacetDate-kind dimensions whose SUMMARY buckets
// lift at fixed month granularity (design 2026-08-20 §6) — day-precise
// per-node values would mean one summary bucket per distinct day. Grouping
// and filtering stay day-precise on the per-node values.
var dateDimensions = map[string]bool{facetDateStart: true, facetDateDue: true}
```

and in the `liftFacets` inner loop, before counting:

```go
			key := v.Key
			if dateDimensions[dim] {
				key = cutting_garden_plugins.TruncateDateKey(key, cutting_garden_plugins.GranularityMonth)
			}
			hist[key]++
```

**Step 4: Run** `just debug-test-pkg PKG=./plugins/caldav/` — PASS. Also run
`just debug-test-pkg PKG=./internal/cutting_garden_plugins/` (unchanged —
sanity).

**Step 5: Commit**

```bash
git add plugins/caldav/ internal/cutting_garden_plugins/  # only if SDK untouched, drop the second path
git commit -m "feat(caldav): date_start/date_due groupable, year/month retired (#230)"
```

---

### Task 6: organize — `dim[:granularity]` group-by, coarsening, round-trip

**Files:**
- Modify: `internal/organize/organize.go` (GroupBy flag docs, ~line 55)
- Modify: `internal/organize/generate.go` (`buildAndStore`, `buildDocument`,
  `dimensionSections`, `writableBuckets` callers)
- Modify: `internal/organize/group.go` (`groupNodes` — coarsen at
  `values := n.Facets[dim]`, line ~38)
- Modify: `internal/organize/apply.go` (dimension resolution from the parsed
  document + `liveAsg` coarsening, line ~241)
- Modify: `internal/organize/document.go` + its parser (the `<dim>=` heading
  term now optionally spells `<dim>:<granularity>=`) — READ the term
  parse/render first (`rg 'Term|dimension' internal/organize/document.go
  internal/organize/parse*.go`) and mirror its existing style.
- Test: `internal/organize/*_test.go` (fake lister with a date dimension —
  follow `terminal_test.go`'s `fakeLister` pattern)

**Design invariant:** the granularity is carried IN THE DOCUMENT ITSELF
(heading term `date_due:month=`; provenance echoes the full spelling), so
`--apply` coarsens live day-values identically WITHOUT consulting config —
config may change between generate and apply. Bare date group-bys resolve
config-then-day AT GENERATE TIME and are then persisted explicitly.

**Step 1: Failing tests.** Add an organize unit test (package-internal, fake
lister declaring `date_due` as `FacetDate` with day-precise node facets):

1. `buildDocument` with groupBy `date_due:month` buckets two nodes with
   facets `2026-08-15`/`2026-08-20` under ONE `=2026-08` heading, term
   `date_due:month=`.
2. groupBy `date_due` (bare, no config) buckets them under two day headings.
3. A granularity suffix on a non-date dimension (`status:month`) is a bad
   request naming the dimension's kind.
4. An unknown granularity (`date_due:week`) is a bad request listing
   year/month/day.
5. Apply-side: with a base/edited document grouped `date_due:month=` and a
   live node whose facet is `2026-08-15`, an UNMOVED line yields no move and
   no conflict (the live day value coarsens to `2026-08` for comparison);
   moving the line under `=2026-09` yields a move with `to == "2026-09"`.
   (Follow the existing apply test harness — find it via
   `rg 'func Test.*[Aa]pply' internal/organize/`.)

**Step 2: Run** — FAIL.

**Step 3: Implement:**

a. A `groupSpec` in `internal/organize` (new small file `groupspec.go`):

```go
// groupSpec is a parsed --group-by / document-heading dimension spelling:
// the facet dimension plus, for a FacetDate dimension, the bucket
// granularity (cutting-garden#230). Non-date dimensions never carry one.
type groupSpec struct {
	Dim         string
	Granularity cgp.DateGranularity // "" for a non-date dimension
}

// String renders the canonical spelling ("date_due:month", or just the
// dimension) — the document heading term and provenance form.
func (s groupSpec) String() string {
	if s.Granularity == "" {
		return s.Dim
	}
	return s.Dim + ":" + string(s.Granularity)
}

// parseGroupSpec resolves a spelling against the plugin's declared schema:
// a `dim:granularity` suffix is legal only on a FacetDate dimension; a bare
// date dimension takes configDefault, then day. dims may be nil (no
// FacetDescriber) — then any suffix is rejected (no schema says it's a date).
func parseGroupSpec(
	spelling string, dims []cgp.NodeTypeFacets, configDefault string,
) (groupSpec, error) {
	dim, suffix, hasSuffix := strings.Cut(spelling, ":")
	d, declared := findDim(dims, dim) // small helper over NodeTypeFacets
	if hasSuffix {
		g, ok := cgp.ParseDateGranularity(suffix)
		if !ok {
			return groupSpec{}, errors.BadRequestf(
				"organize: granularity %q is not one of year, month, day", suffix)
		}
		if !declared || d.Kind != cgp.FacetDate {
			return groupSpec{}, errors.BadRequestf(
				"organize: dimension %q is not a date dimension; a :granularity suffix applies only to date dimensions", dim)
		}
		return groupSpec{Dim: dim, Granularity: g}, nil
	}
	if declared && d.Kind == cgp.FacetDate {
		if g, ok := cgp.ParseDateGranularity(configDefault); ok {
			return groupSpec{Dim: dim, Granularity: g}, nil
		}
		return groupSpec{Dim: dim, Granularity: cgp.GranularityDay}, nil
	}
	return groupSpec{Dim: dim}, nil
}
```

b. `buildAndStore`: resolve the spec once
(`parseGroupSpec(cmd.GroupBy, describedFacets(lister), cmd.dateGranularityDefault)`)
where `describedFacets` probes `cgp.FacetDescriber` (nil-tolerant) and the
config default is loaded the same way the command already loads config —
find how the Organize command reaches `cgconfig` (check
`internal/organize/organize.go` + `command_components.LoadConfig`; add the
load if absent, mirroring another command's usage). Thread the SPEC (not the
raw string) through `buildDocument`/`provenance`/`dimensionSections`.

c. `groupNodes`: accept the spec; coarsen each value key before bucketing:

```go
		values := n.Facets[spec.Dim]
		…
		for _, v := range values {
			key := v.Key
			if spec.Granularity != "" {
				key = cgp.TruncateDateKey(key, spec.Granularity)
			}
			byValue[key] = append(byValue[key], ln)
		}
```

d. `dimensionSections`/document render: the dimension heading term is
`spec.String() + "="`.

e. Document PARSE + apply: where the parser extracts the dimension from the
`<dim>=` term, split on `:` into (dim, granularity) — validate the
granularity spelling (reject unknown), keep both. In `apply.go`, coarsen the
live assignment (line ~241):

```go
		liveAsg[key] = coarsenBucket(firstFacetKey(n.Facets[dim]), gran)
```

with `coarsenBucket(v string, g cgp.DateGranularity) string` returning `v`
untouched for `g == ""`. The write lookup (`DescribeFacetWrites` match,
apply.go ~308) uses the BARE dim.

f. `writableBuckets` callers pass `spec.Dim` (date dims declare no write
Values — nil, no pre-rendered headings — unchanged behavior).

**Step 4: Run** `just debug-test-pkg PKG=./internal/organize/` — PASS.

**Step 5: Commit**

```bash
git add internal/organize/
git commit -m "feat(organize): --group-by dim:granularity with document round-trip coarsening (#230)"
```

---

### Task 7: bats — rewrite the month lane; add day + config-default lanes

**Files:**
- Modify: `zz-tests_bats/organize_month.bats` (rewrite onto the new spelling)
- Grep first: `rg -l 'group-by (month|year)|"year"|"month"' zz-tests_bats/`
  and fix EVERY hit (mcp describe/read_facets snapshots may pin the old
  dimensions too — `rg 'facetYear|"year"|"month"' zz-tests_bats/`).

**Steps:**
1. Read `zz-tests_bats/organize_month.bats` end to end. Rewrite its lanes:
   `--group-by month` → `--group-by date_due:month` (task fixtures) /
   `date_start:month` (event fixtures); heading expectations change from
   `# month=` to `# date_due:month=` (match the document dialect the organize
   unit tests pinned); bucket values (`## =2026-09`) are unchanged; the
   reschedule-by-move apply lanes keep their write assertions (DUE/DTSTART
   splice unchanged).
2. Add one lane: bare `--group-by date_due` groups by exact day
   (`## =2026-08-15` headings).
3. Add one lane: with `date_granularity = "month"` under `[organize]` in the
   test config (find how these bats write their config.toml —
   `rg 'XDG_CONFIG_HOME|config.toml' zz-tests_bats/organize*.bats` — and
   mirror it), a bare `--group-by date_due` groups by month.
4. Add a filter lane (wherever `list --filter`/read_facets is exercised —
   check `zz-tests_bats/*facet*`): `--filter date_start=2026` matches, and a
   malformed `date_start=aug` fails loudly.
5. Run ONLY the touched bats files if a recipe exists for single-file bats
   (`just --list | grep bats`); otherwise leave the full lane to the merge
   gate but run the organize unit + caldav package tests again.
6. Commit: `git commit -m "test(bats): organize date-granularity lanes replace month lane (#230)"`.

---

### Task 8: docs — FDR 0025 matrix + status promotion; close #230

**Files:**
- Modify: `docs/features/0025-unified-field-codec.md`

**Steps:**
1. Migration matrix: the date row's Groupable/Write columns now read
   "✅ same date codec, prefix-granular (`FacetDate`)"; delete the
   `caldavRescheduleCodec` mentions from the Option B narrative (note the
   dissolution); note `year`/`month` retired.
2. The FDR's promotion criteria name `--group-by date_start` (#230) as the
   net-new capability gating `testing`: flip the front-matter
   `status: proposed` → `status: testing` and note the promotion (date +
   "delivered #230 end to end against the caldav testserver").
3. Commit with `Closes #230` in the message:
   `git commit -m "docs(fdr): FDR 0025 → testing; date facets land (#230)" -m "Closes #230. The unified codec model delivered its net-new capability end to end: prefix-granular date grouping/filtering against the caldav testserver."`

---

### Task 9: merge

1. Attest via `mcp__spinclass__nothing-but-the-truth` (simplify / review /
   eng:loose-ends / eng:doc-drift / eng:pii-review — run a `/code-review` on
   the accumulated diff first and fix or file its findings).
2. `mcp__spinclass__merge-this-session-async`; the gate runs build + full
   tests + bats.
3. On a gate failure, the failure IS the signal — investigate from the job
   log (`job_read merge-<id>`), fix, re-attest, re-merge.
