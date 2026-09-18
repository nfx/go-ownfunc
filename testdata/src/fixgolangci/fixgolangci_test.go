package fixgolangci

import "testing"

func TestClearMap(t *testing.T) {
	clearMap(map[string]string{"k": "v"}) // want "clearMap is called only from methods of \\*Cache; consider making it an unexported method"
}
