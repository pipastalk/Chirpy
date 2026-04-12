package main

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

func main() {
	serveMux := http.NewServeMux()
	httpServer := &http.Server{
		Addr:    ":8080",
		Handler: serveMux,
	}
	apiMetrics := &apiConfig{
		fileserverHits: atomic.Int32{},
	}

	fileServer := fileServerHandler()
	readiness := http.HandlerFunc(readyCheck)
	serveMux.HandleFunc("GET /api/healthz", middlewareAPICalls(readiness))
	serveMux.Handle("/app/", apiMetrics.middlewareMetricsInc(fileServer))
	serveMux.HandleFunc("GET /admin/metrics", middlewareAdminCalls(http.HandlerFunc(apiMetrics.fileServerHitsHandler)))
	serveMux.HandleFunc("POST /admin/reset", middlewareAdminCalls(http.HandlerFunc(apiMetrics.resetHandler)))
	httpServer.ListenAndServe()
}
func fileServerHandler() http.Handler {
	return http.Handler(http.StripPrefix("/app", http.FileServer(http.Dir("."))))
}
func readyCheck(w http.ResponseWriter, r *http.Request) {
	headers := w.Header()
	body := "OK"
	headers.Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(body))
}

type apiConfig struct {
	fileserverHits atomic.Int32
}

func middlewareAdminCalls(next http.Handler) http.HandlerFunc {
	stripped := http.StripPrefix("/admin", next)
	return func(w http.ResponseWriter, r *http.Request) {
		stripped.ServeHTTP(w, r)
	}
}
func middlewareAPICalls(next http.Handler) http.HandlerFunc {
	stripped := http.StripPrefix("/api", next)
	return func(w http.ResponseWriter, r *http.Request) {
		stripped.ServeHTTP(w, r)
	}
}
func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (cfg *apiConfig) fileServerHitsHandler(w http.ResponseWriter, r *http.Request) {
	hits := cfg.fileserverHits.Load()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	body := fmt.Sprintf(
		`<html>
  <body>
    <h1>Welcome, Chirpy Admin</h1>
    <p>Chirpy has been visited %d times!</p>
  </body>
</html>`, hits)

	w.Write([]byte(body))
}

func (cfg *apiConfig) resetHandler(w http.ResponseWriter, r *http.Request) {
	cfg.fileserverHits.Store(0)
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("Hits reset\n"))
}
