package fixblankcallerrename

type jobQueue struct {
	pending []string
}

func (q *jobQueue) Enqueue(id string) {
	q.pending = append(q.pending, nextJobID(id))
}

// nextJobID is called only from jobQueue methods, but Requeue's own receiver
// is blank and its body already declares a local named "q" — jobQueue's
// established receiver-name convention. Since "qV" is free, the fix renames
// that local out of the way before naming Requeue's receiver "q".
func nextJobID(id string) string { // want "nextJobID is called only from methods of \\*jobQueue; consider making it an unexported method"
	return id + "-id"
}

func (*jobQueue) Requeue(id string) {
	q := len(id)
	_ = nextJobID(id)
	_ = q
}
