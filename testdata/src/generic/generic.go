package generic

type Box[T any] struct {
	value T
}

// describe is called only from methods of the generic Box type; promoting it
// to a method would require threading Box's type parameter through both the
// receiver clause and any synthesized instance, so no fix is suggested.
func describe[T any](b *Box[T]) T { // want "describe is called only from methods of \\*Box; consider making it an unexported method"
	return b.value
}

func (b *Box[T]) Get() T {
	return describe(b)
}

func (b *Box[T]) Peek() T {
	return describe(b)
}
