package main

import (
	"context"
	"fmt"
	"os"
	"regexp"

	"google.golang.org/grpc"

	"github.com/container-storage-interface/examples/gocsi/csi"
)

func main() {

	protoAddr := os.Getenv("CSI_ENDPOINT")
	if protoAddr == "" {
		protoAddr = "tcp://127.0.0.1:8080"
	}
	_, addr, err := parseProtoAddr(protoAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid endpoint: %s\n", protoAddr)
		os.Exit(1)
	}

	ctx := context.Background()

	grpcClient, err := grpc.DialContext(ctx, addr, grpc.WithInsecure())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: dial failed: %s\n", err)
		os.Exit(1)
	}

	ctrlrClient := csi.NewControllerClient(grpcClient)

	resp, err := ctrlrClient.ListVolumes(ctx, &csi.ListVolumesRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: list volumes failed: %s\n", err)
		os.Exit(1)
	}

	if result := resp.GetResult(); result != nil {
		for _, e := range result.Entries {
			if v := e.VolumeInfo; v != nil {
				if v.Id != nil {
					if id, ok := v.Id.Values["id"]; ok {
						fmt.Printf("VolumeID=%v\n", id)
					}
				}
			}
		}
	}
}

var addrRX = regexp.MustCompile(
	`(?i)^((?:(?:tcp|udp|ip)[46]?)|(?:unix(?:gram|packet)?))://(.+)$`)

func parseProtoAddr(protoAddr string) (proto string, addr string, err error) {
	m := addrRX.FindStringSubmatch(protoAddr)
	if m == nil {
		return "", "", fmt.Errorf("invalid address: %v", protoAddr)
	}
	return m[1], m[2], nil
}
