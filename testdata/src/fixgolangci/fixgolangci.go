package fixgolangci

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
