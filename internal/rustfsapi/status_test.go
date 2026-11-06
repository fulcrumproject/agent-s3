// S3 error diagnostics retain HTTP status without echoing untrusted backend messages.
// The shared logger can safely report these errors without request bodies or signed URLs.
package rustfsapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestS3ErrorKeepsStatusNotPayload(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte("<Error><Code>AccessDenied</Code><Message>SENSITIVE_REMOTE_DATA</Message></Error>"))
	})
	_, err := c.BucketTags(context.Background(), "bucket")
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || strings.Contains(err.Error(), "SENSITIVE_REMOTE_DATA") {
		t.Fatal("unsafe or incomplete error")
	}
}
