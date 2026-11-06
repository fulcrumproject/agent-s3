// Protocol validation distinguishes RustFS administrative APIs from ordinary S3 paths.
// Misrouting must fail before credentials can be written to the wrong protocol.
package rustfsapi

import (
	"context"
	"errors"
	"github.com/fulcrumproject/agent-s3/internal/s3agent"
	"net/http"
	"testing"
)

func TestHealthRequiresAdministrativeIdentity(t *testing.T) {
	for _, body := range []string{`{}`, `{"access_key":"someone-else","status":"enabled"}`, `{"access_key":"test-admin","status":"disabled"}`} {
		t.Run(body, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
			if c.Ping(context.Background()) == nil {
				t.Fatal("false authenticated health")
			}
		})
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_key":"test-admin","status":"enabled"}`))
	})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestS3NotFoundIsNotMissingIAM(t *testing.T) {
	for _, code := range []string{"NoSuchKey", "NoSuchBucket", "NotFound"} {
		t.Run(code, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(404)
				_, _ = w.Write([]byte("<Error><Code>" + code + "</Code></Error>"))
			})
			if _, err := c.GetUser(context.Background(), "user"); err == nil || errors.Is(err, s3agent.ErrNotFound) {
				t.Fatal("wrong protocol treated as absent IAM")
			}
			if _, err := c.GetPolicy(context.Background(), "policy"); err == nil || errors.Is(err, s3agent.ErrNotFound) {
				t.Fatal("wrong protocol treated as absent policy")
			}
		})
	}
}
