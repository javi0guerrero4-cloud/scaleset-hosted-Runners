// Command base44preview is the preview harness used when this library is
// opened in the Base44 sandbox.
//
// This repository is a Go client library, so it has no user interface of its
// own to serve. This command serves a single page that drives the real client
// through a complete runner scale set flow. GitHub's API is replaced by a
// stand-in served by the same process (see stub.go), so the demo runs offline,
// needs no credentials, and creates nothing on GitHub.
package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"time"
)

func main() {
	addr := flag.String("addr", ":3000", "address the preview page listens on")
	stubAddr := flag.String("stub-addr", "127.0.0.1:8081", "address the stand-in GitHub API listens on")
	flag.Parse()

	baseURL := "http://" + *stubAddr
	stub := newStub(baseURL)

	stubServer := &http.Server{
		Addr:              *stubAddr,
		Handler:           stub,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("stand-in GitHub API listening on %s (config URL %s/myorg)", baseURL, baseURL)
		if err := stubServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("stand-in GitHub API stopped: %v", err)
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/", newPageHandler(stub, baseURL+"/myorg"))

	log.Printf("preview page listening on %s", *addr)
	server := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("preview page stopped: %v", err)
	}
}
