package a

import "testing"

// Calls from _test.go must not disqualify production candidates when
// ignore-test-files is true (the default).
func TestClearMapFromTest(t *testing.T) {
	clearMap(map[string]string{"k": "v"})
}

func helperOnlyInTest(s string) {}

func TestUsesTestHelper(t *testing.T) {
	helperOnlyInTest("x")
}
