# Container Storage Daemon
The Container Storage Daemon (`csd`) is a CLI tool that serves 
the CSI services -- Controller, Identity, and Node -- on a TCP 
port or UNIX socket. The storage platform support for `csd` is 
provided via Go plug-ins, loadable at runtime.

```bash
$ CSI_PLUGINS=mock.so CSI_ENDPOINT=127.0.0.1:8080 csd mock
2017/06/27 14:59:22 loaded plug-in: mock.so
2017/06/27 14:59:22 registered endpoint: mock
2017/06/27 14:59:22 mock.Serve
```