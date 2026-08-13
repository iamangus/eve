package chat

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"time"
)

type SSEEvent struct {
	Type string
	Data string
}

// proxySSE reads an SSE stream from src and writes it verbatim to w while
// invoking onEvent for every complete event (delimited by a blank line).
func proxySSE(w http.ResponseWriter, src io.Reader, onEvent func(SSEEvent)) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	flushed := 0
	lastFlush := time.Now()

	br := bufio.NewReaderSize(src, 64*1024)

	var curType string
	var dataParts []string

	reset := func() {
		curType = ""
		dataParts = dataParts[:0]
	}

	dispatch := func() {
		if curType == "" || len(dataParts) == 0 {
			reset()
			return
		}
		onEvent(SSEEvent{Type: curType, Data: strings.Join(dataParts, "\n")})
		reset()
	}

	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			if _, werr := w.Write([]byte(line)); werr != nil {
				return
			}
			flushed += len(line)

			trimmed := strings.TrimRight(line, "\r\n")
			switch {
			case trimmed == "":
				dispatch()
			case strings.HasPrefix(trimmed, ":"):
			case strings.HasPrefix(trimmed, "id:"):
			case strings.HasPrefix(trimmed, "retry:"):
			case strings.HasPrefix(trimmed, "event:"):
				curType = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
			case strings.HasPrefix(trimmed, "data:"):
				d := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				dataParts = append(dataParts, d)
			default:
			}
		}
		if err != nil {
			if err != io.EOF {
				return
			}
			dispatch()
			_ = rc.Flush()
			return
		}
		if time.Since(lastFlush) > 20*time.Millisecond || flushed > 16*1024 {
			_ = rc.Flush()
			flushed = 0
			lastFlush = time.Now()
		}
	}
}
