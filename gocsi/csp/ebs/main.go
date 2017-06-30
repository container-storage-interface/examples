package main

import "C"

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/context"
	"google.golang.org/grpc"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"

	"github.com/container-storage-interface/examples/gocsi/csp/ebs/csi"
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

const name = "ebs"

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
	client *ec2.EC2
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

	// init aws
	sess := session.New()
	config := &aws.Config{
		Credentials: credentials.NewChainCredentials(
			[]credentials.Provider{
				&credentials.EnvProvider{},
				&credentials.SharedCredentialsProvider{},
				&ec2rolecreds.EC2RoleProvider{
					Client: ec2metadata.New(sess),
				},
			},
		),
	}
	if v := os.Getenv("AWS_REGION"); v != "" {
		config.Region = aws.String(v)
	}
	if v := os.Getenv("AWS_ENDPOINT"); v != "" {
		config.Endpoint = aws.String(v)
	}
	s.client = ec2.New(sess, config)
	log.Println("aws initialized")

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

	s.Lock()
	defer s.Unlock()

	name := req.GetName()
	in := &ec2.CreateVolumeInput{}

	// set the volume size
	if v := req.GetCapacityRange(); v != nil {
		in.Size = aws.Int64(int64(v.RequiredBytes))
	}

	var tags []*ec2.Tag

	// set additional options
	params := req.GetParameters()
	for k, v := range params {
		if strings.EqualFold(k, "availabilityzone") {
			in.AvailabilityZone = aws.String(v)
			continue
		}
		if strings.EqualFold(k, "dryrun") {
			b, _ := strconv.ParseBool(v)
			in.DryRun = aws.Bool(b)
			continue
		}
		if strings.EqualFold(k, "encrypted") {
			b, _ := strconv.ParseBool(v)
			in.Encrypted = aws.Bool(b)
			continue
		}
		if strings.EqualFold(k, "iops") {
			i, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				// INVALID_PARAMETER_VALUE
				return ErrCreateVolume(
					7, fmt.Sprintf("invalid iops: %+v", v)), nil
			}
			in.Iops = aws.Int64(i)
			continue
		}
		if strings.EqualFold(k, "kmskeyid") {
			in.KmsKeyId = aws.String(v)
			continue
		}
		if strings.EqualFold(k, "snapshotid") {
			in.SnapshotId = aws.String(v)
			continue
		}
		if strings.EqualFold(k, "volumetype") {
			in.VolumeType = aws.String(v)
			continue
		}
		if strings.EqualFold(k, "tags") {
			for _, te := range strings.Split(v, ",") {
				tp := strings.SplitN(te, "=", 2)
				var tk string
				var tv string
				switch len(tp) {
				case 1:
					tk = tp[0]
					tv = "true"
				case 2:
					tk = tp[0]
					tv = tp[1]
				}
				tags = append(tags, &ec2.Tag{
					Key:   aws.String(tk),
					Value: aws.String(tv),
				})
			}
			continue
		}
	}

	// availability zone is required
	if in.AvailabilityZone == nil {
		// INVALID_VOLUME_NAME
		return ErrCreateVolume(3, "missing availability zone"), nil
	}

	// check to see if the volume already exists
	xvols, err := s.client.DescribeVolumes(&ec2.DescribeVolumesInput{
		Filters: []*ec2.Filter{
			&ec2.Filter{
				Name:   aws.String("availability-zone"),
				Values: []*string{in.AvailabilityZone},
			},
			&ec2.Filter{
				Name:   aws.String("tag:Name"),
				Values: []*string{&name},
			},
		}})
	if err != nil {
		// UNDEFINED
		return ErrCreateVolumeGeneral(
			1, fmt.Sprintf("error: ebs xvol check failed: %+v", err)), nil
	}

	var volume *ec2.Volume

	if len(xvols.Volumes) > 0 {
		volume = xvols.Volumes[0]
	} else {
		// create a new volume
		nvol, err := s.client.CreateVolume(in)
		if err != nil {
			// UNDEFINED
			return ErrCreateVolumeGeneral(
				1, fmt.Sprintf(
					"error: ebs create volume failed: %+v", err)), nil
		}

		// tag the volume with the tags array as well as the
		// provided name of the volume
		tags = append(tags, &ec2.Tag{
			Key:   aws.String("Name"),
			Value: aws.String(name),
		})
		if _, err := s.client.CreateTags(&ec2.CreateTagsInput{
			Resources: []*string{nvol.VolumeId},
			Tags:      tags,
		}); err != nil {
			// UNDEFINED
			return ErrCreateVolumeGeneral(
				1, fmt.Sprintf(
					"error: volume: %s: tag volume failed: %+v",
					*nvol.VolumeId, err)), nil
		}
		nvol.Tags = tags
		// assign the new volume
		volume = nvol
	}

	return &csi.CreateVolumeResponse{
		Reply: &csi.CreateVolumeResponse_Result_{
			Result: &csi.CreateVolumeResponse_Result{
				VolumeInfo: toVolumeInfo(volume),
			},
		},
	}, nil
}

