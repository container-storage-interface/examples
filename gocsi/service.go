package gocsi

import (
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"golang.org/x/net/context"
	"google.golang.org/grpc"

	"github.com/container-storage-interface/examples/gocsi/csi"
)

// ServiceProvider is a gRPC endpoint that provides the CSI
// services: Controller, Identity, Node.
type ServiceProvider interface {

	// Serve accepts incoming connections on the listener lis, creating
	// a new ServerTransport and service goroutine for each. The service
	// goroutine read gRPC requests and then call the registered handlers
	// to reply to them. Serve returns when lis.Accept fails with fatal
	// errors.  lis will be closed when this method returns.
	// Serve always returns non-nil error.
	Serve(ctx context.Context, lis net.Listener) error

	// Stop stops the gRPC server. It immediately closes all open
	// connections and listeners.
	// It cancels all active RPCs on the server side and the corresponding
	// pending RPCs on the client side will get notified by connection
	// errors.
	Stop(ctx context.Context)

	// GracefulStop stops the gRPC server gracefully. It stops the server
	// from accepting new connections and RPCs and blocks until all the
	// pending RPCs are finished.
	GracefulStop(ctx context.Context)
}

// Service represents a distinct configuration for a CSI endpoint that
// provides all three CSI service types: Controller, Identity, and Node.
//
// Service provides access to the endpoint via a high-performance,
// in-memory, piped-based gRPC connection. The pipe-based connection
// is much faster than the TCP or UNIX socket alternatives:
//
//     $ go test -benchmem -parallel 500 -bench '^Benchmark(Pipe|TCP|Unix)$'
//     BenchmarkPipe-8   	    5000	    272109 ns/op	  110744 B/op	    3169 allocs/op
//     BenchmarkTCP-8    	    1000	   1346010 ns/op	  115807 B/op	    3166 allocs/op
//     BenchmarkUnix-8   	    2000	    688202 ns/op	  111098 B/op	    3165 allocs/op
//     PASS
//     ok  	github.com/container-storage-interface/examples/gocsi	4.320s
//
// To use the in-memory, pipe-based connection pass a nil value to the
// Service object's Serve function for the listener.
type Service interface {
	ServiceProvider

	// Name returns the name of the service.
	Name() string

	// Type returns the name of the service provider.
	Type() string
}

// NewService returns a service for the specified provider. If no
// provider matches the specified name a nil value is returned.
func NewService(
	ctx context.Context,
	serviceType, serviceName string) (Service, error) {

	log.Printf("NewService type=%s name=%s\n", serviceType, serviceName)

	// ensure that the go plug-ins are loaded. this is safe to call
	// multiple times as it is guarded internally by a sync.Once
	if err := LoadGoPlugins(ctx); err != nil {
		return nil, err
	}

	for k, v := range serviceProviderCtors {
		if strings.EqualFold(k, serviceType) {
			o := v()
			if sp, ok := o.(ServiceProvider); ok {
				return &service{
					serviceType: k,
					serviceName: serviceName,
					sp:          sp,
					conn:        NewPipeConn(k),
				}, nil
			}
			return nil, fmt.Errorf("invalid service provider type: %T", o)
		}
	}

	return nil, ErrInvalidProvider
}

type service struct {
	serviceType  string
	serviceName  string
	sp           ServiceProvider
	conn         PipeConn
	clnt         *grpc.ClientConn
	versions     []*csi.Version
	versionsOnce sync.Once
}

func (s *service) Name() string {
	return s.serviceName
}

func (s *service) Type() string {
	return s.serviceType
}

func (s *service) Serve(
	ctx context.Context, lis net.Listener) (err error) {

	if lis == nil {
		lis = s.conn
	}
	return s.sp.Serve(ctx, lis)
}

func (s *service) Stop(ctx context.Context) {
	s.sp.Stop(ctx)
	s.conn.Close()
}

func (s *service) GracefulStop(ctx context.Context) {
	s.sp.GracefulStop(ctx)
	s.conn.Close()
}

func (s *service) dial(
	ctx context.Context) (client *grpc.ClientConn, err error) {

	return grpc.DialContext(
		ctx,
		s.serviceName,
		grpc.WithInsecure(),
		grpc.WithDialer(s.conn.DialGrpc))
}

func (s *service) dialController(
	ctx context.Context) (csi.ControllerClient, error) {

	c, err := s.dial(ctx)
	if err != nil {
		return nil, err
	}
	return csi.NewControllerClient(c), nil
}

func (s *service) dialIdentity(
	ctx context.Context) (csi.IdentityClient, error) {

	c, err := s.dial(ctx)
	if err != nil {
		return nil, err
	}
	return csi.NewIdentityClient(c), nil
}

func (s *service) dialNode(
	ctx context.Context) (csi.NodeClient, error) {

	c, err := s.dial(ctx)
	if err != nil {
		return nil, err
	}
	return csi.NewNodeClient(c), nil
}

