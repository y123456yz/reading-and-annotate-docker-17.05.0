package specs

import oci "github.com/opencontainers/runtime-spec/specs-go"

type (
	// ProcessSpec aliases the platform process specs
	ProcessSpec oci.Process
	// Spec aliases the platform oci spec
	/////var/run/docker/libcontainerd/$containerID/config.json 中的内容序列化存入该结构，见 (c *container) readSpec()
	Spec oci.Spec
	// Rlimit aliases the platform resource limit
	Rlimit oci.Rlimit
)
