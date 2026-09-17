package funcval

type Worker struct{}

// With allow-function-values=true, taking the function as a value does not
// disqualify it when every invocation still belongs to a single receiver.
func format(s string) string { // want "format is called only from methods of \\*Worker; consider making it an unexported method"
	return s
}

func (w *Worker) Run(hook func(string) string) {
	_ = format("ok")
	hook = format
	_ = hook("x")
}
