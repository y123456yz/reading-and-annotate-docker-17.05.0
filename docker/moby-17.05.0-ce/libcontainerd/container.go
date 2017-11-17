package libcontainerd

const (
	// InitFriendlyName is the name given in the lookup map of processes
	// for the first process started in a container.
	InitFriendlyName = "init"
	/*
	(clnt *client) Create (libcontainerd\container.go) 创建改文件并写入对应spec内容，
	然后在containerd中的 runtime\container.go中的 (c *container) Start->readSpec 中加载该config.json
	*/
	configFilename   = "config.json"  ///run/docker/libcontainerd/$containerID/config.json
)

//libcontainerd\container_unix.go 中的type container struct 结构包含该结构
type containerCommon struct { //(clnt *client) newContainer 中构造使用
	process
	processes map[string]*process
}
