// Command serve runs a static HTTPS file server for the meeting transcripts site.
//
// Usage:
//
//	serve [flags]
//
// Flags:
//
//	--addr     Address to listen on (default "localhost:9899")
//	--cert     Path to TLS certificate (default "certs/leaf.pem")
//	--key      Path to TLS private key (default "certs/leaf.key")
//	--dir      Directory to serve (default "site")
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/kevinburke/public-meetings/internal/version"
)

func main() {
	addr := flag.String("addr", "localhost:9899", "Address to listen on")
	certFile := flag.String("cert", "certs/leaf.pem", "Path to TLS certificate")
	keyFile := flag.String("key", "certs/leaf.key", "Path to TLS private key")
	dir := flag.String("dir", "site", "Directory to serve")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Fprintf(os.Stdout, "serve version %s\n", version.Version)
		os.Exit(0)
	}

	serverHeader := "public-meetings/" + version.Version

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(*dir)))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", serverHeader)
		mux.ServeHTTP(w, r)
	})

	slog.Info("serving", "addr", "https://"+*addr, "dir", *dir)
	if err := http.ListenAndServeTLS(*addr, *certFile, *keyFile, handler); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
