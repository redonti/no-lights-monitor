package health

import (
	"log"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ServeAsync starts a health + metrics server on :8081 in the background.
// /healthz → 200 always          (liveness)
// /readyz  → calls check()       (readiness)
// /metrics → Prometheus scrape   (not exposed through ingress)
func ServeAsync(check func() error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if err := check(); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/metrics", promhttp.Handler())
	go func() {
		if err := http.ListenAndServe(":8081", mux); err != nil {
			log.Printf("[health] server stopped: %v", err)
		}
	}()
}
