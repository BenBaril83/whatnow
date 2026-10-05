package planner

import "time"

// SetModelTimeout shortens how long a model call may take, for one test.
func SetModelTimeout(d time.Duration) (restore func()) {
	old := modelTimeout
	modelTimeout = d
	return func() { modelTimeout = old }
}
