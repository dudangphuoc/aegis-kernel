package loader

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/hashicorp/go-plugin"
	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/core/controlplane"
	"github.com/dudangphuoc/aegis-kernel/core/feature"
	"github.com/dudangphuoc/aegis-kernel/core/transport"
)

type LoadedModule struct {
	Descriptor contract.ModuleDescriptor
	Routes     []contract.RouteDefinition
	Client     *plugin.Client
	Module     contract.MicroModule
}

type ModuleManager struct {
	mu             sync.RWMutex
	loadedModules  map[string]*LoadedModule
	featureManager *feature.FeatureManager
	cpStore        *controlplane.Store
	modulesDir     string
	logger         *slog.Logger
}

func NewModuleManager(
	cpStore *controlplane.Store,
	fm *feature.FeatureManager,
	modulesDir string,
	logger *slog.Logger,
) *ModuleManager {
	return &ModuleManager{
		loadedModules:  make(map[string]*LoadedModule),
		featureManager: fm,
		cpStore:        cpStore,
		modulesDir:     modulesDir,
		logger:         logger,
	}
}

func (m *ModuleManager) DiscoverAndLoadAll(ctx context.Context) error {
	dir := m.modulesDir
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil || (info.Mode()&0111 == 0) {
			continue
		}

		if err := m.LoadModule(ctx, path); err != nil {
			m.logger.Error("failed to load module", slog.String("path", path), slog.Any("error", err))
		}
	}
	return nil
}

func (m *ModuleManager) LoadModule(ctx context.Context, binaryPath string) error {
	m.logger.Info("spawning micro-module process", slog.String("binary", binaryPath))

	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: transport.Handshake,
		Plugins: map[string]plugin.Plugin{
			"micro_module": &transport.MicroModulePlugin{},
		},
		Cmd: exec.Command(binaryPath),
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return fmt.Errorf("failed to connect plugin rpc: %w", err)
	}

	raw, err := rpcClient.Dispense("micro_module")
	if err != nil {
		client.Kill()
		return fmt.Errorf("failed to dispense interface: %w", err)
	}

	mod, ok := raw.(contract.MicroModule)
	if !ok {
		client.Kill()
		return fmt.Errorf("plugin does not implement contract.MicroModule")
	}

	desc, err := mod.GetDescriptor(ctx)
	if err != nil {
		client.Kill()
		return fmt.Errorf("failed to get descriptor: %w", err)
	}

	// Chuẩn bị danh sách TenantModuleConfig cho module này từ PostgreSQL Control Plane
	tenantsMap := make(map[string]contract.TenantModuleConfig)
	allTenants, _ := m.cpStore.GetTenants(ctx)
	for tID := range allTenants {
		resolved, err := m.cpStore.ResolveTenantRequestContext(ctx, tID, desc.Name)
		if err == nil {
			tenantsMap[tID] = contract.TenantModuleConfig{
				TenantID:         tID,
				ConnectionString: resolved.ConnectionString,
				Features:         resolved.Features,
				Settings:         resolved.Settings,
			}
		}
	}

	initConfig := contract.ModuleInitConfig{
		Name:              desc.Name,
		Version:           desc.Version,
		Environment:       "production",
		DefaultConnection: "",
		DefaultFeatures:   map[string]bool{},
		Tenants:           tenantsMap,
	}

	if err := mod.Initialize(ctx, initConfig); err != nil {
		client.Kill()
		return fmt.Errorf("module %s initialization failed: %w", desc.Name, err)
	}

	routes, err := mod.GetRoutes(ctx)
	if err != nil {
		client.Kill()
		return fmt.Errorf("failed to get routes from %s: %w", desc.Name, err)
	}

	m.mu.Lock()
	m.loadedModules[desc.Name] = &LoadedModule{
		Descriptor: desc,
		Routes:     routes,
		Client:     client,
		Module:     mod,
	}
	m.mu.Unlock()

	m.logger.Info("✅ micro-module successfully loaded",
		slog.String("module", desc.Name),
		slog.String("version", desc.Version),
		slog.Int("routes_count", len(routes)),
	)
	return nil
}

func (m *ModuleManager) GetLoadedModules() []*LoadedModule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]*LoadedModule, 0, len(m.loadedModules))
	for _, v := range m.loadedModules {
		res = append(res, v)
	}
	return res
}

// ShutdownAll tắt 2 pha: gọi Shutdown RPC để module đóng DB pools (song song, giới hạn bởi ctx),
// sau đó Kill tiến trình plugin.
func (m *ModuleManager) ShutdownAll(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var wg sync.WaitGroup
	for name, mod := range m.loadedModules {
		wg.Add(1)
		go func(name string, mod *LoadedModule) {
			defer wg.Done()
			m.logger.Info("shutting down module", slog.String("module", name))
			if err := mod.Module.Shutdown(ctx); err != nil {
				m.logger.Warn("module shutdown did not complete cleanly", slog.String("module", name), slog.Any("error", err))
			}
			mod.Client.Kill()
		}(name, mod)
	}
	wg.Wait()
	m.loadedModules = make(map[string]*LoadedModule)
}
