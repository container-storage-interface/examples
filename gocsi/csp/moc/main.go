package main

import "C"

import (
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"

	"github.com/container-storage-interface/examples/gocsi/csp/moc/csi"
)

////////////////////////////////////////////////////////////////////////////////
//                                 CLI                                        //
////////////////////////////////////////////////////////////////////////////////

// main is ignored when this package is built as a go plug-in
func main() {
	l, err := GetCSIEndpointListener()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to listen: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	s := &sp{}

	if err := s.Serve(ctx, l); err != nil {
		fmt.Fprintf(os.Stderr, "error: grpc failed: %v\n", err)
		os.Exit(1)
	}
}

////////////////////////////////////////////////////////////////////////////////
//                              Go Plug-in                                    //
////////////////////////////////////////////////////////////////////////////////

const name = "mock"

var (
	errServerStarted = errors.New("gocsi: the server has been started")
	errServerStopped = errors.New("gocsi: the server has been stopped")
)

// ServiceProviders is an exported symbol that provides a host program
// with a map of the service provider names and constructors.
var ServiceProviders = map[string]func() interface{}{
	name: func() interface{} { return &sp{name: name} },
}

type sp struct {
	sync.Mutex
	name   string
	server *grpc.Server
	closed bool
}

// ServiceProvider.Serve
func (s *sp) Serve(ctx context.Context, li net.Listener) error {
	log.Println(name + ".Serve")
	if err := func() error {
		s.Lock()
		defer s.Unlock()
		if s.closed {
			return errServerStopped
		}
		if s.server != nil {
			return errServerStarted
		}
		s.server = grpc.NewServer()
		return nil
	}(); err != nil {
		return errServerStarted
	}
	csi.RegisterControllerServer(s.server, s)
	csi.RegisterIdentityServer(s.server, s)
	csi.RegisterNodeServer(s.server, s)

	// start the grpc server
	if err := s.server.Serve(li); err != grpc.ErrServerStopped {
		return err
	}
	return errServerStopped
}

//  ServiceProvider.Stop
func (s *sp) Stop(ctx context.Context) {
	log.Println(name + ".Stop")
	s.Lock()
	defer s.Unlock()

	if s.closed || s.server == nil {
		return
	}
	s.server.Stop()
	s.server = nil
	s.closed = true
}

//  ServiceProvider.GracefulStop
func (s *sp) GracefulStop(ctx context.Context) {
	log.Println(name + ".GracefulStop")
	s.Lock()
	defer s.Unlock()

	if s.closed || s.server == nil {
		return
	}
	s.server.GracefulStop()
	s.server = nil
	s.closed = true
}

////////////////////////////////////////////////////////////////////////////////
//                            Controller Service                              //
////////////////////////////////////////////////////////////////////////////////

func (s *sp) CreateVolume(
	ctx context.Context,
	req *csi.CreateVolumeRequest) (
	*csi.CreateVolumeResponse, error) {

	log.Printf(
		"mock.CreateVolums.Version=%s\n",
		SprintfVersion(req.GetVersion()))
	log.Printf(
		"mock.CreateVolums.CapacityRange=%+v\n",
		req.GetCapacityRange())
	log.Printf(
		"mock.CreateVolums.Name=%v\n",
		req.GetName())
	log.Printf(
		"mock.CreateVolums.Parameters=%+v\n",
		req.GetParameters())
	log.Printf(
		"mock.CreateVolums.VolumeCapabilities=%+v\n",
		req.GetVolumeCapabilities())

	// assert that the name is not empty
	name := req.GetName()
	if name == "" {
		// INVALID_VOLUME_NAME
		return ErrCreateVolume(3, "missing name"), nil
	}

	s.Lock()
	defer s.Unlock()

	// the creation process is idempotent: if the volume
	// does not already exist then create it, otherwise
	// just return the existing volume
	_, v := findVolByName(name)
	if v == nil {
		capacity := gib100
		if cr := req.GetCapacityRange(); cr != nil {
			if rb := cr.GetRequiredBytes(); rb != 0 {
				capacity = rb
			}
		}
		v = newVolume(name, capacity)
		vols = append(vols, v)
	}

	log.Printf("...Volums.ID=%s\n", v.Id.Values["id"])

	return &csi.CreateVolumeResponse{
		Reply: &csi.CreateVolumeResponse_Result_{
			Result: &csi.CreateVolumeResponse_Result{
				VolumeInfo: v,
			},
		},
	}, nil
}

