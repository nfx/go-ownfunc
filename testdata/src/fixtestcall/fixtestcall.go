package fixtestcall

type Cache struct {
	data map[string]string
}

// clearMap is called by Cache's pointer-receiver methods and also directly
// from a test, which must get a synthesized receiver once clearMap becomes a
// method.
func clearMap(m map[string]string) { // want "clearMap is called only from methods of \\*Cache; consider making it an unexported method"
	clear(m)
}

func (c *Cache) Invalidate() {
	clearMap(c.data)
}

func (c *Cache) Reset() {
	clearMap(c.data)
}
