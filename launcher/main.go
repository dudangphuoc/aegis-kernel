package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dudangphuoc/AegisKernel/core/controlplane"
	"github.com/dudangphuoc/AegisKernel/core/feature"
	"github.com/dudangphuoc/AegisKernel/core/gateway"
	"github.com/dudangphuoc/AegisKernel/core/loader"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("🌟 Micro-Kernel Launcher initializing with PostgreSQL Control Plane...")

	// 1. Kết nối PostgreSQL Control Plane (cp_db)
	cpDBURL := os.Getenv("CP_DATABASE_URL")
	if cpDBURL == "" {
		cpDBURL = "postgres://postgres:postgres@localhost:5432/cp_db?sslmode=disable"
	}

	cpStore, err := controlplane.NewStore(cpDBURL, logger)
	if err != nil {
		logger.Error("failed to initialize control plane store", slog.Any("error", err))
		os.Exit(1)
	}

	migrationService := controlplane.NewMigrationService(cpStore)
	cpHandler := controlplane.NewAPIHandler(cpStore, migrationService)

	// 2. Khởi tạo Feature Manager
	featureManager := feature.NewFeatureManager(cpStore)

	// 3. Khởi tạo Module Manager & Quét các modules
	modulesDir := os.Getenv("MODULES_DIR")
	if modulesDir == "" {
		modulesDir = "./bin/modules"
	}
	moduleManager := loader.NewModuleManager(cpStore, featureManager, modulesDir, logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := moduleManager.DiscoverAndLoadAll(ctx); err != nil {
		logger.Error("error during module discovery", slog.Any("error", err))
	}

	// 4. Khởi tạo API Gateway (tích hợp API Control Plane /api/cp/*)
	apiGateway := gateway.NewAPIGateway(moduleManager, featureManager, cpStore, cpHandler, logger)

	httpPort := os.Getenv("PORT")
	if httpPort == "" {
		httpPort = "8080"
	}
	server := &http.Server{
		Addr:              ":" + httpPort,
		Handler:           apiGateway.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("🚀 Micro-Kernel Gateway & Control Plane started",
			slog.String("port", httpPort),
			slog.String("control_plane_db", cpDBURL),
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", slog.Any("error", err))
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down Micro-Kernel and stopping all module processes...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	_ = server.Shutdown(shutdownCtx)
	moduleManager.ShutdownAll()
	logger.Info("Micro-Kernel shutdown complete.")
}
