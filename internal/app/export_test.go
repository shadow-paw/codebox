package app

import "time"

// SetClaudeCredentialsRetryDelayForTest swaps the wait between the
// first failed credentials rsync and its single retry, returning a
// restore function. Tests use this to drive the retry path without
// the real 2-second wall-clock wait.
func SetClaudeCredentialsRetryDelayForTest(d time.Duration) (restore func()) {
	prev := claudeCredentialsRetryDelay
	claudeCredentialsRetryDelay = d
	return func() { claudeCredentialsRetryDelay = prev }
}

// ResolveRefspecForTest exposes resolveRefspec so the source-omission
// rules (filling an absent refspec source from the configured git.push-from
// default) can be unit-tested directly.
func ResolveRefspecForTest(refspec, pushSource string) (string, error) {
	return resolveRefspec(refspec, pushSource)
}

// SetStartCheckBackoffForTest swaps the backoff schedule used by the
// post-run "is the container actually running?" poll, returning a
// restore function. Tests pass a slice of zero durations to exercise
// the retry path without the real wall-clock waits.
func SetStartCheckBackoffForTest(backoff []time.Duration) (restore func()) {
	prev := startCheckBackoff
	startCheckBackoff = backoff
	return func() { startCheckBackoff = prev }
}
