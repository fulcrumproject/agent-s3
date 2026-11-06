// Panic values must not cross into stdagent's error reporting or core job data.
// Test an injected backend panic before any mutation is performed.
package s3agent

import (
	"context"
	"strings"
	"testing"
)

type panicBackend struct{ Backend }

func (b panicBackend) BucketTags(context.Context, string) (map[string]string, error) {
	panic("PANIC_SECRET_SENTINEL")
}
func TestPanicIsSanitized(t *testing.T) {
	a, b := testAgent()
	a.backend = panicBackend{b}
	result, err := a.Handle(context.Background(), createJob())
	if result != nil || err == nil || strings.Contains(err.Error(), "PANIC_SECRET_SENTINEL") {
		t.Fatal("panic details escaped")
	}
	if len(b.writes) != 0 {
		t.Fatal("mutated after panic")
	}
}