func (s *sp) DeleteVolume(
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

	id, ok := idVals["id"]
	if !ok {
		// INVALID_VOLUME_ID
		return ErrDeleteVolume(3, "missing id val"), nil
	}

	s.Lock()
	defer s.Unlock()

	x, v := findVol("id", id)
	if v != nil {
		// this delete logic won't preserve order,
		// but it will prevent any potential mem
		// leaks due to orphaned references
		vols[x] = vols[len(vols)-1]
		vols[len(vols)-1] = nil
		vols = vols[:len(vols)-1]
	}

	return nil, nil
}

func (s *sp) ControllerPublishVolume(
	ctx context.Context,
	req *csi.ControllerPublishVolumeRequest) (
	*csi.ControllerPublishVolumeResponse, error) {

	log.Printf(
		"mock.ControllerPublishVolums.Version=%s\n",
		SprintfVersion(req.GetVersion()))
	log.Printf(
		"mock.ControllerPublishVolums.VolumeID=%+v\n",
		req.GetVolumeId())
	log.Printf(
		"mock.ControllerPublishVolums.VolumeMetadata=%v\n",
		req.GetVolumeMetadata())
	log.Printf(
		"mock.ControllerPublishVolums.NodeID=%+v\n",
		req.GetNodeId())
	log.Printf(
		"mock.ControllerPublishVolums.ReadOnly=%+v\n",
		req.GetReadonly())

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

	id, ok := idVals["id"]
	if !ok {
		// INVALID_VOLUME_ID
		return ErrControllerPublishVolume(3, "missing id val"), nil
	}

	nid := req.GetNodeId()
	if nid == nil {
		// INVALID_NODE_ID
		return ErrControllerPublishVolume(7, "missing node id"), nil
	}

	nidv := nid.GetValues()
	if len(nidv) == 0 {
		// INVALID_NODE_ID
		return ErrControllerPublishVolume(7, "missing node id"), nil
	}

	nidid, ok := nidv["id"]
	if !ok {
		// INVALID_NODE_ID
		return ErrControllerPublishVolume(7, "node id required"), nil
	}

	// the key used with the volume's metadata to see if the volume
	// is attached to a given node id
	attk := fmt.Sprintf("devpath.%s", nidid)

	s.Lock()
	defer s.Unlock()

	_, v := findVol("id", id)
	if v == nil {
		// VOLUME_DOES_NOT_EXIST
		return ErrControllerPublishVolume(5, "missing volume"), nil
	}

	// a "new" device path
	var devpath string

	// check to see if the volume is attached to this nods. if it
	// is then return the existing dev path
	if p, ok := v.Metadata.Values[attk]; ok {
		devpath = p
	} else {
		// attach the volume
		devpath = fmt.Sprintf("%d", time.Now().UTC().Unix())
		v.Metadata.Values[attk] = devpath
	}

	resp := &csi.ControllerPublishVolumeResponse{
		Reply: &csi.ControllerPublishVolumeResponse_Result_{
			Result: &csi.ControllerPublishVolumeResponse_Result{
				PublishVolumeInfo: &csi.PublishVolumeInfo{
					Values: map[string]string{
						"devpath": devpath,
					},
				},
			},
		},
	}

	log.Printf("mock.ControllerPublishVolums.Response=%+v\n", resp)
	return resp, nil
}

