package libcontainerd

import (
	"fmt"
	"sync"

	"github.com/docker/docker/pkg/locker"
)

// clientCommon contains the platform agnostic fields used in the client structure
type clientCommon struct {
	backend    Backend
	//启动起来的容器，起ID和对应的容器信息存在该map中，参考getContainer
	containers map[string]*container
	locker     *locker.Locker
	mapMutex   sync.RWMutex // protects read/write operations from containers map
}

func (clnt *client) lock(containerID string) {
	clnt.locker.Lock(containerID)
}

func (clnt *client) unlock(containerID string) {
	clnt.locker.Unlock(containerID)
}

// must hold a lock for cont.containerID
func (clnt *client) appendContainer(cont *container) {
	clnt.mapMutex.Lock()
	clnt.containers[cont.containerID] = cont
	clnt.mapMutex.Unlock()
}
func (clnt *client) deleteContainer(containerID string) {
	clnt.mapMutex.Lock()
	delete(clnt.containers, containerID)
	clnt.mapMutex.Unlock()
}

//查看containerID是否已经骑起来
func (clnt *client) getContainer(containerID string) (*container, error) {
	clnt.mapMutex.RLock()

	//启动起来的容器，起ID和对应的容器信息存在该map中
	container, ok := clnt.containers[containerID]
	defer clnt.mapMutex.RUnlock()
	if !ok {
		return nil, fmt.Errorf("invalid container: %s", containerID) // fixme: typed error
	}
	return container, nil
}