func (s *sp) DeleteVolume(
	ctx context.Context,
	req *csi.DeleteVolumeRequest) (
	*csi.DeleteVolumeResponse, error) {

	id, ok := req.GetVolumeId().GetValues()["id"]
	if !ok {
		// INVALID_VOLUME_ID
		return ErrDeleteVolume(3, "missing id val"), nil
	}

	s.Lock()
	defer s.Unlock()

	_, err := s.client.DeleteVolume(&ec2.DeleteVolumeInput{
		VolumeId: aws.String(id),
	})

	if aerr, ok := err.(awserr.Error); ok {
		msg := fmt.Sprintf(
			"error: awserr: %s: %s", aerr.Code(), aerr.Message())
		if strings.EqualFold(aerr.Code(), msg) {
			// VOLUME_DOES_NOT_EXIST
			return ErrDeleteVolume(5, msg), nil
		}
		// UNDEFINED
		return ErrDeleteVolumeGeneral(1, msg), nil
	}
	// InvalidVolume.NotFound

	if err != nil {
		// UNDEFINED
		return ErrDeleteVolumeGeneral(
			1, fmt.Sprintf(
				"error: volume: %s: delete failed: %+v",
				id, err)), nil
	}

	return &csi.DeleteVolumeResponse{
		Reply: &csi.DeleteVolumeResponse_Result_{
			Result: &csi.DeleteVolumeResponse_Result{},
		},
	}, nil
}

func (s *sp) ControllerPublishVolume(
	ctx context.Context,
	req *csi.ControllerPublishVolumeRequest) (
	*csi.ControllerPublishVolumeResponse, error) {

	id, ok := req.GetVolumeId().GetValues()["id"]
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
	_ = id
	_ = nidid

	s.Lock()
	defer s.Unlock()

	return &csi.ControllerPublishVolumeResponse{
		Reply: &csi.ControllerPublishVolumeResponse_Result_{
			Result: &csi.ControllerPublishVolumeResponse_Result{
				PublishVolumeInfo: &csi.PublishVolumeInfo{
					Values: map[string]string{},
				},
			},
		},
	}, nil
}

