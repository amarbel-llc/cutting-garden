package capture_sink

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
)

func TestNDJSON_FileEntry(t *testing.T) {
	var out, errOut bytes.Buffer
	s := NewNDJSON(&out, &errOut)

	s.SetStore("storeA")
	s.Entry(capture_receipt.EntryV1{
		Path:   "foo.go",
		Root:   "./src",
		Type:   capture_receipt.TypeFile,
		Mode:   0o644,
		Size:   1234,
		BlobId: "blake3:abc",
	})
	s.Finalize()

	const want = `{"path":"foo.go","root":"./src","type":"file","mode":"0644","size":1234,"blob_id":"blake3:abc","store":"storeA"}` + "\n"
	if out.String() != want {
		t.Fatalf("file entry mismatch\ngot:  %q\nwant: %q", out.String(), want)
	}
	if errOut.Len() != 0 {
		t.Fatalf("nothing should go to errOut for entries; got %q", errOut.String())
	}
}

func TestNDJSON_DirEntry(t *testing.T) {
	var out, errOut bytes.Buffer
	s := NewNDJSON(&out, &errOut)

	s.SetStore("storeA")
	s.Entry(capture_receipt.EntryV1{
		Path: ".",
		Root: "./src",
		Type: capture_receipt.TypeDir,
		Mode: 0o755,
	})
	s.Finalize()

	const want = `{"path":".","root":"./src","type":"dir","mode":"0755","store":"storeA"}` + "\n"
	if out.String() != want {
		t.Fatalf("dir entry mismatch\ngot:  %q\nwant: %q", out.String(), want)
	}
}

func TestNDJSON_SymlinkEntry_DefaultStoreOmitsField(t *testing.T) {
	var out, errOut bytes.Buffer
	s := NewNDJSON(&out, &errOut)

	s.Entry(capture_receipt.EntryV1{
		Path:   "link",
		Root:   ".",
		Type:   capture_receipt.TypeSymlink,
		Mode:   0o777,
		Target: "../bar",
	})
	s.Finalize()

	const want = `{"path":"link","root":".","type":"symlink","mode":"0777","target":"../bar"}` + "\n"
	if out.String() != want {
		t.Fatalf("symlink entry mismatch\ngot:  %q\nwant: %q", out.String(), want)
	}
}

func TestNDJSON_StoreGroupReceipt(t *testing.T) {
	var out, errOut bytes.Buffer
	s := NewNDJSON(&out, &errOut)

	s.SetStore("storeA")
	s.StoreGroupReceipt("blake3:receipt", 42)
	s.Finalize()

	const want = `{"type":"store_group_receipt","store":"storeA","receipt_id":"blake3:receipt","count":42}` + "\n"
	if out.String() != want {
		t.Fatalf("summary mismatch\ngot:  %q\nwant: %q", out.String(), want)
	}
}

func TestNDJSON_NoticeRoutesToErrOut(t *testing.T) {
	var out, errOut bytes.Buffer
	s := NewNDJSON(&out, &errOut)

	s.Notice("switched to blob-store-id: storeA")
	s.Finalize()

	if out.Len() != 0 {
		t.Fatalf("notice must not appear on stdout; got %q", out.String())
	}
	if !strings.Contains(errOut.String(), "switched to blob-store-id: storeA") {
		t.Fatalf("notice missing from errOut; got %q", errOut.String())
	}
}

func TestNDJSON_FailureRecord(t *testing.T) {
	var out, errOut bytes.Buffer
	s := NewNDJSON(&out, &errOut)

	s.Failure("./missing", errors.New("no such file or directory"))
	s.Finalize()

	const want = `{"source":"./missing","error":"no such file or directory"}` + "\n"
	if out.String() != want {
		t.Fatalf("failure mismatch\ngot:  %q\nwant: %q", out.String(), want)
	}
}

