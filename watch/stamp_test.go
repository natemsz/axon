package watch

import "testing"

// Identical requests in one second get distinct stamps; different
// requests keep the clock's.
func TestUniqueStamp(t *testing.T) {
	a := uniqueStamp("tok GET /api/intel/v1/channels x", 1000)
	b := uniqueStamp("tok GET /api/intel/v1/channels x", 1000)
	c := uniqueStamp("tok GET /api/intel/v1/channels x", 1000)
	if a != 1000 || b != 1001 || c != 1002 {
		t.Errorf("stamps %d %d %d, want 1000 1001 1002", a, b, c)
	}
	if d := uniqueStamp("tok POST /lines y", 1000); d != 1000 {
		t.Errorf("another request stamped %d", d)
	}
	if e := uniqueStamp("tok GET /api/intel/v1/channels x", 1005); e != 1005 {
		t.Errorf("a later request stamped %d", e)
	}
}
