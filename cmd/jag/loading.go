package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jaggery-dev/jaggery/internal/airuntime"
)

// A model that isn't loaded can take minutes to answer its first request: a
// 12 GB model read from a network disk took 114 s. Two things help. The
// machine that serves it says the model is loading (inventory, jag models,
// the page), so the house can see why it's slow and sends more requests
// for that model there rather than loading it a second time elsewhere. And
// a streamed request gets its reply headers at once and an SSE comment
// every few seconds until the first token, which apps ignore but which
// keeps their read timeouts from firing.

// loadingNow is the models this machine's runtimes are loading for a
// request, and since when.
var loadingNow = &loadTracker{since: map[string]time.Time{}}

type loadTracker struct {
	mu    sync.Mutex
	since map[string]time.Time
	count map[string]int
}

// begin marks model as loading until the returned func is called; calling
// it again does nothing.
func (l *loadTracker) begin(model string) func() {
	l.mu.Lock()
	if l.count == nil {
		l.count = map[string]int{}
	}
	if l.count[model] == 0 {
		l.since[model] = time.Now().UTC()
	}
	l.count[model]++
	l.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.count[model]--; l.count[model] <= 0 {
				delete(l.count, model)
				delete(l.since, model)
			}
		})
	}
}

// mark sets LoadingSince on the runtimes' models this machine is loading.
func (l *loadTracker) mark(runtimes []airuntime.Runtime) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range runtimes {
		for j := range runtimes[i].Models {
			mo := &runtimes[i].Models[j]
			if t, ok := l.since[mo.Name]; ok && !mo.Loaded {
				mo.LoadingSince = &t
			}
		}
	}
}

// firstByte calls done on the first byte written through it, which is when
// the runtime has the model loaded.
type firstByte struct {
	http.ResponseWriter
	done func()
}

func (f firstByte) Write(p []byte) (int, error) {
	if len(p) > 0 {
		f.done()
	}
	return f.ResponseWriter.Write(p)
}

func (f firstByte) Flush() {
	if fl, ok := f.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

// keepaliveEvery is how often a waiting stream gets a comment.
var keepaliveEvery = 5 * time.Second

// waitingStream answers a streamed request at once with 200 and SSE
// comments until the reply's first byte. From then it passes the reply
// through. If the reply turns out to be an error, its status can no longer
// be sent, so the error goes out as a "data:" event, which OpenAI client
// libraries raise as an error.
type waitingStream struct {
	w       http.ResponseWriter
	mu      sync.Mutex
	started bool // the reply's first byte or header has arrived
	failed  bool // the reply's status was an error
	errBody bytes.Buffer
	stop    chan struct{}
	stopped sync.Once
}

func newWaitingStream(w http.ResponseWriter, note string) *waitingStream {
	s := &waitingStream{w: w, stop: make(chan struct{})}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	s.comment(note)
	go func() {
		t := time.NewTicker(keepaliveEvery)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.mu.Lock()
				if !s.started {
					s.comment(note)
				}
				s.mu.Unlock()
			}
		}
	}()
	return s
}

// comment writes one SSE comment line; the caller holds mu or is the
// constructor.
func (s *waitingStream) comment(note string) {
	fmt.Fprintf(s.w, ": %s\n\n", note)
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *waitingStream) halt() { s.stopped.Do(func() { close(s.stop) }) }

func (s *waitingStream) Header() http.Header { return s.w.Header() }

func (s *waitingStream) WriteHeader(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = true
	s.halt()
	if code != http.StatusOK {
		s.failed = true
	}
}

func (s *waitingStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = true
	s.halt()
	if s.failed {
		if room := 64<<10 - s.errBody.Len(); room > 0 {
			s.errBody.Write(p[:min(len(p), room)])
		}
		return len(p), nil
	}
	return s.w.Write(p)
}

func (s *waitingStream) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

// finish ends the stream. An error reply, or err when nothing was sent,
// goes out as one "data:" event with the OpenAI error shape.
func (s *waitingStream) finish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.halt()
	var event []byte
	switch {
	case s.failed:
		var compact bytes.Buffer
		if json.Compact(&compact, bytes.TrimSpace(s.errBody.Bytes())) == nil && compact.Len() > 0 {
			event = compact.Bytes()
		} else {
			event = apiErrorJSON(fmt.Sprintf("the runtime answered: %s", bytes.TrimSpace(s.errBody.Bytes())))
		}
	case err != nil:
		event = apiErrorJSON(err.Error())
	default:
		return
	}
	fmt.Fprintf(s.w, "data: %s\n\n", event)
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

func apiErrorJSON(msg string) []byte {
	b, _ := json.Marshal(map[string]any{"error": map[string]string{"message": msg, "type": "jaggery_error"}})
	return b
}
