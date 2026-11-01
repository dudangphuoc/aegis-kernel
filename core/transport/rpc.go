package transport

import (
	"context"
	"net/rpc"

	"github.com/hashicorp/go-plugin"
	"github.com/dudangphuoc/aegis-kernel/core/contract"
)

// Handshake là cấu hình bắt tay xác thực bảo mật giữa Kernel và Plugin
var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "MICRO_KERNEL_MAGIC_COOKIE",
	MagicCookieValue: "go-micro-kernel-2026",
}

type MicroModulePlugin struct {
	Impl contract.MicroModule
}

func (p *MicroModulePlugin) Server(*plugin.MuxBroker) (interface{}, error) {
	return &RPCServer{Impl: p.Impl}, nil
}

func (MicroModulePlugin) Client(b *plugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &RPCClient{client: c}, nil
}

// RPCClient bọc RPC Client phía Launcher/Kernel
type RPCClient struct {
	client *rpc.Client
}

func (c *RPCClient) GetDescriptor(ctx context.Context) (contract.ModuleDescriptor, error) {
	var resp contract.ModuleDescriptor
	err := c.client.Call("Plugin.GetDescriptor", true, &resp)
	return resp, err
}

func (c *RPCClient) Initialize(ctx context.Context, config contract.ModuleInitConfig) error {
	var resp bool
	return c.client.Call("Plugin.Initialize", config, &resp)
}

func (c *RPCClient) GetRoutes(ctx context.Context) ([]contract.RouteDefinition, error) {
	var resp []contract.RouteDefinition
	err := c.client.Call("Plugin.GetRoutes", true, &resp)
	return resp, err
}

func (c *RPCClient) Execute(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error) {
	var resp contract.InvocationResponse
	err := c.client.Call("Plugin.Execute", req, &resp)
	return resp, err
}

func (c *RPCClient) HealthCheck(ctx context.Context) (bool, error) {
	var resp bool
	err := c.client.Call("Plugin.HealthCheck", true, &resp)
	return resp, err
}

// RPCServer lắng nghe cuộc gọi RPC phía Module (Plugin process)
type RPCServer struct {
	Impl contract.MicroModule
}

func (s *RPCServer) GetDescriptor(args bool, resp *contract.ModuleDescriptor) error {
	res, err := s.Impl.GetDescriptor(context.Background())
	if err != nil {
		return err
	}
	*resp = res
	return nil
}

func (s *RPCServer) Initialize(config contract.ModuleInitConfig, resp *bool) error {
	err := s.Impl.Initialize(context.Background(), config)
	if err != nil {
		return err
	}
	*resp = true
	return nil
}

func (s *RPCServer) GetRoutes(args bool, resp *[]contract.RouteDefinition) error {
	routes, err := s.Impl.GetRoutes(context.Background())
	if err != nil {
		return err
	}
	*resp = routes
	return nil
}

func (s *RPCServer) Execute(req contract.InvocationRequest, resp *contract.InvocationResponse) error {
	res, err := s.Impl.Execute(context.Background(), req)
	if err != nil {
		return err
	}
	*resp = res
	return nil
}

func (s *RPCServer) HealthCheck(args bool, resp *bool) error {
	ok, err := s.Impl.HealthCheck(context.Background())
	if err != nil {
		return err
	}
	*resp = ok
	return nil
}

