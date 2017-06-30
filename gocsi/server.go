package gocsi

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/container-storage-interface/examples/gocsi/csi"
)

// a no-op assignment that asserts a Server is a ServiceProvider
var _ ServiceProvider = &Server{}

// Server is a GoCSI gRPC server.
type Server struct {
	// Addr is the Go network address on which to listen.
	Addr string

	// Services are the GoCSI Service endpoints managed
	// by this server.
	Services []Service

	// Options are used when creating the gRPC server.
	Options []grpc.ServerOption

	e chan error
	g *grpc.Server
	s *server
	l net.Listener
	x []func()
}

type server struct {
	s *Server
}

// Serve accepts incoming connections on the provided listener.
// If no listener is provided then the server will create a
// listener using s.Addr.
//
// Serve always returns a non-nil error.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {

	if len(s.Services) == 0 {
		return ErrEmptyServices
	}

	// if the provided listener is nil then create one
	// using s.Addr.
	if l == nil {
		p, a, err := ParseProtoAddr(s.Addr)
		if err != nil {
			return err
		}
		lis, err := net.Listen(p, a)
		if err != nil {
			return err
		}
		l = lis
	}

	// update the server's Addr field based on the listener
	netw := l.Addr().Network()
	addr := l.Addr().String()
	s.Addr = fmt.Sprintf("%s:/%s", netw, addr)

	// if the listener is a unix socket then append an exit
	// handler to remove the socket file
	if netw == "unix" {
		s.x = append(s.x, func() { os.RemoveAll(addr) })
	}

	// create the internal server
	s.s = &server{s: s}

	// create a new gRPC server and register this object
	// as the handler for the CSI services
	s.g = grpc.NewServer(s.Options...)
	csi.RegisterControllerServer(s.g, s.s)
	csi.RegisterIdentityServer(s.g, s.s)
	csi.RegisterNodeServer(s.g, s.s)

	// start each of the Services
	s.e = make(chan error)
	go func(services []Service) {
		var wg sync.WaitGroup
		for _, svc := range services {
			wg.Add(1)
			go func(svc Service) {
				s.e <- svc.Serve(ctx, nil)
				wg.Done()
			}(svc)
		}
		wg.Wait()
		close(s.e)
	}(s.Services)

	// start accepting incoming gRPC connections
	return s.g.Serve(l)
}

// ServiceErrs returns a channel that receives the errors
// returned from the Services Serve functions. This channel
// is closed when all of the Services have been stopped.
func (s *Server) ServiceErrs() chan<- error {
	return s.e
}

// Stop stops the gRPC server. It immediately closes all open
// connections and listeners. It cancels all active RPCs on the
// server side and the corresponding pending RPCs on the client
// side will get notified by connection errors.
func (s *Server) Stop(ctx context.Context) {
	// stop each of the Services
	for _, svc := range s.Services {
		svc.Stop(ctx)
	}
	s.g.Stop()
	for _, x := range s.x {
		x()
	}
}

// GracefulStop stops the gRPC server gracefully. It stops the
// server from accepting new connections and RPCs and blocks
// until all the pending RPCs are finished.
func (s *Server) GracefulStop(ctx context.Context) {
	// stop each of the Services
	for _, svc := range s.Services {
		svc.GracefulStop(ctx)
	}
	s.g.GracefulStop()
	for _, x := range s.x {
		x()
	}
}

type csisvc interface {
	csi.ControllerServer
	csi.IdentityServer
	csi.NodeServer
}

// svcFromCtx returns the CSI service specified in the gRPC metadata
// by inspecting the RPC context. If no server is found then the
// the first Service in the Server's Services list is used.
func (s *server) svcFromCtx(ctx context.Context) csisvc {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if n, ok := md["csi.service"]; ok && len(n) > 0 {
			for _, svc := range s.s.Services {
				if strings.EqualFold(n[0], svc.Name()) {
					log.Printf(
						"routed to service: type=%s name=%s\n",
						svc.Type(),
						svc.Name())
					return svc.(csisvc)
				}
			}
		}
	}
	svc := s.s.Services[0]
	log.Printf(
		"routed to service: type=%s name=%s\n",
		svc.Type(),
		svc.Name())
	return svc.(csisvc)
}

////////////////////////////////////////////////////////////////////////////////
//                            Controller Service                              //
////////////////////////////////////////////////////////////////////////////////

