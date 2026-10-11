package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func newRouter() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		if err := json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		}); err != nil {
			log.Printf("failed to write health response: %v", err)
		}
	})

	return mux
}

func main() {
	log.Println("LinkForge started on :8080")

	if err := http.ListenAndServe(":8080", newRouter()); err != nil {
		log.Fatal(err)
	}
}
