package publish

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type recorder struct {
	mu      sync.Mutex
	paths   []string
	tree    map[string]any
	commit  map[string]any
	ref     map[string]any
	blobs   int
	refPOST bool
}

func fakeGitHub(t *testing.T, empty bool) (*httptest.Server, *recorder) {
	t.Helper()
	r := &recorder{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if req.Body != nil {
			_ = json.NewDecoder(req.Body).Decode(&body)
		}
		r.mu.Lock()
		r.paths = append(r.paths, req.Method+" "+req.URL.Path)
		r.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == "GET" && strings.HasSuffix(req.URL.Path, "/git/ref/heads/main"):
			if empty {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"message":"Not Found"}`))
				return
			}
			w.Write([]byte(`{"object":{"sha":"p1"}}`))
		case req.Method == "GET" && strings.Contains(req.URL.Path, "/git/commits/"):
			w.Write([]byte(`{"tree":{"sha":"t1"}}`))
		case req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/git/blobs"):
			r.mu.Lock()
			r.blobs++
			r.mu.Unlock()
			w.Write([]byte(`{"sha":"b1"}`))
		case req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/git/trees"):
			r.mu.Lock()
			r.tree = body
			r.mu.Unlock()
			w.Write([]byte(`{"sha":"t2"}`))
		case req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/git/commits"):
			r.mu.Lock()
			r.commit = body
			r.mu.Unlock()
			w.Write([]byte(`{"sha":"c1"}`))
		case req.Method == "PATCH" && strings.Contains(req.URL.Path, "/git/refs/heads/main"):
			r.mu.Lock()
			r.ref = body
			r.mu.Unlock()
			w.Write([]byte(`{}`))
		case req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/git/refs"):
			r.mu.Lock()
			r.refPOST = true
			r.ref = body
			r.mu.Unlock()
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, r
}

func newClient(srv *httptest.Server, path string) *Client {
	c := New("tok", "main", path)
	c.base = srv.URL
	return c
}

func TestPublishSquashed(t *testing.T) {
	srv, r := fakeGitHub(t, false)
	c := newClient(srv, "")

	sha, err := c.Publish(context.Background(), "o/r",
		map[string][]byte{"index.html": []byte("<html>hi</html>"), ".nojekyll": nil},
		"msg", true)
	if err != nil {
		t.Fatal(err)
	}
	if sha != "c1" {
		t.Fatalf("commit = %q", sha)
	}
	if r.tree["base_tree"] != "t1" {
		t.Fatalf("base_tree missing: %v", r.tree)
	}
	if parents, ok := r.commit["parents"].([]any); !ok || len(parents) != 0 {
		t.Fatalf("squashed commit must have no parents: %v", r.commit["parents"])
	}
	if r.ref["force"] != true || r.ref["sha"] != "c1" {
		t.Fatalf("ref update = %v", r.ref)
	}
	entries := r.tree["tree"].([]any)
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	byPath := map[string]map[string]any{}
	for _, e := range entries {
		m := e.(map[string]any)
		byPath[m["path"].(string)] = m
	}
	if m, ok := byPath["index.html"]; !ok || m["mode"] != "100644" {
		t.Fatalf("bad entry: %v", byPath)
	}
	if _, ok := byPath[".nojekyll"]; !ok {
		t.Fatalf("missing .nojekyll: %v", byPath)
	}
}

func TestPublishAppendKeepsParent(t *testing.T) {
	srv, r := fakeGitHub(t, false)
	c := newClient(srv, "docs")

	if _, err := c.Publish(context.Background(), "o/r", map[string][]byte{"index.html": []byte("x")}, "msg", false); err != nil {
		t.Fatal(err)
	}
	parents, _ := r.commit["parents"].([]any)
	if len(parents) != 1 || parents[0] != "p1" {
		t.Fatalf("append must keep the parent: %v", r.commit["parents"])
	}
	if r.ref["force"] != false {
		t.Fatalf("append must not force: %v", r.ref)
	}
	entries := r.tree["tree"].([]any)
	if p := entries[0].(map[string]any)["path"]; p != "docs/index.html" {
		t.Fatalf("path = %v, want docs/index.html", p)
	}
}

func TestPublishEmptyRepo(t *testing.T) {
	srv, r := fakeGitHub(t, true)
	c := newClient(srv, "")

	if _, err := c.Publish(context.Background(), "o/r", map[string][]byte{"index.html": []byte("x")}, "first", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.tree["base_tree"]; ok {
		t.Fatal("empty repo must not send base_tree")
	}
	if !r.refPOST {
		t.Fatal("expected POST /git/refs for a new branch")
	}
	if r.ref["ref"] != "refs/heads/main" {
		t.Fatalf("ref create body: %v", r.ref)
	}
}

func TestPublishErrorIsReadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()

	_, err := newClient(srv, "").Publish(context.Background(), "o/r",
		map[string][]byte{"index.html": []byte("x")}, "m", true)
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Bad credentials") {
		t.Fatalf("unexpected error: %v", err)
	}
}
