// cmd_serve.go — otoxan serve subcommand
package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	versionpkg "github.com/silas/otoxan/internal/version"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the otoxan HTTP server",
		Long:  "Start a lightweight HTTP API server for otoxan resources.",
		RunE: func(cmd *cobra.Command, args []string) error {
			mux := http.NewServeMux()
			mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			})
			mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"version":"%s"}`, versionpkg.Short())
			})

			srv := &http.Server{Addr: addr, Handler: mux}

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				_ = srv.Close()
			}()

			fmt.Printf("otoxan server listening on %s\n", addr)
			return srv.ListenAndServe()
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "listen address")
	return cmd
}
