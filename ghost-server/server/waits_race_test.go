//go:build race

package server_test

// raceWaitScale is how much longer every wait in this package's tests may take
// under the race detector. It is a build tag rather than a runtime check
// because -race is a property of the build: the instrumentation slows every
// memory access, and this package runs a real server, a real SQLite database
// and real WebSocket sessions, so a whole package run takes minutes and a
// single exchange can take seconds on a loaded machine.
const raceWaitScale = 8