func (s *sp) ControllerUnpublishVolume(
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

	id, ok := idVals["id"]
	if !ok {
		// INVALID_VOLUME_ID
		return ErrControllerUnpublishVolume(3, "missing id val"), nil
	}

	nid := req.GetNodeId()
	if nid == nil {
		// INVALID_NODE_ID
		return ErrControllerUnpublishVolume(7, "missing node id"), nil
	}

	nidv := nid.GetValues()
	if len(nidv) == 0 {
		// INVALID_NODE_ID
		return ErrControllerUnpublishVolume(7, "missing node id"), nil
	}

	nidid, ok := nidv["id"]
	if !ok {
		// NODE_ID_REQUIRED
		return ErrControllerUnpublishVolume(9, "node id required"), nil
	}

	// the key used with the volume's metadata to see if the volume
	// is attached to a given node id
	attk := fmt.Sprintf("devpath.%s", nidid)

	s.Lock()
	defer s.Unlock()

	_, v := findVol("id", id)
	if v == nil {
		// VOLUME_DOES_NOT_EXIST
		return ErrControllerUnpublishVolume(5, "missing volume"), nil
	}

	// check to see if the volume is attached to thi node
	if _, ok := v.Metadata.Values[attk]; !ok {
		// VOLUME_NOT_ATTACHED_TO_SPECIFIED_NODE
		return ErrControllerUnpublishVolume(8, "not attached"), nil
	}

	// zero out the device path for this node
	delete(v.Metadata.Values, attk)

	return &csi.ControllerUnpublishVolumeResponse{
		Reply: &csi.ControllerUnpublishVolumeResponse_Result_{
			Result: &csi.ControllerUnpublishVolumeResponse_Result{},
		},
	}, nil
}

func (s *sp) ValidateVolumeCapabilities(
	ctx context.Context,
	req *csi.ValidateVolumeCapabilitiesRequest) (
	*csi.ValidateVolumeCapabilitiesResponse, error) {

	return nil, nil
}

func (s *sp) ListVolumes(
	ctx context.Context,
	req *csi.ListVolumesRequest) (
	*csi.ListVolumesResponse, error) {

	s.Lock()
	defer s.Unlock()

	var (
		ulenVols      = uint32(len(vols))
		maxEntries    = uint32(req.GetMaxEntries())
		startingToken uint32
	)

	if v := req.GetStartingToken(); v != "" {
		i, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return ErrListVolumes(0, fmt.Sprintf(
				"startingToken=%d !< uint32=%d",
				startingToken, math.MaxUint32)), nil
		}
		startingToken = uint32(i)
	}

	if startingToken > ulenVols {
		return ErrListVolumes(0, fmt.Sprintf(
			"startingToken=%d > len(vols)=%d",
			startingToken, ulenVols)), nil
	}

	entries := []*csi.ListVolumesResponse_Result_Entry{}
	lena := uint32(0)
	for x := startingToken; x < ulenVols; x++ {
		if maxEntries > 0 && lena >= maxEntries {
			break
		}
		v := vols[x]
		log.Printf("...Volums.ID=%s\n", v.Id.Values["id"])
		entries = append(entries,
			&csi.ListVolumesResponse_Result_Entry{VolumeInfo: v})
		lena++
	}

	var nextToken string
	if (startingToken + lena) < ulenVols {
		nextToken = fmt.Sprintf("%d", startingToken+lena)
		fmt.Printf("nextToken=%s\n", nextToken)
	}

	return &csi.ListVolumesResponse{
		Reply: &csi.ListVolumesResponse_Result_{
			Result: &csi.ListVolumesResponse_Result{
				Entries:   entries,
				NextToken: nextToken,
			},
		},
	}, nil
}

func (s *sp) GetCapacity(
	ctx context.Context,
	req *csi.GetCapacityRequest) (
	*csi.GetCapacityResponse, error) {

	return &csi.GetCapacityResponse{
		Reply: &csi.GetCapacityResponse_Result_{
			Result: &csi.GetCapacityResponse_Result{
				TotalCapacity: tib100,
			},
		},
	}, nil
}

