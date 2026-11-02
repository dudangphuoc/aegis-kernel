package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/bus"
	"github.com/dudangphuoc/aegis-kernel/core/controlplane"
	"github.com/dudangphuoc/aegis-kernel/core/feature"
	"github.com/dudangphuoc/aegis-kernel/core/gateway"
	"github.com/dudangphuoc/aegis-kernel/core/loader"
	"github.com/dudangphuoc/aegis-kernel/sdk/security"
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

	// Data Plane qua PgBouncer × Node (VD: PGBOUNCER_NODES=localhost:6432,localhost:6433)
	if nodes := os.Getenv("PGBOUNCER_NODES"); nodes != "" {
		cpStore.ConfigureRuntimePooler(strings.Split(nodes, ","))
		logger.Info("data plane routed through pgbouncer", slog.Any("nodes", cpStore.PoolerNodes()))
	} else {
		logger.Warn("PGBOUNCER_NODES not set; modules connect directly to PostgreSQL")
	}

	// Bổ sung database audit_<tenant_id> cho các Tenant cũ (idempotent)
	if err := cpStore.EnsureAuditModuleForAllTenants(context.Background()); err != nil {
		logger.Warn("failed to provision audit database for some tenants", slog.Any("error", err))
	}

	// JWT secret dùng chung Gateway (verify) và module auth (sign) — module kế thừa qua env
	jwtSecret, err := security.SecretFromEnv()
	if errors.Is(err, security.ErrMissingSecret) {
		_ = os.Setenv(security.EnvJWTSecret, security.GenerateSecret())
		jwtSecret, err = security.SecretFromEnv()
		logger.Warn("AEGIS_JWT_SECRET not set; generated an ephemeral secret (tokens are invalidated on restart)")
	}
	if err != nil {
		logger.Error("invalid JWT secret", slog.Any("error", err))
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

	// 3b. Kernel Event Bus: giao sự kiện (audit.recorded, domain events) giữa các module
	var busNodes []bus.Node
	for _, m := range moduleManager.GetLoadedModules() {
		busNodes = append(busNodes, bus.Node{Name: m.Descriptor.Name, Module: m.Module, Subscriptions: m.Descriptor.Subscriptions})
	}
	eventBus := bus.New(busNodes, cpStore, bus.Config{}, logger)
	eventBus.Start()

	// 4. Khởi tạo API Gateway (tích hợp API Control Plane /api/cp/*)
	apiGateway := gateway.NewAPIGateway(moduleManager, featureManager, cpStore, cpHandler, jwtSecret, logger)

	// Proxy tin cậy (CIDR/IP) được phép báo IP client qua X-Forwarded-For; mặc định không tin ai
	trusted, err := gateway.ParseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		logger.Error("invalid TRUSTED_PROXIES", slog.Any("error", err))
		os.Exit(1)
	}
	apiGateway.SetTrustedProxies(trusted)

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

	// Xả hết sự kiện còn trong outbox (VD: audit của request cuối) trước khi tắt module
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer drainCancel()
	if err := eventBus.Drain(drainCtx); err != nil {
		logger.Warn("event bus drain incomplete", slog.Any("error", err))
	}

	// Pha 2: module đóng DB pools rồi mới bị Kill (budget riêng, không dùng chung với HTTP drain)
	moduleCtx, moduleCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer moduleCancel()
	moduleManager.ShutdownAll(moduleCtx)
	logger.Info("Micro-Kernel shutdown complete.")
}
