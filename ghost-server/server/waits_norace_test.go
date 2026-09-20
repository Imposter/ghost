//go:build !race

package server_test

// raceWaitScale is 1 without the race detector; see waits_race_test.go for
// why a race build needs more.
const raceWaitScale = 1
