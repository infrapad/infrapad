package cmd

import (
	"context"
	"fmt"
	"log"
	"net"
	nethttp "net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/store/postgres"
	grpcTransport "github.com/infrapad/infrapad/server/pkg/transport/grpc"
	transporthttp "github.com/infrapad/infrapad/server/pkg/transport/http"
)

var (
	flagDBString string
	flagPort     int
	flagHTTPAddr string
)

func init() {
	startCmd.Flags().StringVar(&flagDBString, "db", "", "PostgreSQL connection string (or INFRAPAD_DBSTRING env)")
	startCmd.Flags().IntVar(&flagPort, "port", grpcTransport.DefaultPort, "gRPC listen port")
	startCmd.Flags().StringVar(&flagHTTPAddr, "http-addr", ":8088", "HTTP/JSON gateway listen address")
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the gRPC + HTTP server",
	RunE: func(cmd *cobra.Command, args []string) error {
		connStr := flagDBString
		if connStr == "" {
			connStr = os.Getenv("INFRAPAD_DBSTRING")
		}
		if connStr == "" {
			return fmt.Errorf("database connection string is required (--db or INFRAPAD_DBSTRING)")
		}

		db, err := postgres.OpenPG(connStr)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		store := postgres.NewStore(db)
		ctrl := controller.New(store)
		srv := grpcTransport.New(ctrl)

		// 1. Start gRPC listener.
		grpcAddr := fmt.Sprintf(":%d", flagPort)
		grpcLis, err := net.Listen("tcp", grpcAddr)
		if err != nil {
			return fmt.Errorf("listen gRPC on %s: %w", grpcAddr, err)
		}
		log.Printf("starting gRPC server on %s", grpcLis.Addr())
		go func() {
			if err := srv.GRPCServer().Serve(grpcLis); err != nil {
				log.Printf("gRPC server error: %v", err)
			}
		}()

		// 2. Loopback connection for gRPC-gateway.
		loopbackAddr := grpcLis.Addr().String()
		conn, err := grpc.NewClient(loopbackAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("loopback grpc client: %w", err)
		}
		defer conn.Close()

		// 3. Gateway mux.
		ctx := context.Background()
		gwMux := runtime.NewServeMux()
		if err := transporthttp.RegisterGatewayHandlers(ctx, gwMux, conn); err != nil {
			return fmt.Errorf("register gateway handlers: %w", err)
		}

		// 4. Top-level HTTP mux.
		readiness := &transporthttp.Readiness{}
		topMux := nethttp.NewServeMux()
		transporthttp.RegisterHealthRoutes(topMux, readiness)
		topMux.Handle("/v1/", gwMux)

		// 5. HTTP server with CORS + body-limit middleware.
		httpServer := &nethttp.Server{
			Handler: transporthttp.MaxBody(transporthttp.CORS(topMux)),
		}
		httpLis, err := net.Listen("tcp", flagHTTPAddr)
		if err != nil {
			return fmt.Errorf("listen HTTP on %s: %w", flagHTTPAddr, err)
		}
		log.Printf("starting HTTP gateway on %s", httpLis.Addr())
		go func() {
			if err := httpServer.Serve(httpLis); err != nil && err != nethttp.ErrServerClosed {
				log.Printf("HTTP server error: %v", err)
			}
		}()

		readiness.MarkReady()

		// Graceful shutdown on SIGINT/SIGTERM.
		// Stop HTTP first, then gRPC (same order as fleetshift).
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")

		readiness.ClearReady()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP shutdown error: %v", err)
		}

		srv.GracefulStop()
		return nil
	},
}
