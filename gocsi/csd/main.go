package main

import (
	"fmt"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"

	"golang.org/x/net/context"

	"github.com/container-storage-interface/examples/gocsi"
)

func main() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc)

	if len(os.Args) < 2 {
		fmt.Fprintf(
			os.Stderr,
			"usage: %s [GOPLUGIN_PATH [GOPLUGIN_PATH...]] "+
				"TYPE[:SERVICE] [TYPE[:SERVICE]...]\n",
			path.Base(os.Args[0]))
		os.Exit(1)
	}

	ctx := context.Background()

	// parse the args to get the go plug-in paths and
	// service definitions
	var gpPaths []string
	var svcDefs [][]string

	for _, arg := range os.Args[1:] {

		// if the argument is a valid file path then
		// treat it as a go plug-in
		if fileExists(arg) {
			gpPaths = append(gpPaths, arg)
			continue
		}

		// parse the service type and name
		svcDefs = append(svcDefs, strings.SplitN(arg, ":", 2))
	}

	// load the GoCSI service provider plug-ins
	if err := gocsi.LoadGoPlugins(ctx, gpPaths...); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to load go plug-ins: %v\n", err)
		os.Exit(1)
	}

	addr := os.Getenv("CSI_ENDPOINT")
	if addr == "" {
		fmt.Fprintln(os.Stderr, "missing CSI_ENDPOINT")
		os.Exit(1)
	}

	// create the services
	var svcs []gocsi.Service
	for _, sdef := range svcDefs {
		var stype string
		var sname string
		switch len(sdef) {
		case 1:
			stype = sdef[0]
			sname = sdef[0]
		case 2:
			stype = sdef[0]
			sname = sdef[1]
		}
		s, err := gocsi.NewService(ctx, stype, sname)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: new service failed: %v\n", err)
			os.Exit(1)
		}
		svcs = append(svcs, s)
	}

	// create the GoCSI server
	server := &gocsi.Server{Addr: addr, Services: svcs}

	trapSignals(func() {
		server.GracefulStop(ctx)
		fmt.Fprintln(os.Stderr, "server stopped gracefully")
	}, func() {
		server.Stop(ctx)
		fmt.Fprintln(os.Stderr, "server aborted")
	})

	// start listening for incoming connections
	server.Serve(ctx, nil)
}

func trapSignals(onExit, onAbort func()) {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc)
	go func() {
		for s := range sigc {
			ok, graceful := isExitSignal(s)
			if !ok {
				continue
			}
			if !graceful {
				fmt.Fprintf(os.Stderr, "received signal: %v: aborting\n", s)
				if onAbort != nil {
					onAbort()
				}
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "received signal: %v: shutting down\n", s)
			if onExit != nil {
				onExit()
			}
			os.Exit(0)
		}
	}()
}

// isExitSignal returns a flag indicating whether a signal is SIGKILL, SIGHUP,
// SIGINT, SIGTERM, or SIGQUIT. The second return value is whether it is a
// graceful exit. This flag is true for SIGTERM, SIGHUP, SIGINT, and SIGQUIT.
func isExitSignal(s os.Signal) (bool, bool) {
	switch s {
	case syscall.SIGKILL:
		return true, false
	case syscall.SIGTERM,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGQUIT:
		return true, true
	default:
		return false, false
	}
}

func fileExists(filePath string) bool {
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		return true
	}
	return false
}
