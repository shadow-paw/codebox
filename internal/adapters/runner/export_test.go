package runner

// ExecArgs exposes the unexported execArgs builder for white-box tests.
// It returns the program name and argv that Run would exec for the
// given shell command, without spawning anything.
func (r *Runner) ExecArgs(shellCmd string) (string, []string) {
	return r.execArgs(shellCmd)
}
