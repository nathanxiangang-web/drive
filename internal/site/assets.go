package site

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type asset struct {
	body, zipped []byte
	hash         string
	mime         string
}

func newAsset(body []byte, mime string) asset {
	digest := sha256.Sum256(body)
	if strings.HasPrefix(mime, "image/") {
		return asset{body: body, hash: hex.EncodeToString(digest[:16]), mime: mime}
	}
	var buffer bytes.Buffer
	zip := gzip.NewWriter(&buffer)
	_, _ = zip.Write(body)
	_ = zip.Close()
	return asset{body, buffer.Bytes(), hex.EncodeToString(digest[:16]), mime}
}

func supportsGzip(header string) bool {
	for _, coding := range strings.Split(header, ",") {
		token, parameter, _ := strings.Cut(coding, ";")
		if !strings.EqualFold(strings.TrimSpace(token), "gzip") {
			continue
		}
		if parameter == "" {
			return true
		}
		key, q, _ := strings.Cut(parameter, "=")
		score, err := strconv.ParseFloat(strings.TrimSpace(q), 64)
		return strings.TrimSpace(key) == "q" && err == nil && score > 0 && score <= 1
	}
	return false
}

func (a asset) serve(w http.ResponseWriter, r *http.Request, policy string) {
	body, etag := a.body, a.hash
	w.Header().Set("Vary", "Accept-Encoding")
	if len(a.zipped) > 0 && r.Header.Get("Range") == "" && supportsGzip(r.Header.Get("Accept-Encoding")) {
		body, etag = a.zipped, etag+"-gzip"
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.Header().Set("Cache-Control", policy)
	contentType := a.mime
	if strings.HasPrefix(a.mime, "text/") {
		contentType += "; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", fmt.Sprintf("%q", etag))
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}

func (s *Site) webRoutes(mux *http.ServeMux) {
	index, _ := assets.ReadFile("web/index.html")
	indexHTML := string(index)
	indexHTML = strings.ReplaceAll(indexHTML, "__REGISTRATION__", strconv.FormatBool(s.OpenRegistration))
	indexHTML = strings.ReplaceAll(indexHTML, "__MEDIA_ORIGIN__", html.EscapeString(s.mediaOrigin()))
	static := make(map[string]string)
	for name, mime := range map[string]string{
		"style.css": "text/css", "app.js": "text/javascript", "admin.js": "text/javascript", "admin-shares.js": "text/javascript",
		"share.js": "text/javascript", "media.js": "text/javascript", "share-view.js": "text/javascript", "share.css": "text/css", "share-bg.webp": "image/webp",
	} {
		body, _ := assets.ReadFile("web/" + name)
		a := newAsset(body, mime)
		url := "/assets/" + a.hash + "/" + name
		static[name] = url
		indexHTML = strings.ReplaceAll(indexHTML, `"/`+name+`"`, `"`+url+`"`)
		mux.HandleFunc("GET "+url, func(w http.ResponseWriter, r *http.Request) {
			a.serve(w, r, "public, max-age=31536000, immutable")
		})
	}
	s.publicShareRoutes(mux, static)
	page := newAsset([]byte(indexHTML), "text/html")
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		page.serve(w, r, "no-cache")
	})
}

func (s *Site) mediaOrigin() string {
	if len(s.Links.Hosts) == 1 {
		for host := range s.Links.Hosts {
			return "https://" + host
		}
	}
	return ""
}
