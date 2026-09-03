package schema

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testRetryDelay is what these tests wait between attempts. The retry policy
// is what is under test, not how long it holds for.
const testRetryDelay = time.Millisecond

// Every store below fetches with its own server's client rather than the
// package-level default. A store with no client of its own shares one
// connection pool with every other store in the process, and these tests run
// in parallel: a retry leaves the connection idle in that shared pool between
// attempts, which is exactly where another server shutting down can close it
// underneath the attempt about to reuse it. It surfaced as "transport
// connection broken: http: CloseIdleConnections called" on a change that
// touched neither this package nor the network. srv.Client() is per-server, so
// nothing else can reach into its pool.

// TestGetRetriesAThrottledRegistry pins that a 429 is a pause rather than the
// end of the run. The default registry is served from GitHub, which throttles;
// failing the whole lint on the first one would make a large run a coin toss.
func TestGetRetriesAThrottledRegistry(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) < 3 {
			w.Header().Set(retryAfterHeader, "0")
			w.WriteHeader(http.StatusTooManyRequests)

			return
		}

		_, _ = w.Write([]byte("served"))
	}))
	defer srv.Close()

	store := Store{AllowInsecure: true, retryDelay: testRetryDelay, NoCache: true, HTTPClient: srv.Client()}

	body, err := store.get(t.Context(), srv.URL, immutable)
	require.NoError(t, err)
	assert.Equal(t, "served", string(body))
	assert.Equal(t, int32(3), requests.Load(), "the throttled attempts should have been retried")
}

// TestGetGivesUpAfterTheLastAttempt pins that the retries are bounded, and that
// the error says how many attempts stand behind it: a registry that is simply
// down should not look like a single unlucky request.
func TestGetGivesUpAfterTheLastAttempt(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	store := Store{AllowInsecure: true, retryDelay: testRetryDelay, NoCache: true, HTTPClient: srv.Client()}

	_, err := store.get(t.Context(), srv.URL, immutable)
	require.ErrorIs(t, err, errBadStatus)
	assert.Contains(t, err.Error(), "503")
	assert.Contains(t, err.Error(), "gave up after 3 attempts")
	assert.Equal(t, int32(fetchAttempts), requests.Load())
}

// TestGetDoesNotRetryWhatWillNotChange pins that only a throttle or a
// server-side failure earns a second request. A 404 is load-bearing -- it is
// how the extension probe and the next location are reached -- and a 400 says
// the request itself is wrong, so asking again only spends the rate limit.
func TestGetDoesNotRetryWhatWillNotChange(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		status int
	}{
		{"not found", http.StatusNotFound},
		{"bad request", http.StatusBadRequest},
		{"forbidden", http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int32

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			store := Store{AllowInsecure: true, retryDelay: testRetryDelay, NoCache: true, HTTPClient: srv.Client()}

			_, err := store.get(t.Context(), srv.URL, immutable)
			require.Error(t, err)
			assert.Equal(t, int32(1), requests.Load(), "one request is all this status is worth")
		})
	}
}

// TestGetStopsRetryingWhenTheRunIsCancelled pins that a cancelled run does not
// keep waiting out a backoff nobody is waiting for.
func TestGetStopsRetryingWhenTheRunIsCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	// A minute of backoff, which the cancellation has to cut short for this to
	// return at all.
	store := Store{AllowInsecure: true, retryDelay: time.Minute, NoCache: true, HTTPClient: srv.Client()}

	_, err := store.get(ctx, srv.URL, immutable)
	require.ErrorIs(t, err, context.Canceled)
}

func TestRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

	for _, tt := range []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"absent", "", 0},
		{"seconds", "12", 12 * time.Second},
		{"padded", " 12 ", 12 * time.Second},
		{"negative seconds", "-12", 0},
		{"a date ahead", now.Add(30 * time.Second).Format(http.TimeFormat), 30 * time.Second},
		{"a date already past", now.Add(-time.Hour).Format(http.TimeFormat), 0},
		{"nonsense", "soon", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			header := http.Header{}
			if tt.value != "" {
				header.Set(retryAfterHeader, tt.value)
			}

			assert.Equal(t, tt.want, retryAfter(header, now))
		})
	}
}

func TestRetryDelay(t *testing.T) {
	t.Parallel()

	base := 500 * time.Millisecond

	assert.Equal(t, base, retryDelay(0, base, 0), "the first wait is the base")
	assert.Equal(t, 2*base, retryDelay(1, base, 0), "each further wait doubles")
	assert.Equal(t, 4*time.Second, retryDelay(0, base, 4*time.Second),
		"what the endpoint asked for wins over the backoff")
	assert.Equal(t, maxRetryDelay, retryDelay(0, base, time.Hour),
		"a wait longer than the cap is the cap")
	assert.Equal(t, maxRetryDelay, retryDelay(20, base, 0), "the backoff is capped too")
}
