package scanplugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	pb "github.com/ChristopherAparicio/aisync/internal/secrets/scanplugin/proto"
)

// GRPCPlugin implements hashicorp/go-plugin.GRPCPlugin for the scanner.
// It embeds NetRPCUnsupportedPlugin to indicate this is a gRPC-only plugin.
type GRPCPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	// Impl is the concrete implementation (set on the plugin side, nil on host side).
	Impl ScannerPlugin
}

// GRPCServer registers the plugin implementation with the gRPC server.
// This is called on the plugin side.
func (p *GRPCPlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	pb.RegisterSecretScannerServer(s, &grpcServer{impl: p.Impl})
	return nil
}

// GRPCClient creates a client that talks to the plugin over gRPC.
// This is called on the host (aisync) side.
func (p *GRPCPlugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &grpcClient{client: pb.NewSecretScannerClient(c)}, nil
}

// --- gRPC Server (plugin side) ---

type grpcServer struct {
	pb.UnimplementedSecretScannerServer
	impl ScannerPlugin
}

// Scan implements the gRPC SecretScanner.Scan RPC.
func (s *grpcServer) Scan(_ context.Context, req *pb.ScanRequest) (*pb.ScanResponse, error) {
	matches, err := s.impl.Scan(req.Content)
	if err != nil {
		return nil, err
	}
	return &pb.ScanResponse{Matches: matches}, nil
}

// Mask implements the gRPC SecretScanner.Mask RPC.
func (s *grpcServer) Mask(_ context.Context, req *pb.MaskRequest) (*pb.MaskResponse, error) {
	masked, err := s.impl.Mask(req.Content)
	if err != nil {
		return nil, err
	}
	return &pb.MaskResponse{MaskedContent: masked}, nil
}

// --- gRPC Client (host side) ---

type grpcClient struct {
	client pb.SecretScannerClient
}

// Scan calls the remote gRPC scanner's Scan method.
func (c *grpcClient) Scan(content string) ([]*pb.SecretMatch, error) {
	resp, err := c.client.Scan(context.Background(), &pb.ScanRequest{Content: content})
	if err != nil {
		return nil, err
	}
	return resp.Matches, nil
}

// Mask calls the remote gRPC scanner's Mask method.
func (c *grpcClient) Mask(content string) (string, error) {
	resp, err := c.client.Mask(context.Background(), &pb.MaskRequest{Content: content})
	if err != nil {
		return "", err
	}
	return resp.MaskedContent, nil
}
