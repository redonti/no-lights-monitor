// Command webdev is a local frontend dev server. It serves ./web straight from
// disk (edits show up on refresh) and proxies /api/* and /admin/api/* to a
// remote backend, so frontend changes — including the admin panel — can be
// tested against real data without running the stack.
//
//	go run ./cmd/webdev                          # proxies to https://lights-monitor.com
//	go run ./cmd/webdev -upstream http://localhost:8081 -addr :3001
//
// /admin.html itself is served from local disk (so admin panel edits show up
// on refresh); only its data calls under /admin/api/* go to the upstream.
// The upstream will challenge those with HTTP Basic Auth — your browser will
// prompt for the real admin login/password the first time.
//
// WARNING: write requests (settings PUT/POST/DELETE, admin broadcasts, dev
// mode toggle, etc.) go to the upstream too — they affect real production data.
package main

import (
	"bytes"
	"flag"
	"html/template"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type webVars struct{ BotUsername, ChatUsername string }

func main() {
	addr := flag.String("addr", ":3000", "listen address")
	upstream := flag.String("upstream", "https://lights-monitor.com", "backend to proxy /api/* to")
	dir := flag.String("web", "./web", "path to the web directory")
	flag.Parse()

	target, err := url.Parse(*upstream)
	if err != nil {
		log.Fatalf("bad -upstream: %v", err)
	}
	vars := webVars{
		BotUsername:  os.Getenv("TELEGRAM_BOT_USERNAME"),
		ChatUsername: os.Getenv("TELEGRAM_CHAT_USERNAME"),
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		baseDirector(r)
		r.Host = target.Host // upstream ingress routes by Host header
	}

	// Templates are re-parsed on every request so edits are picked up live.
	render := func(w http.ResponseWriter, file string, status int) {
		tmpl, err := template.ParseFiles(filepath.Join(*dir, file))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, vars); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		w.Write(buf.Bytes())
	}

	files := http.FileServer(http.Dir(*dir))
	mux := http.NewServeMux()
	mux.Handle("/api/", proxy)
	mux.Handle("/admin/api/", proxy) // admin.html itself stays local; only its data calls go upstream
	mux.HandleFunc("GET /settings/{token}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(*dir, "settings.html"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html":
			render(w, "index.html", http.StatusOK)
			return
		}
		path := filepath.Join(*dir, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/")))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			render(w, "404.html", http.StatusNotFound)
			return
		}
		files.ServeHTTP(w, r)
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		log.Printf("%s %s", r.Method, r.URL.Path)
		mux.ServeHTTP(w, r)
	})

	log.Printf("serving %s on http://localhost%s, proxying /api/* and /admin/api/* to %s", *dir, *addr, target)
	log.Fatal(http.ListenAndServe(*addr, handler))
}
