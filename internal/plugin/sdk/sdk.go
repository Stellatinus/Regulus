// Package sdk is what a plugin written in Go uses to serve the plugin
// contract (proto/regulus/plugin/v1) on the socket the host gives it. It does
// not decide what the plugin does, and a plugin in another language does not
// need it: serving the contract on REGULUS_PLUGIN_SOCKET is enough
// (docs/plugins/writing-a-plugin.md).
package sdk

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	pluginv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/plugin/v1"
)

// Environment the host sets for a plugin.
const (
	EnvSocket      = "REGULUS_PLUGIN_SOCKET"
	EnvID          = "REGULUS_PLUGIN_ID"
	EnvRole        = "REGULUS_PLUGIN_ROLE"
	EnvCoreVersion = "REGULUS_CORE_VERSION"
	EnvDataDir     = "REGULUS_PLUGIN_DATA_DIR"
)

// Plugin is what the plugin tells the host about itself; it must match its
// plugin.yaml.
type Plugin struct {
	ID         string
	Version    string
	APIVersion uint32
	// Health is asked periodically; nil means always serving. A non-nil error
	// is reported as not serving, with the error as the message.
	Health func(ctx context.Context) error
}

// Serve serves the contract on the socket named by REGULUS_PLUGIN_SOCKET
// until ctx ends or the process gets SIGTERM or SIGINT, which the host sends
// to stop it. register, if not nil, adds the plugin's own services to the
// same server.
func Serve(ctx context.Context, p Plugin, register func(*grpc.Server)) error {
	socket := os.Getenv(EnvSocket)
	if socket == "" {
		return fmt.Errorf("%s is not set: a plugin is started by Regulus, not by hand", EnvSocket)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	srv := grpc.NewServer()
	pluginv1.RegisterPluginServiceServer(srv, &service{p: p})
	if register != nil {
		register(srv)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}

type service struct {
	pluginv1.UnimplementedPluginServiceServer
	p Plugin
}

func (s *service) Describe(context.Context, *pluginv1.DescribeRequest) (*pluginv1.DescribeResponse, error) {
	return &pluginv1.DescribeResponse{Id: s.p.ID, Version: s.p.Version, ApiVersion: s.p.APIVersion}, nil
}

func (s *service) Health(ctx context.Context, _ *pluginv1.HealthRequest) (*pluginv1.HealthResponse, error) {
	if s.p.Health != nil {
		if err := s.p.Health(ctx); err != nil {
			return &pluginv1.HealthResponse{Status: pluginv1.HealthResponse_STATUS_NOT_SERVING, Message: err.Error()}, nil
		}
	}
	return &pluginv1.HealthResponse{Status: pluginv1.HealthResponse_STATUS_SERVING}, nil
}
