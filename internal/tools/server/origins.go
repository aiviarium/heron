package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/aiviarium/heron/internal/tools/server/route"
)

// loopback reports whether an address listens on the loopback interface
// alone.
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sameOrigin reports whether a request's Origin is the server's own.
func sameOrigin(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// guardOrigins refuses API requests from pages of another origin than the
// server's.
func guardOrigins(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || !strings.HasPrefix(r.URL.Path, "/api/") || sameOrigin(r, origin) {
			next.ServeHTTP(w, r)
			return
		}
		route.WriteError(w, route.Coded(http.StatusForbidden, "server.ORIGIN_NOT_ALLOWED", "%s may not call this server", origin))
	})
}

// gzipped compresses the files a handler serves for a client that accepts
// gzip, on the fly. HTML that changes per request and the API aren't its business.
func gzipped(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		gw := &gzipWriter{ResponseWriter: w}
		next.ServeHTTP(gw, r)
		gw.finish()
	})
}

// gzipWriter buffers a response and writes it gzip-compressed when it's a
// success, as it is otherwise.
type gzipWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (g *gzipWriter) WriteHeader(status int) { g.status = status }
func (g *gzipWriter) Write(b []byte) (int, error) {
	return g.buf.Write(b)
}

func (g *gzipWriter) finish() {
	h := g.ResponseWriter.Header()
	if g.status == 0 {
		g.status = http.StatusOK
	}
	if g.status != http.StatusOK || h.Get("Content-Encoding") != "" || g.buf.Len() < 256 {
		g.ResponseWriter.WriteHeader(g.status)
		_, _ = g.ResponseWriter.Write(g.buf.Bytes())
		return
	}
	h.Set("Content-Encoding", "gzip")
	h.Del("Content-Length")
	g.ResponseWriter.WriteHeader(g.status)
	zw := gzip.NewWriter(g.ResponseWriter)
	_, _ = io.Copy(zw, &g.buf)
	_ = zw.Close()
}
