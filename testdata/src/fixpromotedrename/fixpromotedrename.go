package fixpromotedrename

type Widget struct {
	id string
}

func (w *Widget) Build() string {
	return w.id
}

func (w *Widget) Rebuild() string {
	return w.id
}

// describe already takes Widget explicitly as an argument, named "widget",
// but Widget's other methods consistently use "w" as the receiver name, so
// the fix must rename the promoted receiver (and its uses) to match.
func describe(widget *Widget, suffix string) string { // want "describe is called only from methods of \\*Widget; consider making it an unexported method"
	return widget.id + suffix
}

func (w *Widget) Describe(suffix string) string {
	return describe(w, suffix)
}

func (w *Widget) Redescribe(suffix string) string {
	return describe(w, suffix)
}
