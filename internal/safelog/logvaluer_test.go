// Lazily resolved groups must receive the same filtering as ordinary attributes.
// Unknown LogValuer wrappers must not bypass the configuration/secret denylist.
package safelog

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

type secretGroup string

func (s secretGroup) LogValue() slog.Value {
	return slog.GroupValue(slog.String("secretKey", string(s)), slog.String("safe", "visible"))
}
func TestLogValuerIsFiltered(t *testing.T) {
	var out bytes.Buffer
	log := slog.New(New(slog.NewJSONHandler(&out, nil)))
	log.Info("test", "lazy", secretGroup("LAZY_SECRET_SENTINEL"))
	if strings.Contains(out.String(), "LAZY_SECRET_SENTINEL") || !strings.Contains(out.String(), "visible") {
		t.Fatal("unsafe lazy group")
	}
}
