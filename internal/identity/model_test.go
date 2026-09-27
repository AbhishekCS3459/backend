package identity

import (
	"testing"

	"github.com/AbhishekCS3459/find-me-backend/internal/platform/httputil"
)

func TestLoginRequest_AcceptsEmailOrPhone(t *testing.T) {
	valid := []LoginRequest{
		{Email: "owner@shop.in", Password: "secret123"},
		{Phone: "98765 43210", Password: "secret123"},
	}
	for _, req := range valid {
		if err := httputil.ValidateStruct(&req); err != nil {
			t.Errorf("%+v: unexpected error %v", req, err)
		}
	}
	invalid := []LoginRequest{
		{Password: "secret123"},
		{Email: "not-an-email", Password: "secret123"},
		{Phone: "98765 43210"},
	}
	for _, req := range invalid {
		if err := httputil.ValidateStruct(&req); err == nil {
			t.Errorf("%+v: expected a validation error", req)
		}
	}
}
