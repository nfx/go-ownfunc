package a

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

// init is ignored by default
func init() {
	_ = 1
}

// Disqualified: called by a non-method function.
func helperFree(s string) {}

func (c *Cache) Process() {
	helperFree("test")
}

func FreeFunction() {
	helperFree("test")
}

// Disqualified: used across two different receivers.
type Storage struct{}

func sharedHelper() {}

func (c *Cache) Touch() {
	sharedHelper()
}

func (s *Storage) Touch() {
	sharedHelper()
}

// Self-recursion originates from a free-function context.
func recursive() {
	recursive()
}

func mutualA() { mutualB() }
func mutualB() { mutualA() }

// Disqualified: taken as a function value.
func asCallback() {}

func (c *Cache) Register() {
	_ = asCallback
	go asCallback()
}

// Pointer and value receivers of the same named type share ownership.
func normalizeKey(s string) string { // want "normalizeKey is called only from methods of \\*Cache; consider making it an unexported method"
	return s
}

func (c Cache) Lookup(k string) {
	_ = normalizeKey(k)
}

func (c *Cache) Store(k, v string) {
	_ = normalizeKey(k)
	c.data[k] = v
}

// Parenthesized call is still a direct call.
func wrappedClear() { // want "wrappedClear is called only from methods of \\*Cache; consider making it an unexported method"
}

func (c *Cache) Wrapped() {
	(wrappedClear)()
}

// go and defer count as direct calls.
func backgroundFlush() { // want "backgroundFlush is called only from methods of \\*Cache; consider making it an unexported method"
}

func (c *Cache) FlushLater() {
	go backgroundFlush()
	defer backgroundFlush()
}
