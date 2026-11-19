// Command githubstub serves a stand-in for the GitHub REST API and the GitHub
// Actions runner scale set service.
//
// It backs the Base44 sandbox preview: examples/supervisor is pointed at it so
// the live view can show a real scale set converging without GitHub
// credentials or anything being created on GitHub. It is sandbox tooling, not
// part of the library.
//
//	go run ./hack/githubstub -addr 127.0.0.1:8081 -workload
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "address the stand-in listens on")
	advertise := flag.String("advertise", "", "base URL the client is told to call back on (default derived from -addr)")
	workload := flag.Bool("workload", false, "drive a repeating burst of jobs so the live view has demand")
	flag.Parse()

	baseURL := *advertise
	if baseURL == "" {
		host := *addr
		if strings.HasPrefix(host, ":") {
			host = "127.0.0.1" + host
		}
		baseURL = "http://" + host
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	standIn := newStub(baseURL)
	go standIn.runScheduler(ctx)
	if *workload {
		go standIn.runWorkload(ctx)
	}

	server := &http.Server{
		Addr:              *addr,
		Handler:           standIn,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("stand-in GitHub API listening on %s (advertising %s, workload %v)", *addr, baseURL, *workload)

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("stand-in GitHub API stopped: %v", err)
	}
}
