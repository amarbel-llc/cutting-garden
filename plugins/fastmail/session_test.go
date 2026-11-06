package fastmail

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Concurrent first callers share one session fetch and one cached value
// (cutting-garden#264); the server's delay keeps every caller inside the
// window where an unsynchronized check-then-fetch would each fetch.
func TestEnsureSession_ConcurrentCallersShareOneFetch(t *testing.T) {
	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"apiUrl":"http://x/api","downloadUrl":"http://x/d",` +
			`"primaryAccounts":{"urn:ietf:params:jmap:mail":"acct-1"},"state":"s"}`))
	}))
	t.Cleanup(srv.Close)
	c := newClient(srv.URL, "tok")

	const callers = 8
	got := make([]*Session, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			sess, err := c.ensureSession(context.Background())
			if err != nil {
				t.Errorf("ensureSession: %v", err)
			}
			got[i] = sess
		}()
	}
	close(start)
	wg.Wait()

	if n := fetches.Load(); n != 1 {
		t.Errorf("session fetched %d times, want 1", n)
	}
	for i, s := range got {
		if s == nil || s != got[0] {
			t.Errorf("caller %d got a different session pointer", i)
		}
	}
}

// A failed fetch is not cached: the next caller fetches again.
func TestEnsureSession_FailureIsRetried(t *testing.T) {
	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fetches.Add(1) == 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"apiUrl":"http://x/api","downloadUrl":"http://x/d",` +
			`"primaryAccounts":{"urn:ietf:params:jmap:mail":"acct-1"},"state":"s"}`))
	}))
	t.Cleanup(srv.Close)
	c := newClient(srv.URL, "tok")

	if _, err := c.ensureSession(context.Background()); err == nil {
		t.Fatal("first fetch should fail")
	}
	if _, err := c.ensureSession(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
}