type hasGetVersion interface {
	GetVersion() *csi.Version
}

// chkReqVersion validates the request version. an empty
// return value means the request version is valid. any
// other value means the version is invalid and the return
// value describes the reason for the invalidation
func (s *service) chkReqVersion(
	ctx context.Context,
	req hasGetVersion) string {

	// cache the supported versions exactly once
	if err := s.initSupportedVersionsOnce(ctx); err != nil {
		return err.Error()
	}

	rv := req.GetVersion()
	if rv == nil {
		return "request version is nil"
	}

	for _, v := range s.versions {
		if rv.GetMajor() != v.GetMajor() {
			continue
		}
		if rv.GetMinor() != v.GetMinor() {
			continue
		}
		if rv.GetPatch() != v.GetPatch() {
			continue
		}
		return ""
	}

	return fmt.Sprintf(
		"unsupported request version: %s", SprintfVersion(rv))
}

func (s *service) initSupportedVersionsOnce(ctx context.Context) (err error) {
	s.versionsOnce.Do(func() {
		err = s.initSupportedVersions(ctx)
	})
	return
}

func (s *service) initSupportedVersions(ctx context.Context) error {
	c, err := s.dialIdentity(ctx)
	if err != nil {
		return err
	}
	r, err := c.GetSupportedVersions(
		ctx,
		&csi.GetSupportedVersionsRequest{})
	if err != nil {
		return err
	}
	// check to see if there is a csi error
	if cerr := r.GetError(); cerr != nil {
		if err := cerr.GetGeneralError(); err != nil {
			return fmt.Errorf(
				"error: GetSupportedVersionsResponse failed: %d: %s",
				err.GetErrorCode(),
				err.GetErrorDescription())
		}
		return errors.New(cerr.String())
	}
	result := r.GetResult()
	if result == nil {
		return ErrNilResult
	}
	s.versions = result.GetSupportedVersions()
	return nil
}

////////////////////////////////////////////////////////////////////////////////
//                            Controller Service                              //
////////////////////////////////////////////////////////////////////////////////

func (s *service) CreateVolume(
	ctx context.Context,
	req *csi.CreateVolumeRequest) (
	*csi.CreateVolumeResponse, error) {

	c, err := s.dialController(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrCreateVolumeGeneral(2, v), nil
	}
	if len(req.GetName()) == 0 {
		// INVALID_VOLUME_NAME
		return ErrCreateVolume(3, "missing name"), nil
	}
	return c.CreateVolume(ctx, req)
}

func (s *service) DeleteVolume(
	ctx context.Context,
	req *csi.DeleteVolumeRequest) (
	*csi.DeleteVolumeResponse, error) {

	idObj := req.GetVolumeId()
	if idObj == nil {
		// INVALID_VOLUME_ID
		return ErrDeleteVolume(3, "missing id obj"), nil
	}

	idVals := idObj.GetValues()
	if len(idVals) == 0 {
		// INVALID_VOLUME_ID
		return ErrDeleteVolume(3, "missing id map"), nil
	}

	c, err := s.dialController(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrDeleteVolumeGeneral(2, v), nil
	}
	return c.DeleteVolume(ctx, req)
}

func (s *service) ControllerPublishVolume(
	ctx context.Context,
	req *csi.ControllerPublishVolumeRequest) (
	*csi.ControllerPublishVolumeResponse, error) {

	idObj := req.GetVolumeId()
	if idObj == nil {
		// INVALID_VOLUME_ID
		return ErrControllerPublishVolume(3, "missing id obj"), nil
	}

	idVals := idObj.GetValues()
	if len(idVals) == 0 {
		// INVALID_VOLUME_ID
		return ErrControllerPublishVolume(3, "missing id map"), nil
	}

	c, err := s.dialController(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrControllerPublishVolumeGeneral(2, v), nil
	}
	return c.ControllerPublishVolume(ctx, req)
}

func (s *service) ControllerUnpublishVolume(
	ctx context.Context,
	req *csi.ControllerUnpublishVolumeRequest) (
	*csi.ControllerUnpublishVolumeResponse, error) {

	idObj := req.GetVolumeId()
	if idObj == nil {
		// INVALID_VOLUME_ID
		return ErrControllerUnpublishVolume(3, "missing id obj"), nil
	}

	idVals := idObj.GetValues()
	if len(idVals) == 0 {
		// INVALID_VOLUME_ID
		return ErrControllerUnpublishVolume(3, "missing id map"), nil
	}

	c, err := s.dialController(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrControllerUnpublishVolumeGeneral(2, v), nil
	}
	return c.ControllerUnpublishVolume(ctx, req)
}

