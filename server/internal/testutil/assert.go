// Package testutil provides shared helpers for server tests.
package testutil

import "testing"

// NoErr fails the test at the calling line if err is non-nil.
// Call it from the test goroutine, not from worker goroutines.
func NoErr(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
