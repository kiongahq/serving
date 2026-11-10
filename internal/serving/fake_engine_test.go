package serving

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeEngine is a small stateful Docker Engine API. It records every call and
// every create payload by container name. A test overrides one route with
// on(); everything else follows the happy path: the fetch container exits 0
// and the serving container reports healthy.
type fakeEngine struct {
	t        *testing.T
	mu       sync.Mutex
	calls    []string
	created  map[string]map[string]any // container name -> create payload
	states   map[string][]string       // container ID -> scripted inspect bodies
	logs     map[string]string         // container ID -> log output
	images   map[string]string         // image reference -> image ID
	removed  []string
	routes   map[string]http.HandlerFunc
	inspects map[string]int
}

func newFakeEngine(t *testing.T) (*fakeEngine, *httptest.Server) {
	t.Helper()
	e := &fakeEngine{
		t:        t,
		created:  map[string]map[string]any{},
		states:   map[string][]string{},
		logs:     map[string]string{},
		images:   map[string]string{"kionga-mlflow": "sha256:" + strings.Repeat("a", 64)},
		routes:   map[string]http.HandlerFunc{},
		inspects: map[string]int{},
	}
	server := httptest.NewServer(http.HandlerFunc(e.serve))
	t.Cleanup(server.Close)
	return e, server
}

func (e *fakeEngine) on(key string, status int, body string) {
	e.routes[key] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func (e *fakeEngine) script(id string, bodies ...string) { e.states[id] = bodies }

func (e *fakeEngine) called(key string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, call := range e.calls {
		if call == key {
			return true
		}
	}
	return false
}

func (e *fakeEngine) countCalls(prefix string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	count := 0
	for _, call := range e.calls {
		if strings.HasPrefix(call, prefix) {
			count++
		}
	}
	return count
}

func (e *fakeEngine) serve(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	key := r.Method + " " + r.URL.Path
	e.calls = append(e.calls, key)
	handler, overridden := e.routes[key]
	e.mu.Unlock()
	if overridden {
		handler(w, r)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	path := r.URL.Path
	switch {
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/containers/"):
		e.removed = append(e.removed, strings.TrimPrefix(path, "/containers/"))
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/volumes/"):
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodPost && path == "/volumes/create":
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
		ref := strings.TrimSuffix(strings.TrimPrefix(path, "/images/"), "/json")
		id, ok := e.images[ref]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"No such image: ` + ref + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"Id":"` + id + `"}`))
	case r.Method == http.MethodPost && path == "/containers/create":
		name := r.URL.Query().Get("name")
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		e.created[name] = payload
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"` + name + `-id"}`))
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/start"):
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/logs"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/logs")
		_, _ = w.Write(frame(1, e.logs[id]))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json") && path != "/containers/json":
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
		bodies := e.states[id]
		if len(bodies) == 0 {
			if strings.HasSuffix(id, "-fetch-id") {
				bodies = []string{`{"State":{"Status":"exited","ExitCode":0}}`}
			} else {
				bodies = []string{`{"State":{"Status":"running","Health":{"Status":"healthy"}}}`}
			}
		}
		index := e.inspects[id]
		if index >= len(bodies) {
			index = len(bodies) - 1
		}
		e.inspects[id]++
		_, _ = w.Write([]byte(bodies[index]))
	case r.Method == http.MethodGet && path == "/containers/json":
		_, _ = w.Write([]byte(`[{"State":"running","Labels":{"kionga.serving":"true","kionga.model":"churn","kionga.artifact":"models:/churn-classifier/3","kionga.image":"ghcr.io/acme/churn:3"}}]`))
	default:
		e.t.Errorf("unexpected docker call %s %s", r.Method, r.URL.String())
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// frame encodes text as one Docker multiplexed log frame.
func frame(stream byte, text string) []byte {
	header := make([]byte, 8)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(text))) // #nosec G115 -- test data is tiny
	return append(header, text...)
}