func (s *server) CreateVolume(
	ctx context.Context,
	req *csi.CreateVolumeRequest) (
	*csi.CreateVolumeResponse, error) {

	return s.svcFromCtx(ctx).CreateVolume(ctx, req)
}

func (s *server) DeleteVolume(
	ctx context.Context,
	req *csi.DeleteVolumeRequest) (
	*csi.DeleteVolumeResponse, error) {

	return s.svcFromCtx(ctx).DeleteVolume(ctx, req)
}

func (s *server) ControllerPublishVolume(
	ctx context.Context,
	req *csi.ControllerPublishVolumeRequest) (
	*csi.ControllerPublishVolumeResponse, error) {

	return s.svcFromCtx(ctx).ControllerPublishVolume(ctx, req)
}

func (s *server) ControllerUnpublishVolume(
	ctx context.Context,
	req *csi.ControllerUnpublishVolumeRequest) (
	*csi.ControllerUnpublishVolumeResponse, error) {

	return s.svcFromCtx(ctx).ControllerUnpublishVolume(ctx, req)
}

func (s *server) ValidateVolumeCapabilities(
	ctx context.Context,
	req *csi.ValidateVolumeCapabilitiesRequest) (
	*csi.ValidateVolumeCapabilitiesResponse, error) {

	return s.svcFromCtx(ctx).ValidateVolumeCapabilities(ctx, req)
}

func (s *server) ListVolumes(
	ctx context.Context,
	req *csi.ListVolumesRequest) (
	*csi.ListVolumesResponse, error) {

	return s.svcFromCtx(ctx).ListVolumes(ctx, req)
}

func (s *server) GetCapacity(
	ctx context.Context,
	req *csi.GetCapacityRequest) (
	*csi.GetCapacityResponse, error) {

	return s.svcFromCtx(ctx).GetCapacity(ctx, req)
}

func (s *server) ControllerGetCapabilities(
	ctx context.Context,
	req *csi.ControllerGetCapabilitiesRequest) (
	*csi.ControllerGetCapabilitiesResponse, error) {

	return s.svcFromCtx(ctx).ControllerGetCapabilities(ctx, req)
}

////////////////////////////////////////////////////////////////////////////////
//                             Identity Service                               //
////////////////////////////////////////////////////////////////////////////////

func (s *server) GetSupportedVersions(
	ctx context.Context,
	req *csi.GetSupportedVersionsRequest) (
	*csi.GetSupportedVersionsResponse, error) {

	return s.svcFromCtx(ctx).GetSupportedVersions(ctx, req)
}

func (s *server) GetPluginInfo(
	ctx context.Context,
	req *csi.GetPluginInfoRequest) (
	*csi.GetPluginInfoResponse, error) {

	return s.svcFromCtx(ctx).GetPluginInfo(ctx, req)
}

////////////////////////////////////////////////////////////////////////////////
//                                Node Service                                //
////////////////////////////////////////////////////////////////////////////////

func (s *server) NodePublishVolume(
	ctx context.Context,
	req *csi.NodePublishVolumeRequest) (
	*csi.NodePublishVolumeResponse, error) {

	return s.svcFromCtx(ctx).NodePublishVolume(ctx, req)
}

func (s *server) NodeUnpublishVolume(
	ctx context.Context,
	req *csi.NodeUnpublishVolumeRequest) (
	*csi.NodeUnpublishVolumeResponse, error) {

	return s.svcFromCtx(ctx).NodeUnpublishVolume(ctx, req)
}

func (s *server) GetNodeID(
	ctx context.Context,
	req *csi.GetNodeIDRequest) (
	*csi.GetNodeIDResponse, error) {

	return s.svcFromCtx(ctx).GetNodeID(ctx, req)
}

func (s *server) ProbeNode(
	ctx context.Context,
	req *csi.ProbeNodeRequest) (
	*csi.ProbeNodeResponse, error) {

	return s.svcFromCtx(ctx).ProbeNode(ctx, req)
}

func (s *server) NodeGetCapabilities(
	ctx context.Context,
	req *csi.NodeGetCapabilitiesRequest) (
	*csi.NodeGetCapabilitiesResponse, error) {

	return s.svcFromCtx(ctx).NodeGetCapabilities(ctx, req)
}