func (s *service) ValidateVolumeCapabilities(
	ctx context.Context,
	req *csi.ValidateVolumeCapabilitiesRequest) (
	*csi.ValidateVolumeCapabilitiesResponse, error) {

	c, err := s.dialController(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrValidateVolumeCapabilitiesGeneral(2, v), nil
	}
	return c.ValidateVolumeCapabilities(ctx, req)
}

func (s *service) ListVolumes(
	ctx context.Context,
	req *csi.ListVolumesRequest) (
	*csi.ListVolumesResponse, error) {

	c, err := s.dialController(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrListVolumes(2, v), nil
	}
	return c.ListVolumes(ctx, req)
}

func (s *service) GetCapacity(
	ctx context.Context,
	req *csi.GetCapacityRequest) (
	*csi.GetCapacityResponse, error) {

	c, err := s.dialController(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrGetCapacity(2, v), nil
	}
	return c.GetCapacity(ctx, req)
}

func (s *service) ControllerGetCapabilities(
	ctx context.Context,
	req *csi.ControllerGetCapabilitiesRequest) (
	*csi.ControllerGetCapabilitiesResponse, error) {

	c, err := s.dialController(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrControllerGetCapabilities(2, v), nil
	}
	return c.ControllerGetCapabilities(ctx, req)
}

////////////////////////////////////////////////////////////////////////////////
//                             Identity Service                               //
////////////////////////////////////////////////////////////////////////////////

func (s *service) GetSupportedVersions(
	ctx context.Context,
	req *csi.GetSupportedVersionsRequest) (
	*csi.GetSupportedVersionsResponse, error) {

	if err := s.initSupportedVersionsOnce(ctx); err != nil {
		return nil, err
	}
	return &csi.GetSupportedVersionsResponse{
		Reply: &csi.GetSupportedVersionsResponse_Result_{
			Result: &csi.GetSupportedVersionsResponse_Result{
				SupportedVersions: s.versions,
			},
		},
	}, nil
}

func (s *service) GetPluginInfo(
	ctx context.Context,
	req *csi.GetPluginInfoRequest) (
	*csi.GetPluginInfoResponse, error) {

	c, err := s.dialIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrGetPluginInfo(2, v), nil
	}
	return c.GetPluginInfo(ctx, req)
}

////////////////////////////////////////////////////////////////////////////////
//                               Node Service                                 //
////////////////////////////////////////////////////////////////////////////////

func (s *service) NodePublishVolume(
	ctx context.Context,
	req *csi.NodePublishVolumeRequest) (
	*csi.NodePublishVolumeResponse, error) {

	idObj := req.GetVolumeId()
	if idObj == nil {
		// MISSING_REQUIRED_FIELD
		return ErrNodePublishVolumeGeneral(3, "missing id obj"), nil
	}

	idVals := idObj.GetValues()
	if len(idVals) == 0 {
		// MISSING_REQUIRED_FIELD
		return ErrNodePublishVolumeGeneral(3, "missing id map"), nil
	}

	c, err := s.dialNode(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrNodePublishVolumeGeneral(2, v), nil
	}
	return c.NodePublishVolume(ctx, req)
}

func (s *service) NodeUnpublishVolume(
	ctx context.Context,
	req *csi.NodeUnpublishVolumeRequest) (
	*csi.NodeUnpublishVolumeResponse, error) {

	idObj := req.GetVolumeId()
	if idObj == nil {
		// MISSING_REQUIRED_FIELD
		return ErrNodeUnpublishVolumeGeneral(3, "missing id obj"), nil
	}

	idVals := idObj.GetValues()
	if len(idVals) == 0 {
		// MISSING_REQUIRED_FIELD
		return ErrNodeUnpublishVolumeGeneral(3, "missing id map"), nil
	}

	c, err := s.dialNode(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrNodeUnpublishVolumeGeneral(2, v), nil
	}
	return c.NodeUnpublishVolume(ctx, req)
}

func (s *service) GetNodeID(
	ctx context.Context,
	req *csi.GetNodeIDRequest) (
	*csi.GetNodeIDResponse, error) {

	c, err := s.dialNode(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrGetNodeIDGeneral(2, v), nil
	}
	return c.GetNodeID(ctx, req)
}

func (s *service) ProbeNode(
	ctx context.Context,
	req *csi.ProbeNodeRequest) (
	*csi.ProbeNodeResponse, error) {

	c, err := s.dialNode(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrProbeNodeGeneral(2, v), nil
	}
	return c.ProbeNode(ctx, req)
}

func (s *service) NodeGetCapabilities(
	ctx context.Context,
	req *csi.NodeGetCapabilitiesRequest) (
	*csi.NodeGetCapabilitiesResponse, error) {

	c, err := s.dialNode(ctx)
	if err != nil {
		return nil, err
	}
	if v := s.chkReqVersion(ctx, req); len(v) != 0 {
		// UNSUPPORTED_REQUEST_VERSION
		return ErrNodeGetCapabilities(2, v), nil
	}
	return c.NodeGetCapabilities(ctx, req)
}
