# gocsi
The `gocsi` project is a CSI client written in Go:

```bash
# get and install the sources
$ go get github.com/container-storage-interface/examples/gocsi

# run gocsi (assuming $GOPATH/bin is in the PATH)
$ gocsi
```

## Build Reference
This section describes how to build the project.

### Generated Sources
While the CSI protobuf file and source are versioned with the project
at `csi/csi.proto` and `csi.pb.go`, it is still possible to generate
them from a remote CSI specification file.

First, remove the generated files:

```bash
$ make clobber
```

Next, make the target `csi/csi.pb.go`:

```bash
$ make csi/csi.pb.go
```

It's also possible to influence the location from which the CSI specification
is retrieved with the following environment variables:

| Name | Description | Default |
|------|-------------|---------|
| `CSI_SPEC_FILE` | The path to a local spec file used to generate the protobuf | |
| `CSI_GIT_OWNER` | The GitHub user or organization that owns the git repository that contains the CSI spec file | `container-storage-interface` |
| `CSI_GIT_REPO` | The GitHub repository that contains the CSI spec file | `spec` |
| `CSI_GIT_REF` | The git ref to use when getting the CSI spec file. This value can be a branch name, a tag, or a git commit ID | `master` |
| `CSI_SPEC_NAME` | The name of the CSI spec markdown file | `spec.md` |
| `CSI_SPEC_PATH` | The remote path of the CSI markdown file | |
| `CSI_PROTO_NAME` | The name of the protobuf file to generate. This value should not include the file extension | `csi` |
| `CSI_PROTO_DIR` | The path of the directory in which the protobuf and Go source files will be generated. If this directory does not exist it will be created. | `.` |
| `CSI_PROTO_ADD` | A list of additional protobuf files used when building the Go source file | |
| `CSI_IMPORT_PATH` | The package of the generated Go source | `csi` |

### CSI Client (gocsi)
The `gocsi` binary may be built with the following command:

```bash
$ make gocsi
```
