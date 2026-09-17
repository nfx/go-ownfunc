package fixpromoted

import "testing"

func TestNewRuntime(t *testing.T) {
	newRuntime(t.Context(), &Some{value: "x"})
}