func TestNDJSON_StoreScopesSubsequentEntries(t *testing.T) {
	var out, errOut bytes.Buffer
	s := NewNDJSON(&out, &errOut)

	s.SetStore("A")
	s.Entry(capture_receipt.EntryV1{
		Path: "a", Root: ".", Type: capture_receipt.TypeFile,
		Mode: 0o644, Size: 1, BlobId: "x",
	})
	s.SetStore("B")
	s.Entry(capture_receipt.EntryV1{
		Path: "b", Root: ".", Type: capture_receipt.TypeFile,
		Mode: 0o644, Size: 1, BlobId: "y",
	})
	s.StoreGroupReceipt("r", 1)
	s.Finalize()

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], `"store":"A"`) {
		t.Fatalf("line 0 store should be A: %q", lines[0])
	}
	if !strings.Contains(lines[1], `"store":"B"`) {
		t.Fatalf("line 1 store should be B: %q", lines[1])
	}
	if !strings.Contains(lines[2], `"store":"B"`) {
		t.Fatalf("summary store should be B: %q", lines[2])
	}
}

func TestTAP_EntryEmitsOk(t *testing.T) {
	var buf bytes.Buffer
	s := NewTAP(&buf)

	s.SetStore("storeA")
	s.Entry(capture_receipt.EntryV1{
		Path:   "foo.go",
		Root:   "./src",
		Type:   capture_receipt.TypeFile,
		Mode:   0o644,
		Size:   1234,
		BlobId: "blake3:abc",
	})
	s.Finalize()

	got := buf.String()
	mustContain(t, got, "ok ")
	mustContain(t, got, "./src/foo.go")
	mustContain(t, got, "file")
	mustContain(t, got, "mode=0644")
	mustContain(t, got, "size=1234")
	mustContain(t, got, "blob=blake3:abc")
}

func TestTAP_DirEntryNoFileFields(t *testing.T) {
	var buf bytes.Buffer
	s := NewTAP(&buf)

	s.Entry(capture_receipt.EntryV1{
		Path: ".",
		Root: "./src",
		Type: capture_receipt.TypeDir,
		Mode: 0o755,
	})
	s.Finalize()

	got := buf.String()
	mustContain(t, got, "./src dir mode=0755")
	if strings.Contains(got, "size=") {
		t.Fatalf("dir line should not include size: %q", got)
	}
	if strings.Contains(got, "blob=") {
		t.Fatalf("dir line should not include blob: %q", got)
	}
}

func TestTAP_SymlinkEntry(t *testing.T) {
	var buf bytes.Buffer
	s := NewTAP(&buf)

	s.Entry(capture_receipt.EntryV1{
		Path:   "link",
		Root:   "./src",
		Type:   capture_receipt.TypeSymlink,
		Mode:   0o777,
		Target: "../bar",
	})
	s.Finalize()

	mustContain(t, buf.String(), "./src/link symlink mode=0777 target=../bar")
}

func TestTAP_StoreGroupReceiptIsOkLine(t *testing.T) {
	var buf bytes.Buffer
	s := NewTAP(&buf)

	s.SetStore("storeA")
	s.StoreGroupReceipt("blake3:receipt", 42)
	s.Finalize()

	got := buf.String()
	mustContain(t, got, "receipt store=storeA")
	mustContain(t, got, "id=blake3:receipt")
	mustContain(t, got, "count=42")

	if strings.Contains(got, "not ok") {
		t.Fatalf("receipt should be ok, got: %q", got)
	}
}

func TestTAP_NoticeIsComment(t *testing.T) {
	var buf bytes.Buffer
	s := NewTAP(&buf)

	s.Notice("switched to blob-store-id: storeA")
	s.Finalize()

	got := buf.String()
	mustContain(t, got, "# ")
	mustContain(t, got, "switched to blob-store-id: storeA")
}

func TestTAP_FailureIsNotOk(t *testing.T) {
	var buf bytes.Buffer
	s := NewTAP(&buf)

	s.Failure("./missing", errors.New("no such file"))
	s.Finalize()

	mustContain(t, buf.String(), "not ok")
	mustContain(t, buf.String(), "./missing")
}

func TestTAP_FinalizeEmitsPlan(t *testing.T) {
	var buf bytes.Buffer
	s := NewTAP(&buf)

	s.Entry(capture_receipt.EntryV1{
		Path: ".", Root: ".", Type: capture_receipt.TypeDir, Mode: 0o755,
	})
	s.Finalize()

	mustContain(t, buf.String(), "1..1")
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected substring %q in:\n%s", needle, haystack)
	}
}
