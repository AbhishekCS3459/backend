package orders

// transitions lists every status change an order may make. Anything else is
// refused, whoever asks.
var transitions = map[Status][]Status{
	StatusPendingPayment: {StatusPlaced, StatusCancelled, StatusExpired},
	StatusPlaced:         {StatusAccepted, StatusRejected, StatusCancelled},
	StatusAccepted:       {StatusReady, StatusRejected, StatusCancelled},
	StatusReady:          {StatusCompleted, StatusNoShow},
	// A payment that arrives after the hold lapsed revives the order if the stock is still there.
	StatusExpired: {StatusPlaced},
}

func canMove(from, to Status) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}
