package capture_receipt

import (
	"fmt"
	"io/fs"
	"runtime"
	"testing"

	"github.com/amarbel-llc/madder/go/pkgs/markl"
)

// benchIndexEntry mirrors the in-memory shape proposed by issue #80's
// streaming-writer plan: just the sort key plus an (offset, length)
// reference into a disk-backed spool. Defined here, not as part of the
// public API, so the benchmark can compare retention apples-to-apples
// without committing to the streaming implementation.
type benchIndexEntry struct {
	root, path string
	offset     int64
	length     uint32
}

// makeBenchEntries builds n synthetic Entry values shaped like a
// realistic file capture: ~50-char path, short root, and a real
// markl-id string produced by hashing unique content per entry via
// the same FormatHashSha256 path the production blob writers use.
//
// Important: the Id is intentionally NOT repooled. Production gets
// its Id from BlobWriter.GetMarklId(), which returns no repool
// handle — the Entry owns the Id for its lifetime. Mirroring that
// here keeps the per-entry retention measurement honest. The hash
// IS repooled because the next iteration borrows a fresh hash from
// the same pool slot.
func makeBenchEntries(n int) []EntryV1 {
	out := make([]EntryV1, n)
	for i := 0; i < n; i++ {
		h, hRepool := markl.FormatHashSha256.Get()
		_, _ = h.Write([]byte(fmt.Sprintf("blob-content-%d", i)))
		id, _ := h.GetMarklId() //repool:owned // Entry retains id for its lifetime; see func doc
		blobId := id.String()
		hRepool()

		out[i] = EntryV1{
			Path:   fmt.Sprintf("internal/foxtrot/blob_stores/store_local_%06d.go", i),
			Root:   "go",
			Type:   TypeFile,
			Mode:   fs.FileMode(0o644),
			Size:   int64(1234 + i),
			BlobId: blobId,
		}
	}
	return out
}

func makeBenchIndex(n int) []benchIndexEntry {
	out := make([]benchIndexEntry, n)
	for i := 0; i < n; i++ {
		out[i] = benchIndexEntry{
			root:   "go",
			path:   fmt.Sprintf("internal/foxtrot/blob_stores/store_local_%06d.go", i),
			offset: int64(i * 250),
			length: 250,
		}
	}
	return out
}

// retainedHeapBytes returns the steady-state heap delta after `build`
// runs and the GC has settled. KeepAlive ensures the slice survives
// until after the second snapshot.
func retainedHeapBytes(build func() any) uint64 {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	v := build()

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	runtime.KeepAlive(v)

	if after.HeapAlloc < before.HeapAlloc {
		return 0
	}
	return after.HeapAlloc - before.HeapAlloc
}

// BenchmarkAccumulatorRetention measures the heap held by the existing
// in-memory `[]Entry` accumulator in capture.Run. This is the
// memory footprint #80's streaming refactor would relieve.
func BenchmarkAccumulatorRetention(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				retained := retainedHeapBytes(func() any {
					return makeBenchEntries(n)
				})
				b.ReportMetric(float64(retained), "retained_bytes")
				b.ReportMetric(float64(retained)/float64(n), "bytes/entry")
			}
		})
	}
}

// BenchmarkIndexRetention measures the heap held by the proposed
// `[]indexEntry` slice — sort key only, with offset/length references
// to a disk-backed spool. Compare against BenchmarkAccumulatorRetention
// to validate the savings #80 promises.
func BenchmarkIndexRetention(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				retained := retainedHeapBytes(func() any {
					return makeBenchIndex(n)
				})
				b.ReportMetric(float64(retained), "retained_bytes")
				b.ReportMetric(float64(retained)/float64(n), "bytes/entry")
			}
		})
	}
}
