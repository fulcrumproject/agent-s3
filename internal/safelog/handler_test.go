// Logger regressions cover raw JSON bytes, scoped attributes and nested groups.
// These are the forms used by stdagent after secret resolution.
package safelog

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestSecretFieldsNeverReachEitherFormat(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			var h slog.Handler
			if format == "json" {
				h = slog.NewJSONHandler(&out, nil)
			} else {
				h = slog.NewTextHandler(&out, nil)
			}
			log := slog.New(New(h))
			secret := "LOG_SENTINEL_DO_NOT_EMIT"
			log.Info("runtime", "config", []byte(`{"secretKey":"`+secret+`"}`), "job_id", "visible")
			log.With("configuration", secret).WithGroup("scope").Info("nested",
				"details", slog.GroupValue(slog.String("secretAccessKey", secret), slog.String("safe", "visible")),
				"panic", secret, "error", errors.New("job handler panicked: "+secret))
			log.Error("metrics", "error", errors.New("metrics reporter panicked: "+secret))
			if strings.Contains(out.String(), secret) || strings.Contains(out.String(), "\"config\"") {
				t.Fatal("secret or config emitted")
			}
			if !strings.Contains(out.String(), "visible") {
				t.Fatal("safe diagnostics lost")
			}
			if !log.Enabled(context.Background(), slog.LevelInfo) {
				t.Fatal("level changed")
			}
		})
	}
}
