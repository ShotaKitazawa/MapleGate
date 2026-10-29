package tunnel

// maxMoveFailures is how many moves of the node's route may fail in a row. With the route gone every packet goes into the
// tunnel and dies there, so the machine has no internet at all; ending the tunnel gives it back.
const maxMoveFailures = 3

type routeWatch struct{ fails int }

// failed records the outcome of one move and reports whether to give up.
func (w *routeWatch) failed(err error) bool {
	if err == nil {
		w.fails = 0
		return false
	}
	w.fails++
	return w.fails >= maxMoveFailures
}