func (s *sp) ControllerGetCapabilities(
	ctx context.Context,
	req *csi.ControllerGetCapabilitiesRequest) (
	*csi.ControllerGetCapabilitiesResponse, error) {

	return &csi.ControllerGetCapabilitiesResponse{
		Reply: &csi.ControllerGetCapabilitiesResponse_Result_{
			Result: &csi.ControllerGetCapabilitiesResponse_Result{
				Capabilities: []*csi.ControllerServiceCapability{
					&csi.ControllerServiceCapability{
						Type: &csi.ControllerServiceCapability_Rpc{
							Rpc: &csi.ControllerServiceCapability_RPC{
								// CREATE_DELETE_VOLUME
								Type: 1,
							},
						},
					},
					&csi.ControllerServiceCapability{
						Type: &csi.ControllerServiceCapability_Rpc{
							Rpc: &csi.ControllerServiceCapability_RPC{
								// PUBLISH_UNPUBLISH_VOLUME
								Type: 2,
							},
						},
					},
					&csi.ControllerServiceCapability{
						Type: &csi.ControllerServiceCapability_Rpc{
							Rpc: &csi.ControllerServiceCapability_RPC{
								// LIST_VOLUMES
								Type: 3,
							},
						},
					},
					&csi.ControllerServiceCapability{
						Type: &csi.ControllerServiceCapability_Rpc{
							Rpc: &csi.ControllerServiceCapability_RPC{
								// GET_CAPACITY
								Type: 4,
							},
						},
					},
				},
			},
		},
	}, nil
}

////////////////////////////////////////////////////////////////////////////////
//                             Identity Service                               //
////////////////////////////////////////////////////////////////////////////////

func (s *sp) GetSupportedVersions(
	ctx context.Context,
	req *csi.GetSupportedVersionsRequest) (
	*csi.GetSupportedVersionsResponse, error) {

	return &csi.GetSupportedVersionsResponse{
		Reply: &csi.GetSupportedVersionsResponse_Result_{
			Result: &csi.GetSupportedVersionsResponse_Result{
				SupportedVersions: []*csi.Version{
					&csi.Version{
						Major: 0,
						Minor: 1,
						Patch: 0,
					},
				},
			},
		},
	}, nil
}

func (s *sp) GetPluginInfo(
	ctx context.Context,
	req *csi.GetPluginInfoRequest) (
	*csi.GetPluginInfoResponse, error) {

	return &csi.GetPluginInfoResponse{
		Reply: &csi.GetPluginInfoResponse_Result_{
			Result: &csi.GetPluginInfoResponse_Result{
				Name:          s.name,
				VendorVersion: "0.1.0",
				Manifest:      nil,
			},
		},
	}, nil
}

////////////////////////////////////////////////////////////////////////////////
//                                Node Service                                //
////////////////////////////////////////////////////////////////////////////////

func (s *sp) NodePublishVolume(
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

	id, ok := idVals["id"]
	if !ok {
		// MISSING_REQUIRED_FIELD
		return ErrNodePublishVolumeGeneral(3, "missing id val"), nil
	}

	s.Lock()
	defer s.Unlock()

	_, v := findVol("id", id)
	if v == nil {
		// VOLUME_DOES_NOT_EXIST
		return ErrNodePublishVolume(2, "missing volume"), nil
	}

	mntpath := req.GetTargetPath()
	if mntpath == "" {
		// UNSUPPORTED_MOUNT_OPTION
		return ErrNodePublishVolume(3, "missing mount path"), nil
	}

	// record the mount path
	v.Metadata.Values[nodeMntpath] = mntpath

	return &csi.NodePublishVolumeResponse{
		Reply: &csi.NodePublishVolumeResponse_Result_{
			Result: &csi.NodePublishVolumeResponse_Result{},
		},
	}, nil
}

func (s *sp) NodeUnpublishVolume(
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

	s.Lock()
	defer s.Unlock()

	id, ok := idVals["id"]
	if !ok {
		// VOLUME_DOES_NOT_EXIST
		return ErrNodeUnpublishVolume(2, "missing id val"), nil
	}

	_, v := findVol("id", id)
	if v == nil {
		// VOLUME_DOES_NOT_EXIST
		return ErrNodeUnpublishVolume(2, "missing volume"), nil
	}

	// zero out the mount path for this node
	delete(v.Metadata.Values, nodeMntpath)

	return &csi.NodeUnpublishVolumeResponse{
		Reply: &csi.NodeUnpublishVolumeResponse_Result_{
			Result: &csi.NodeUnpublishVolumeResponse_Result{},
		},
	}, nil
}

