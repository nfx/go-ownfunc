package genericunfixable

type Box[T any] struct {
	value T
}

// label is called only from methods of the generic Box type, but one caller
// uses a blank receiver, so no receiver identifier exists there to qualify
// the call. The advice is still sound (a human can name the receiver first),
// so this stays reported with no suggested fix.
func label() string { // want "label is called only from methods of \\*Box; consider making it an unexported method"
	return "box"
}

func (b *Box[T]) Label() string {
	return label()
}

func (_ *Box[T]) Describe() string {
	return label()
}

// wrap declares its own type parameter but every call site instantiates it
// with a fixed concrete type (int), never with the calling method's own
// receiver type argument, so wrap could never become any method of Box[T],
// not merely one this tool can't auto-fix. Telling the user to "consider
// making it an unexported method" would be wrong advice, so it must not be
// reported at all — there is deliberately no "want" comment below.
func wrap[V any](v V) []V {
	return []V{v}
}

func (b *Box[T]) WrapCount() []int {
	return wrap(1)
}

func (b *Box[T]) WrapAgain() []int {
	return wrap(2)
}
