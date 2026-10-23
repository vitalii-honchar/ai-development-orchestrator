package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"unicode/utf8"
)

const auditChunkBytes = 16 * 1024

// Each subprocess stream owns one writer; Flush runs after exec has joined its copy goroutines.
type auditWriter struct {
	ctx      context.Context
	log      *slog.Logger
	stream   string
	pending  []byte
	tail     []byte
	sequence uint64
}

func newAuditWriter(ctx context.Context, log *slog.Logger, stream string) *auditWriter {
	return &auditWriter{ctx: ctx, log: log, stream: stream, pending: make([]byte, 0, auditChunkBytes)}
}

func (w *auditWriter) Write(data []byte) (int, error) {
	length := len(data)
	if w.stream == "stderr" {
		if len(data) >= auditChunkBytes {
			w.tail = append(w.tail[:0], data[len(data)-auditChunkBytes:]...)
		} else {
			keep := min(len(w.tail), auditChunkBytes-len(data))
			w.tail = append(w.tail[:0], w.tail[len(w.tail)-keep:]...)
			w.tail = append(w.tail, data...)
		}
	}
	for len(data) > 0 {
		count := min(len(data), auditChunkBytes-len(w.pending))
		if newline := bytes.IndexByte(data[:count], '\n'); newline >= 0 {
			w.pending = append(w.pending, data[:newline]...)
			w.emit(strings.TrimSuffix(string(w.pending), "\r"), false)
			w.pending = w.pending[:0]
			data = data[newline+1:]
		} else {
			w.pending = append(w.pending, data[:count]...)
			data = data[count:]
			if len(w.pending) == auditChunkBytes {
				prefix := auditPrefixLength(w.pending)
				w.emit(string(w.pending[:prefix]), true)
				w.pending = append(w.pending[:0], w.pending[prefix:]...)
			}
		}
	}

	return length, nil
}

func (w *auditWriter) Flush() {
	if len(w.pending) > 0 {
		w.emit(string(w.pending), true)
		w.pending = w.pending[:0]
	}
}

func (w *auditWriter) String() string {
	return strings.TrimSpace(string(w.tail))
}

func (w *auditWriter) emit(output string, partial bool) {
	w.sequence++
	attrs := []any{"event", "codex_output", "stream", w.stream, "sequence", w.sequence,
		"partial", partial, "output", output}
	if w.stream == "stdout" {
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal([]byte(output), &event) == nil {
			attrs = append(attrs, "codex_event_type", event.Type)
			if event.ThreadID != "" {
				attrs = append(attrs, "codex_thread_id", event.ThreadID)
			}
		}
	}
	w.log.InfoContext(w.ctx, "Codex output", attrs...)
}

func auditPrefixLength(data []byte) int {
	for index := max(0, len(data)-utf8.UTFMax); index < len(data); index++ {
		if utf8.RuneStart(data[index]) && !utf8.FullRune(data[index:]) {
			return index
		}
	}

	return len(data)
}
