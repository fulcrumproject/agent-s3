// Deletion success requires observed absence, not just successful mutation responses.
// This regression protects the core from a false terminal transition.
package s3agent

import (
	"context"
	"testing"
)

type noEffectDelete struct{ Backend }

func (b noEffectDelete) DeleteBucket(context.Context, string) error { return nil }
func TestDeleteVerifiesAbsence(t *testing.T) {
	a, b := testAgent()
	mustCreate(t, a)
	a.backend = noEffectDelete{b}
	if _, e := a.Handle(context.Background(), testJob("delete", nil)); e == nil {
		t.Fatal("reported deletion while bucket remained")
	}
	if b.tags[tagPhase] != deleting {
		t.Fatal("lost deletion barrier")
	}
	a.backend = b
	if _, e := a.Handle(context.Background(), testJob("delete", nil)); e != nil {
		t.Fatal(e)
	}
}
