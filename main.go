package main

import "net/http"

func main() {
	serveMux := http.NewServeMux()
	httpServer := http.Server{
		Addr:    ":8080",
		Handler: serveMux,
	}
	httpServer.ListenAndServe()
}
