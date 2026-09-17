package fix

type Cache struct {
	data map[string]string
}

func clearMap(m map[string]string) { // want "clearMap is called only from methods of \\*Cache; consider making it an unexported method"
	clear(m)
}

func (c *Cache) Invalidate() {
	clearMap(c.data)
}

func (c *Cache) Reset() {
	clearMap(c.data)
}

// unnamed receives no receiver identifier, so the fix cannot rewrite this
// call site into a method call and must stay a plain diagnostic.
func unnamed(m map[string]string) { // want "unnamed is called only from methods of \\*Cache; consider making it an unexported method"
	clear(m)
}

func (Cache) Touch() {
	unnamed(nil)
}
