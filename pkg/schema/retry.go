package schema

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// fetchAttempts is how many times a remote location is asked before the fetch
// reports what it last got. The default registry is served from GitHub, which
// throttles and which returns a transient 5xx often enough that a run notices;
// one 429 should not fail a lint that a second request would have passed.
const fetchAttempts = 3

// retryBaseDelay is how long the wait after the first failed attempt is, with
// each further one doubling it. Three attempts therefore span about a second
// and a half, which is short enough to sit inside a lint run and long enough
// for the burst that provoked the throttle to have passed.
const retryBaseDelay = 500 * time.Millisecond

// maxRetryDelay caps one wait, including a Retry-After the endpoint named. A
// registry asking to be left alone for an hour is telling the run to fail with
// its error rather than to hold a terminal open for an hour.
const maxRetryDelay = 10 * time.Second

// retryAfterHeader is where an endpoint says how long it wants to be left
// alone, which is worth more than any backoff guessed from this side.
const retryAfterHeader = "Retry-After"

// statusError reports a status a remote location answered with that the store
// cannot read a schema from. It carries what deciding on a retry needs: the
// status itself, and how long the endpoint asked to be left alone.
//
// A transport failure is not one of these and is not retried. Whether a
// connection that was reset would succeed on a second attempt is not knowable
// from here, while a 429 says so outright.
type statusError struct {
	url    string
	status string
	code   int
	after  time.Duration
}

func (e *statusError) Error() string {
	return fmt.Sprintf("GET %s: %v %s", e.url, errBadStatus, e.status)
}

// Unwrap keeps errors.Is(err, errBadStatus) answering for the callers that ask.
func (e *statusError) Unwrap() error { return errBadStatus }

// retryable reports whether asking again could plausibly serve the schema.
// Throttling and a server-side failure both say the request was fine and the
// moment was not; every other status says the request itself is the problem.
//
// 404 never reaches here: errNotFound is load-bearing for the extension probe
// and for falling through to the next location, and a registry that does not
// carry a version will not grow one while the run waits.
func (e *statusError) retryable() bool {
	return e.code == http.StatusTooManyRequests || e.code >= http.StatusInternalServerError
}

// retryDelay is the wait after a failed attempt: what the endpoint asked for
// when it said, and an exponential backoff from base otherwise. attempt counts
// from zero.
func retryDelay(attempt int, base, asked time.Duration) time.Duration {
	delay := asked
	if delay <= 0 {
		delay = base << attempt
	}

	return min(delay, maxRetryDelay)
}

// retryAfter reads the Retry-After header, in either form RFC 9110 gives it: a
// number of seconds, or the date to wait until. A header that is missing,
// unreadable, or already past means zero, which leaves the backoff to decide.
func retryAfter(header http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(header.Get(retryAfterHeader))
	if value == "" {
		return 0
	}

	seconds, err := strconv.Atoi(value)
	if err == nil {
		return max(time.Duration(seconds)*time.Second, 0)
	}

	until, err := http.ParseTime(value)
	if err != nil {
		return 0
	}

	return max(until.Sub(now), 0)
}

// sleep holds for the given delay, or gives up early when the run is
// cancelled. A retry that outlives the context it was started for is a retry
// nobody is waiting for.
func sleep(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting to retry: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