func (s *sp) ControllerUnpublishVolume(
	ctx context.Context,
	req *csi.ControllerUnpublishVolumeRequest) (
	*csi.ControllerUnpublishVolumeResponse, error) {

	id, ok := req.GetVolumeId().GetValues()["id"]
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

	_ = id
	_ = nidid

	s.Lock()
	defer s.Unlock()

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

	in := &ec2.DescribeVolumesInput{}
	if v := req.GetMaxEntries(); v > 0 {
		in.MaxResults = aws.Int64(int64(v))
	}
	if v := req.GetStartingToken(); len(v) > 0 {
		in.NextToken = aws.String(v)
	}

	out, err := s.client.DescribeVolumes(in)
	if err != nil {
		// UNDEFINED
		return ErrListVolumes(1, err.Error()), nil
	}

	entries := make([]*csi.ListVolumesResponse_Result_Entry, len(out.Volumes))
	for x, volume := range out.Volumes {
		entries[x] = &csi.ListVolumesResponse_Result_Entry{
			VolumeInfo: toVolumeInfo(volume),
		}
	}

	var nextToken string
	if v := out.NextToken; v != nil {
		nextToken = *v
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

	id, ok := req.GetVolumeId().GetValues()["id"]
	if !ok {
		// MISSING_REQUIRED_FIELD
		return ErrNodePublishVolumeGeneral(3, "missing id val"), nil
	}

	s.Lock()
	defer s.Unlock()

	_ = id

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

	s.Lock()
	defer s.Unlock()

	id, ok := req.GetVolumeId().GetValues()["id"]
	if !ok {
		// VOLUME_DOES_NOT_EXIST
		return ErrNodeUnpublishVolume(2, "missing id val"), nil
	}

	_ = id

	return &csi.NodeUnpublishVolumeResponse{
		Reply: &csi.NodeUnpublishVolumeResponse_Result_{
			Result: &csi.NodeUnpublishVolumeResponse_Result{},
		},
	}, nil
}

const iidURL = "http://169.254.169.254/" +
	"latest/dynamic/instance-identity/document"

type instanceIdentityDoc struct {
	InstanceID       string `json:"instanceId,omitempty"`
	Region           string `json:"region,omitempty"`
	AvailabilityZone string `json:"availabilityZone,omitempty"`
}

func (s *sp) GetNodeID(
	ctx context.Context,
	req *csi.GetNodeIDRequest) (
	*csi.GetNodeIDResponse, error) {

	hreq, err := http.NewRequest(http.MethodGet, iidURL, nil)
	if err != nil {
		// UNDEFINED
		return ErrGetNodeIDGeneral(1, err.Error()), nil
	}

	hres, err := http.DefaultClient.Do(hreq)
	if err != nil {
		// UNDEFINED
		return ErrGetNodeIDGeneral(1, err.Error()), nil
	}

	defer hres.Body.Close()

	iid := instanceIdentityDoc{}
	dec := json.NewDecoder(hres.Body)
	if err := dec.Decode(&iid); err != nil {
		// UNDEFINED
		return ErrGetNodeIDGeneral(1, err.Error()), nil
	}

	return &csi.GetNodeIDResponse{
		Reply: &csi.GetNodeIDResponse_Result_{
			Result: &csi.GetNodeIDResponse_Result{
				NodeId: &csi.NodeID{
					Values: map[string]string{
						"instanceID":       iid.InstanceID,
						"region":           iid.Region,
						"availabilityZone": iid.AvailabilityZone,
					},
				},
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
)

func toVolumeInfo(volume *ec2.Volume) *csi.VolumeInfo {

	volInfo := &csi.VolumeInfo{
		Id: &csi.VolumeID{
			Values: map[string]string{
				"id": *volume.VolumeId,
			},
		},
		Metadata: &csi.VolumeMetadata{
			Values: map[string]string{},
		},
	}

	// if the size was set and it's not a negative number
	// then return it with the volume info. it cannot be
	// negative since CSI's VolumeInfo.CapacityBytes is an
	// unsigned integer
	if v := volume.Size; v != nil && *v >= 0 {
		volInfo.CapacityBytes = uint64(*v)
	}
	if v := volume.AvailabilityZone; v != nil {
		volInfo.Metadata.Values["availabilityZone"] = *v
	}
	if v := volume.CreateTime; v != nil {
		volInfo.Metadata.Values["createTime"] = (*v).String()
	}
	if v := volume.Encrypted; v != nil {
		volInfo.Metadata.Values["encrypted"] = fmt.Sprintf("%v", *v)
	}
	if v := volume.Iops; v != nil {
		volInfo.Metadata.Values["iops"] = fmt.Sprintf("%d", *v)
	}
	if v := volume.KmsKeyId; v != nil {
		volInfo.Metadata.Values["kmsKeyID"] = *v
	}
	if v := volume.SnapshotId; v != nil {
		volInfo.Metadata.Values["snapshotID"] = *v
	}
	if v := volume.State; v != nil {
		volInfo.Metadata.Values["state"] = *v
	}
	var name string
	if v := volume.Tags; len(v) > 0 {
		buf := &bytes.Buffer{}
		for x, t := range v {
			if t.Key == nil {
				continue
			}
			tkey := *t.Key
			fmt.Fprintf(buf, tkey)
			if t.Value == nil {
				continue
			}
			tval := *t.Value
			if tkey == "Name" {
				name = tval
			}
			fmt.Fprintf(buf, "=%s", tval)
			if x < len(v)-1 {
				fmt.Fprintf(buf, ",")
			}
		}
		volInfo.Metadata.Values["tags"] = buf.String()
	}
	if v := volume.VolumeType; v != nil {
		volInfo.Metadata.Values["volumeType"] = *v
	}
	if name != "" {
		volInfo.Id.Values["name"] = name
	}

	return volInfo
}
