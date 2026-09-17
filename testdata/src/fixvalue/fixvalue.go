package fixvalue

type Counter struct {
	n int
}

// doubled is only ever called from value-receiver methods of Counter, so the
// fix must attach it as a value-receiver method, not a pointer one.
func doubled(n int) int { // want "doubled is called only from methods of \\*Counter; consider making it an unexported method"
	return n * 2
}

func (c Counter) Value() int {
	return doubled(c.n)
}

func (c Counter) Doubled() int {
	return doubled(c.n)
}
