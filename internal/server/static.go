package server

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// uiCSP locks the app down to its own scripts. connect-src stays open so the
// browser can forward captured requests to any URL (e.g. localhost).
const uiCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; font-src 'self' data:; connect-src *; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

const notBuilt = `<!doctype html><meta charset="utf-8"><title>webhookgo</title>
<body style="font:16px system-ui;padding:3rem;max-width:40rem;margin:auto">
<h1>UI not built</h1><p>The web interface was not embedded in this binary.
Run <code>make build</code> (or <code>docker compose up --build</code>).</p>`

type asset struct {
	body  []byte
	gzip  []byte
	ctype string
	etag  string
}

// staticFiles serves the embedded web UI from memory, pre-compressed.
type staticFiles struct {
	files map[string]*asset
	pages map[string]bool // paths of the single-page app, served index.html
}

func loadStatic(fsys fs.FS) (*staticFiles, error) {
	sf := &staticFiles{files: map[string]*asset{}}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		a := &asset{
			body:  b,
			ctype: mime.TypeByExtension(path.Ext(p)),
			etag:  `"` + hex.EncodeToString(sum[:8]) + `"`,
		}
		if a.ctype == "" {
			a.ctype = http.DetectContentType(b)
		}
		if compressible(a.ctype) && len(b) > 1024 {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			_, _ = zw.Write(b)
			_ = zw.Close()
			if buf.Len() < len(b) {
				a.gzip = buf.Bytes()
			}
		}
		sf.files["/"+p] = a
		return nil
	})
	if err != nil {
		return nil, err
	}
	if sf.files["/index.html"] == nil {
		sf.files["/index.html"] = &asset{body: []byte(notBuilt), ctype: "text/html; charset=utf-8", etag: `"none"`}
	}
	return sf, nil
}

func compressible(ctype string) bool {
	return strings.HasPrefix(ctype, "text/") || strings.Contains(ctype, "javascript") ||
		strings.Contains(ctype, "json") || strings.Contains(ctype, "svg") || strings.Contains(ctype, "xml")
}

func (sf *staticFiles) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if page := strings.TrimSuffix(p, "/"); page != p && sf.pages[page] {
		// /admin/ -> /admin: the app picks its page by the exact path.
		if r.URL.RawQuery != "" {
			page += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, page, http.StatusMovedPermanently)
		return
	}
	if sf.pages[p] {
		p = "/index.html"
	}
	a := sf.files[p]
	if a == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	h := w.Header()
	h.Set("Content-Type", a.ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Vary", "Accept-Encoding")
	h.Set("ETag", a.etag)
	if strings.HasPrefix(p, "/assets/") {
		h.Set("Cache-Control", "public, max-age=31536000, immutable") // hashed file names
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	if p == "/index.html" {
		h.Set("Content-Security-Policy", uiCSP)
		h.Set("Referrer-Policy", "no-referrer")
	}
	if match := r.Header.Get("If-None-Match"); match != "" && match == a.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	body := a.body
	if a.gzip != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		body = a.gzip
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
