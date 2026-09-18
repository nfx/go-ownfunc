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

// unnamed's caller Touch has a blank receiver, so the fix must first name it,
// using Cache's established receiver-name convention ("c"), before it can
// rewrite the call site into a method call.
func unnamed(m map[string]string) { // want "unnamed is called only from methods of \\*Cache; consider making it an unexported method"
	clear(m)
}

func (Cache) Touch() {
	unnamed(nil)
}
