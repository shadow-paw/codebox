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