func (s *sp) GetNodeID(
	ctx context.Context,
	req *csi.GetNodeIDRequest) (
	*csi.GetNodeIDResponse, error) {

	return &csi.GetNodeIDResponse{
		Reply: &csi.GetNodeIDResponse_Result_{
			Result: &csi.GetNodeIDResponse_Result{
				NodeId: nodeID,
			},
		},
	}, nil
}

func (s *sp) ProbeNode(
	ctx context.Context,
	req *csi.ProbeNodeRequest) (
	*csi.ProbeNodeResponse, error) {

	return &csi.ProbeNodeResponse{
		Reply: &csi.ProbeNodeResponse_Result_{
			Result: &csi.ProbeNodeResponse_Result{},
		},
	}, nil
}

func (s *sp) NodeGetCapabilities(
	ctx context.Context,
	req *csi.NodeGetCapabilitiesRequest) (
	*csi.NodeGetCapabilitiesResponse, error) {

	return &csi.NodeGetCapabilitiesResponse{
		Reply: &csi.NodeGetCapabilitiesResponse_Result_{
			Result: &csi.NodeGetCapabilitiesResponse_Result{
				Capabilities: []*csi.NodeServiceCapability{
					&csi.NodeServiceCapability{
						Type: &csi.NodeServiceCapability_VolumeCapability{
							VolumeCapability: &csi.VolumeCapability{
								Value: &csi.VolumeCapability_Mount{
									Mount: &csi.VolumeCapability_MountVolume{
										FsType: "ext4",
										MountFlags: []string{
											"norootsquash",
											"uid=500",
											"gid=500",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}, nil
}

////////////////////////////////////////////////////////////////////////////////
//                                  Utils                                     //
////////////////////////////////////////////////////////////////////////////////

const (
	kib    uint64 = 1024
	mib    uint64 = kib * 1024
	gib    uint64 = mib * 1024
	gib100 uint64 = gib * 100
	tib    uint64 = gib * 1024
	tib100 uint64 = tib * 100

	nodeIDID    = "mock"
	nodeMntpath = nodeIDID + ".mntpath"
	nodeDevpath = nodeIDID + ".devpath"
)

var (
	nextVolID uint64

	vols = []*csi.VolumeInfo{
		newVolume("Mock Volume 1", gib100),
		newVolume("Mock Volume 2", gib100),
		newVolume("Mock Volume 3", gib100),
	}

	nodeID = &csi.NodeID{
		Values: map[string]string{
			"id": nodeIDID,
		},
	}

	version = &csi.Version{Major: 0, Minor: 1, Patch: 0}
)

func newVolume(name string, capcity uint64) *csi.VolumeInfo {
	id := atomic.AddUint64(&nextVolID, 1)
	vi := &csi.VolumeInfo{
		Id: &csi.VolumeID{
			Values: map[string]string{
				"id":   fmt.Sprintf("%d", id),
				"name": name,
			},
		},
		Metadata: &csi.VolumeMetadata{
			Values: map[string]string{},
		},
		CapacityBytes: capcity,
	}
	return vi
}

func findVolByID(id *csi.VolumeID) (int, *csi.VolumeInfo) {
	if id == nil || len(id.Values) == 0 {
		return -1, nil
	}
	if idv, ok := id.Values["id"]; ok {
		return findVol("id", idv)
	}
	if idv, ok := id.Values["name"]; ok {
		return findVol("name", idv)
	}
	return -1, nil
}

func findVolByName(name string) (int, *csi.VolumeInfo) {
	return findVol("name", name)
}

func findVol(field, val string) (int, *csi.VolumeInfo) {
	for x, v := range vols {
		id := v.Id
		if id == nil {
			continue
		}
		if len(id.Values) == 0 {
			continue
		}
		fv, ok := id.Values[field]
		if !ok {
			continue
		}
		if strings.EqualFold(fv, val) {
			return x, v
		}
	}
	return -1, nil
}
