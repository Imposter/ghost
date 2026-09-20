package server_test

import (
	"os"
	"strconv"
	"time"
)

// waitScaleEnv scales every wait on top of raceWaitScale, for a machine (or a
// CI runner) slower than the one the deadlines were written for. "2" doubles
// them.
const waitScaleEnv = "GHOST_TEST_TIMEOUT_SCALE"

// waitScale is the multiplier applied to every deadline in this package's
// tests. Deadlines are written for what the server should do, and scaled for
// what the machine running it can manage: a fixed five seconds is a bet that
// a loaded race build loses, and the tests it loses are whichever ones were
// running, which is a poor way to choose what to distrust.
var waitScale = func() float64 {
	s := float64(raceWaitScale)
	if v := os.Getenv(waitScaleEnv); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			s *= f
		}
	}
	return s
}()

// wait scales d for the build and the machine. Every deadline in this
// package's tests goes through it.
func wait(d time.Duration) time.Duration {
	return time.Duration(float64(d) * waitScale)
}
