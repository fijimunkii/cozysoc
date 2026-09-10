package localapi

import "testing"

func TestResponseErrorPreservesCodeAndHistoricalFormatting(t *testing.T) {
	err := &ResponseError{Code: "conflict", Message: "already configured"}
	if err.Code != "conflict" || err.Message != "already configured" {
		t.Fatalf("unexpected typed response error: %+v", err)
	}
	if got := err.Error(); got != "controller error conflict: already configured" {
		t.Fatalf("Error() = %q", got)
	}
}
