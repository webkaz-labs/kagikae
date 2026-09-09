package distribution

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

// Fixture serves immutable exact paths and never forwards requests. Rejections
// are sticky so an expected client failure cannot hide a routing/authentication leak.
// A product adapter may explicitly allow a missing-asset route with status 404.
type Response struct {
	Body   []byte
	Status int
}
type Fixture struct {
	routes      map[string]Response
	requests    atomic.Int64
	rejected    atomic.Int64
	refusedPath atomic.Value
}

func NewFixture(routes map[string]Response) *Fixture {
	f := &Fixture{routes: map[string]Response{}}
	for path, r := range routes {
		r.Body = append([]byte(nil), r.Body...)
		f.routes[path] = r
	}
	return f
}

func (f *Fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	if _, ok := r.Header["Authorization"]; ok {
		f.rejected.Add(1)
		http.Error(w, "authorization refused", http.StatusBadRequest)
		return
	}
	response, ok := f.routes[r.URL.Path]
	if !ok || r.Method != http.MethodGet {
		f.rejected.Add(1)
		f.refusedPath.Store(r.Method + " " + r.URL.EscapedPath())
		http.NotFound(w, r)
		return
	}
	status := response.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(response.Body)))
	w.WriteHeader(status)
	_, _ = w.Write(response.Body)
}
func (f *Fixture) Requests() int64 { return f.requests.Load() }
func (f *Fixture) Verdict() error {
	if f.rejected.Load() != 0 {
		return fmt.Errorf("fixture refused unexpected or authenticated requests: %v", f.refusedPath.Load())
	}
	return nil
}
