# Container Storage Providers
The table below lists the available Container Storage Providers (`csp`) -
packages that can be built as stand-alone CLI programs capable of serving 
the CSI services via a TCP port or UNIX socket, similar to `csd`. 
Additionally, the `csp` packages can **also** be built as Go plug-ins that 
the `csd` program can load, extending its storage provider support 
dynamically, at runtime.

| Name | Description |
|------|-------------|
| [`moc`](./moc) | Mock implementations of the CSI services used for development and testing |
| [`ebs`](./ebs) | Amazon Elastic Block Service (EBS) |