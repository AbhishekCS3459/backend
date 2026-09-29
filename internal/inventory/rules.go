package inventory

import (
	"fmt"
	"strings"
)

// change is a validated stock mutation that has not been applied yet.
type change struct {
	Type    TxType
	Reason  *Reason
	Delta   int
	Counted *int
}

func receiveChange(quantity int) (change, error) {
	if quantity <= 0 || quantity > MaxChange {
		return change{}, &ValidationError{Message: fmt.Sprintf("quantity must be between 1 and %d", MaxChange)}
	}
	return change{Type: TypeStockReceived, Delta: quantity}, nil
}

func saleChange(quantity int) (change, error) {
	if quantity <= 0 || quantity > MaxChange {
		return change{}, &ValidationError{Message: fmt.Sprintf("quantity must be between 1 and %d", MaxChange)}
	}
	return change{Type: TypeOfflineSale, Delta: -quantity}, nil
}

// adjustChange turns an adjustment into a change against the current, locked
// stock. For COUNT the difference is always computed here, never by the client.
func adjustChange(current Stock, req AdjustRequest) (change, error) {
	switch req.Mode {
	case ModeChange:
		if req.CountedQuantity != nil {
			return change{}, &ValidationError{Message: "counted_quantity is only used with mode COUNT"}
		}
		if req.Quantity == nil || *req.Quantity == 0 {
			return change{}, &ValidationError{Message: "quantity must be a non-zero number"}
		}
		if abs(*req.Quantity) > MaxChange {
			return change{}, &ValidationError{Message: fmt.Sprintf("quantity must be between -%d and %d", MaxChange, MaxChange)}
		}
		reason := Reason(strings.ToUpper(strings.TrimSpace(string(req.Reason))))
		if !changeReasons[reason] {
			return change{}, &ValidationError{
				Message: "reason must be one of DAMAGED, EXPIRED, LOST, RETURN_TO_SUPPLIER, OTHER",
			}
		}
		return change{Type: TypeAdjustment, Reason: &reason, Delta: *req.Quantity}, nil
	case ModeCount:
		if req.Quantity != nil || req.Reason != "" {
			return change{}, &ValidationError{Message: "mode COUNT takes only counted_quantity and an optional note"}
		}
		if req.CountedQuantity == nil || *req.CountedQuantity < 0 || *req.CountedQuantity > maxOnHand {
			return change{}, &ValidationError{Message: fmt.Sprintf("counted_quantity must be between 0 and %d", maxOnHand)}
		}
		reason := ReasonStockCount
		counted := *req.CountedQuantity
		return change{Type: TypeAdjustment, Reason: &reason, Delta: counted - current.OnHand, Counted: &counted}, nil
	default:
		return change{}, &ValidationError{Message: "mode must be CHANGE or COUNT"}
	}
}

// apply returns the stock after the change, refusing anything that would take
// on-hand stock below zero or below what is already reserved for orders.
func apply(current Stock, c change) (Stock, error) {
	next := Stock{OnHand: current.OnHand + c.Delta, Reserved: current.Reserved}
	if next.OnHand > maxOnHand {
		return Stock{}, &StockError{Message: "stock would exceed the maximum allowed quantity"}
	}
	if next.OnHand >= next.Reserved {
		return next, nil
	}
	if c.Type == TypeOfflineSale {
		msg := fmt.Sprintf("Cannot sell %d: only %d %s free to sell", -c.Delta, max(current.Available(), 0), unitsAre(current.Available()))
		if current.Reserved > 0 {
			msg += fmt.Sprintf(" (%d held for online orders)", current.Reserved)
		}
		return Stock{}, &StockError{Message: msg + "."}
	}
	if c.Counted != nil {
		return Stock{}, &StockError{Message: fmt.Sprintf(
			"Cannot set stock to %d because %d %s currently reserved.",
			*c.Counted, current.Reserved, unitsAre(current.Reserved))}
	}
	if current.Reserved > 0 {
		return Stock{}, &StockError{Message: fmt.Sprintf(
			"Cannot reduce stock by %d because %d %s currently reserved.",
			-c.Delta, current.Reserved, unitsAre(current.Reserved))}
	}
	return Stock{}, &StockError{Message: fmt.Sprintf(
		"Cannot reduce stock by %d because only %d %s on hand.",
		-c.Delta, current.OnHand, unitsAre(current.OnHand))}
}

func unitsAre(n int) string {
	if n == 1 {
		return "unit is"
	}
	return "units are"
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
