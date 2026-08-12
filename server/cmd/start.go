package cmd

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/store/postgres"
	grpcTransport "github.com/infrapad/infrapad/server/pkg/transport/grpc"
)

var (
	flagDBString string
	flagPort     int
)

func init() {
	startCmd.Flags().StringVar(&flagDBString, "db", "", "PostgreSQL connection string (or INFRAPAD_DBSTRING env)")
	startCmd.Flags().IntVar(&flagPort, "port", grpcTransport.DefaultPort, "gRPC listen port")
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the gRPC server",
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

		addr := fmt.Sprintf(":%d", flagPort)
		log.Printf("starting gRPC server on %s", addr)

		// Graceful shutdown on SIGINT/SIGTERM.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			log.Println("shutting down...")
			srv.GracefulStop()
		}()

		return srv.Serve(addr)
	},
}
