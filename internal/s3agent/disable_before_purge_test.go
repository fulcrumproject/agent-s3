// No data deletion is allowed when credential disablement fails.
// A durable intent marker alone is not evidence that customer access is disabled.
package s3agent

import (
	"context"
	"testing"
)

func TestDeleteNeverPurgesBeforeDisablement(t *testing.T) {
	a, b := testAgent()
	mustCreate(t, a)
	b.fail = "disable"
	before := len(b.writes)
	if _, err := a.Handle(context.Background(), testJob("delete", nil)); err == nil {
		t.Fatal("disablement failure hidden")
	}
	for _, op := range b.writes[before:] {
		if op == "purge" || op == "deleteUser" || op == "deletePolicy" || op == "deleteBucket" {
			t.Fatal("destructive operation before disablement")
		}
	}
	if b.tags[tagPhase] != deleting {
		t.Fatal("deletion intent lost")
	}
}
