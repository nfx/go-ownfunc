package generic

type Box[T any] struct {
	value T
}

// describe is called only from methods of the generic Box type and takes
// Box[T] as its own sole argument, using its own matching type parameter, so
// it can still be promoted to a method: T moves from describe's own
// type-parameter list into the receiver's.
func describe[T any](b *Box[T]) T { // want "describe is called only from methods of \\*Box; consider making it an unexported method"
	return b.value
}

func (b *Box[T]) Get() T {
	return describe(b)
}

func (b *Box[T]) Peek() T {
	return describe(b)
}

// defaultLabel is called only from methods of the generic Box type and takes
// no argument that could supply Box's type parameter, but it also declares
// none of its own, so a receiver naming Box's own type parameter can be
// synthesized without needing to prove anything about it.
func defaultLabel() string { // want "defaultLabel is called only from methods of \\*Box; consider making it an unexported method"
	return "box"
}

func (b *Box[T]) Label() string {
	return defaultLabel()
}

func (b *Box[T]) Describe() string {
	return defaultLabel()
}

// consume is called only from methods of the generic Box type and declares
// its own type parameter, matching Box's, but takes no Box-typed argument to
// promote. Every call site still passes a yield already typed for the
// calling method's own T, so ownfunc can prove decl's type parameter is
// always instantiated with exactly that receiver's own type argument and
// move it into a synthesized receiver instead of leaving decl unfixable.
func consume[T any](yield func(T) bool) bool { // want "consume is called only from methods of \\*Box; consider making it an unexported method"
	return yield(*new(T))
}

func (b *Box[T]) Consume(yield func(T) bool) bool {
	return consume(yield)
}

func (b *Box[T]) ConsumeAgain(yield func(T) bool) bool {
	return consume(yield)
}
