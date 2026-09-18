package fixpromotedidx

type Bar struct {
	Close float64
}

type bars []Bar

func (b bars) Sum() float64 {
	total := 0.0
	for _, bar := range b {
		total += bar.Close
	}
	return total
}

func (b bars) Resum() float64 {
	return b.Sum()
}

// isDoubleCounted already takes bars explicitly as an argument, declared with
// the unnamed slice type []Bar rather than the named bars type, and uses it
// via indexing in the body; the promoted receiver must still be renamed to
// match bars' established "b" convention, with the indexing rewritten too.
func isDoubleCounted(bars []Bar, idx int) bool { // want "isDoubleCounted is called only from methods of \\*bars; consider making it an unexported method"
	if idx == 0 {
		return false
	}
	prevClose := bars[idx-1].Close
	return bars[idx].Close == prevClose
}

func (b bars) CountDoubles() int {
	count := 0
	for i := range b {
		if isDoubleCounted(b, i) {
			count++
		}
	}
	return count
}
