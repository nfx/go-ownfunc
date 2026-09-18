package blankcallercollision

type jobQueue struct {
	pending []string
}

func (q *jobQueue) Enqueue(id string) {
	q.pending = append(q.pending, nextJobID(id))
}

// q is a package-level variable that happens to share jobQueue's established
// receiver-name convention. nextJobID is called only from jobQueue methods,
// but Requeue's own receiver is blank and its body reads this q, so naming
// Requeue's receiver "q" directly would collide — and since that "q" isn't a
// local declaration inside Requeue, there's nothing renameCollidingLocals can
// rename out of the way either; nextJobID stays reported with no suggested
// fix.
var q int

func nextJobID(id string) string { // want "nextJobID is called only from methods of \\*jobQueue; consider making it an unexported method"
	return id + "-id"
}

func (*jobQueue) Requeue(id string) {
	_ = nextJobID(id)
	_ = q
}
