// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package tui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// SlogHandler is a slog.Handler that feeds entries into a Collector.
// It also writes plain text to an optional fallback writer for startup
// messages before the TUI is running.
type SlogHandler struct {
	collector *Collector
	level     slog.Level
	attrs     []slog.Attr
	groups    []string
}

func NewSlogHandler(c *Collector, level slog.Level) *SlogHandler {
	return &SlogHandler{collector: c, level: level}
}

func (h *SlogHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *SlogHandler) Handle(_ context.Context, r slog.Record) error {
	level := r.Level.String()

	attrs := make(map[string]string)
	// pre-attached attrs
	for _, a := range h.attrs {
		attrs[a.Key] = fmt.Sprintf("%v", a.Value)
	}
	// record attrs
	r.Attrs(func(a slog.Attr) bool {
		key := a.Key
		if len(h.groups) > 0 {
			key = strings.Join(h.groups, ".") + "." + key
		}
		attrs[key] = fmt.Sprintf("%v", a.Value)
		return true
	})

	h.collector.AddLog(LogEntry{
		Time:    r.Time,
		Level:   level,
		Message: r.Message,
		Attrs:   attrs,
	})
	return nil
}

func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newH := &SlogHandler{collector: h.collector, level: h.level, groups: h.groups}
	newH.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return newH
}

func (h *SlogHandler) WithGroup(name string) slog.Handler {
	return &SlogHandler{
		collector: h.collector,
		level:     h.level,
		attrs:     h.attrs,
		groups:    append(append([]string{}, h.groups...), name),
	}
}
