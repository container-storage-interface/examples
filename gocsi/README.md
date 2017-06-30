# gocsi
This project provides a Go-based CSI client and server capable of
supporting additional storage platforms at runtime via Go plug-ins.

| Name | Description |
|------|-------------|
| Container Storage Client ([`csc`](./csc)) | A command line interface (CLI) tool that provides analogues for all of the CSI RPCs. |
| Container Storage Daemon ([`csd`](./csd)) | A CLI tool that serves the CSI services -- Controller, Identity, and Node -- on a TCP port or UNIX socket. The storage platform support for `csd` is provided via Go plug-ins, loadable at runtime. |
| Container Storage Providers ([`csp`](./csp)) | A directory containing Go packages that can be built as stand-alone CLI programs capable of serving the CSI services via a TCP port or UNIX socket, similar to `csd`. Additionally, the `csp` packages can **also** be built as Go plug-ins that the `csd` program can load, extending its storage provider support dynamically, at runtime.

## Getting Started
The example below illustrates how the above tools and Go plug-ins
work together to provide a cohesive experience in line with the CSI
specification.

Please note that the mock `csp` is used to fulfill the role of a 
storage platform. Also, because Go plug-ins are demonstrated this
example must be executed on a Linux platform as Go plug-ins are not
yet supported by other operating systems (OS).

```bash
# get and install the sources
$ go get github.com/container-storage-interface/examples/gocsi

# build the container storage client
$ go install github.com/container-storage-interface/examples/gocsi/csc

# build the container storage daemon
$ go install github.com/container-storage-interface/examples/gocsi/csd

# build the mock container storage provider
$ go build -o mock.so -buildmode plugin github.com/container-storage-interface/examples/gocsi/csp/moc

# export the CSI endpoint
$ export CSI_ENDPOINT=tcp://127.0.0.1:8080

# start the server (assuming $GOPATH/bin is in the PATH)
$ CSI_PLUGINS=$(pwd)/mock.so csd mock > csd.log 2>&1 &
[1] 19050

# use the client to ask for a list of volumes
$ csc ls
id=1	name=Mock Volume 1	
id=2	name=Mock Volume 2	
id=3	name=Mock Volume 3

# create a new volume
$ csc new "My New Volume"
id=4	name=My New Volume

# query the volume list again
$ csc ls
id=1	name=Mock Volume 1	
id=2	name=Mock Volume 2	
id=3	name=Mock Volume 3	
id=4	name=My New Volume

# kill the server
kill -HUP $(ps aux | grep '[c]sd' | awk '{print $2}')

# view the server log
$ cat csd.log
2017/06/26 01:54:48 loaded plug-in: mock.so
2017/06/26 01:54:48 registered endpoint: mock
2017/06/26 01:54:48 mock.Serve
2017/06/26 01:55:36 csd.ListVolumes
2017/06/26 01:55:36 ...Volume.ID=1
2017/06/26 01:55:36 ...Volume.ID=2
2017/06/26 01:55:36 ...Volume.ID=3
2017/06/26 01:55:47 csd.CreateVolume
2017/06/26 01:55:47 CreateVolume.CapacityRange=<nil>
2017/06/26 01:55:47 CreateVolume.Name=My New Volume
2017/06/26 01:55:47 CreateVolume.Parameters=map[]
2017/06/26 01:55:47 CreateVolume.VolumeCapabilities=[]
2017/06/26 01:55:47 ...Volume.ID=4
2017/06/26 01:56:04 csd.ListVolumes
2017/06/26 01:56:04 ...Volume.ID=1
2017/06/26 01:56:04 ...Volume.ID=2
2017/06/26 01:56:04 ...Volume.ID=3
2017/06/26 01:56:04 ...Volume.ID=4
received signal: terminated: shutting down
server stopped gracefully
```

## Build Reference
GoCSI can be built entirely using `go build` and `go install`. However,
Make is also used in order to create a more deterministic and portable
build process.

| Make Target | Description |
|-------------|-------------|
| `build` | Builds `csc`, `csd`, and all of the `csp`s. Please note that this target does not install anything to the `$GOPATH`. Instead each of the packages' own Makefiles ensure that all code is built in a directory named `.build` inside the package using the `go` tool's `-pkgdir` flag in conjunction with the environment variable `GOBIN`. This ensures builds that are easy to clean. |
| `clean` | Removes the artifacts produced by the `build` target. |
| `clobber` | Depends on `clean`. Removes all generated sources and vendored dependencies. |
| `test` | Depends on `build`. Executes an end-to-end example using `csc`, `csd`, and the mock `csp`.
| `bencmark` | Usses `go test` to illustrate the performance of the `PipeConn` used by `csd` to host the `csp`s as in-memory CSI endpoints. |
| `goget` | Will scan the packages and execute `go get` for any missing dependencies. |


## Frequently Asked Questions
This section answers some of the common inquiries related to this project.

**What is the purpose of GoCSI?**

The purpose of GoCSI is not to provide a reference library or set
of tools meant to be used by other, third-party or external consumers
wishing to jumpstart CSI development. GoCSI is simply a series of small
programs and packages that provide working examples and demonstrations
of the CSI specification. If at some point the CSI working group wishes
to build a set of reference implementations based on the contents of
this project, then that is fine. If GoCSI stands forever as simply a
series of examples, that's fine as well.

**Why vendor Proto and gRPC in the `csp`s?**

There are two answers to this question. The first is simply so that
the examples included in GoCSI can be built/executed with standard
Go patterns without the need for an external build system such as 
[Make](https://www.gnu.org/software/make/) or vendoring solution like
[Glide](https://glide.sh/). The fact is that executing `make clobber`
at the root of the GoCSI project will remove all of the vendored 
code as well as generated sources. Running `make` again will restore
all of the removed code. It's simply by design to leave that code
as part of the commit in order to make it as easy as possible to use
the examples included in GoCSI.

The second answer is the result of weeks of research and trial and 
error. The short answer has to do with how Go manages packages, plug-ins,
and the gRPC type registry. The long answer is summarized by 
[golang/go#20481](https://github.com/golang/go/issues/20481).

The purpose of the `csp`s is to demonstrate the triviality of creating a 
package that can be built both as a stand-alone CSI endpoint and a Go
plug-in, capable of providing support for a new storage platform to
a separate, completely unrelated host program. Therefore the `csp`s 
should have absolutely no relationship to any other GoCSI project, 
even `csd`, the program capable of loading the `csp`s as Go plug-ins.

Truthfully the matter is much more detailed, but it won't be repeated 
here. For that please see the aforementioned Golang issue. Suffice
it to say, the only way to create Go plug-ins that are useful in
production software is to 1) vendor everything inside the plug-in's
package and 2) use only Go stdlib types and empty interfaces when 
communicating with the plug-in from the host program.

**Why generate Go sources from the CSI protobuf in multiple locations?**

The answer to this question is similar to the answer of the previous
question. If the `csp`s referenced the `csi` package at the root of the
GoCSI project then the `csd` program would not be able to load the Go 
plug-ins due to 1) duplicate gRPC type registrations and 2) a possibly
different hash for the referenced package since the plug-ins are built
separately and thus potentially from different sources.
