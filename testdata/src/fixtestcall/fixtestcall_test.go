package fixtestcall

import "testing"

func TestClearMap(t *testing.T) {
	clearMap(map[string]string{"k": "v"})
}
