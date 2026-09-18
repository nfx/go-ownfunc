package genericunfixable

type Box[T any] struct {
	value T
}

// defaultLabel is called only from methods of the generic Box type, but it
// takes no argument that could supply Box's type parameter, so it cannot be
// promoted to a method and no fix is suggested.
func defaultLabel() string { // want "defaultLabel is called only from methods of \\*Box; consider making it an unexported method"
	return "box"
}

func (b *Box[T]) Label() string {
	return defaultLabel()
}

func (b *Box[T]) Describe() string {
	return defaultLabel()
}
