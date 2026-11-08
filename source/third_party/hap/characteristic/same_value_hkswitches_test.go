package characteristic

import (
	"net/http/httptest"
	"testing"
)

// Tests for the hkswitches patch (SetUpdateOnSameValue).
func TestSameValueWrite(t *testing.T) {
	for _, tc := range []struct {
		flag bool
		want int
	}{
		{false, 0},
		{true, 1},
	} {
		c := NewOn()
		c.SetValue(true)
		c.SetUpdateOnSameValue(tc.flag)

		calls := 0
		c.OnValueRemoteUpdate(func(v bool) { calls++ })

		req := httptest.NewRequest("PUT", "/characteristics", nil)
		if _, code := c.SetValueRequest(true, req); code != 0 {
			t.Fatalf("flag=%v: status %d", tc.flag, code)
		}
		if calls != tc.want {
			t.Errorf("flag=%v: same-value write fired callback %d times, want %d", tc.flag, calls, tc.want)
		}

		// A real change always fires.
		c.SetValueRequest(false, req)
		if calls != tc.want+1 {
			t.Errorf("flag=%v: changing write didn't fire callback", tc.flag)
		}
	}
}
