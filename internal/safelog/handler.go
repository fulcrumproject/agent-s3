// Package safelog prevents shared-runtime configuration dumps from reaching logs.
// It wraps the existing logger rather than changing agent-lib-go.
package safelog

import (
	"context"
	"log/slog"
	"strings"
)

type Handler struct{ next slog.Handler }

func New(next slog.Handler) slog.Handler { return &Handler{next: next} }
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func clean(a slog.Attr) slog.Attr {
	switch strings.ToLower(a.Key) {
	case "config", "configuration", "params", "properties", "body", "authorization",
		"panic", "secret", "secretkey", "secretaccesskey", "agenttoken":
		return slog.Attr{}
	}
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		attrs := a.Value.Group()
		out := make([]slog.Attr, 0, len(attrs))
		for _, child := range attrs {
			if x := clean(child); x.Key != "" {
				out = append(out, x)
			}
		}
		a.Value = slog.GroupValue(out...)
	}
	if a.Key == "error" && strings.Contains(a.Value.String(), "panicked:") {
		return slog.String("error", "handler panicked (details suppressed)")
	}
	return a
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	safe := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if x := clean(a); x.Key != "" {
			safe.AddAttrs(x)
		}
		return true
	})
	return h.next.Handle(ctx, safe)
}
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		if x := clean(a); x.Key != "" {
			out = append(out, x)
		}
	}
	return New(h.next.WithAttrs(out))
}
func (h *Handler) WithGroup(name string) slog.Handler { return New(h.next.WithGroup(name)) }
