package mincalls

type Cache struct{}

// Called only once; MinCalls=2 filters it out.
func onceHelper() {}

func (c *Cache) Once() {
	onceHelper()
}

func twiceHelper() { // want "twiceHelper is called only from methods of \\*Cache; consider making it an unexported method"
}

func (c *Cache) A() { twiceHelper() }
func (c *Cache) B() { twiceHelper() }
