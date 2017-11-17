package libnetwork

import "github.com/docker/docker/pkg/reexec"

//SetExternalKey 中构造使用
type setKeyData struct {
	ContainerID string
	Key         string
}

func init() {
	reexec.Register("libnetwork-setkey", processSetKeyReexec)
}
