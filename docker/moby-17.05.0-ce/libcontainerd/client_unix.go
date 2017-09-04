// +build linux solaris

package libcontainerd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Sirupsen/logrus"
	containerd "github.com/docker/containerd/api/grpc/types"
	"github.com/docker/docker/pkg/idtools"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/net/context"
)

func (clnt *client) prepareBundleDir(uid, gid int) (string, error) {
	root, err := filepath.Abs(clnt.remote.stateDir)
	if err != nil {
		return "", err
	}
	if uid == 0 && gid == 0 {
		return root, nil
	}
	p := string(filepath.Separator)
	for _, d := range strings.Split(root, string(filepath.Separator))[1:] {
		p = filepath.Join(p, d)
		fi, err := os.Stat(p)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if os.IsNotExist(err) || fi.Mode()&1 == 0 {
			p = fmt.Sprintf("%s.%d.%d", p, uid, gid)
			if err := idtools.MkdirAs(p, 0700, uid, gid); err != nil && !os.IsExist(err) {
				return "", err
			}
		}
	}
	return p, nil
}

//libcontainerd\client_unix.go中的Create函数
//start.go中的函数 containerStart 执行
func (clnt *client) Create(containerID string, checkpoint string, checkpointDir string, spec specs.Spec, attachStdio StdioCallback, options ...CreateOption) (err error) {
	clnt.lock(containerID)
	defer clnt.unlock(containerID)

	if _, err := clnt.getContainer(containerID); err == nil { //查看该id对应的容器是否已经启动
		return fmt.Errorf("Container %s is already active", containerID)
	}

	uid, gid, err := getRootIDs(specs.Spec(spec))
	if err != nil {
		return err
	}
	dir, err := clnt.prepareBundleDir(uid, gid)
	if err != nil {
		return err
	}

	//获取一个container实例
	container := clnt.newContainer(filepath.Join(dir, containerID), options...)
	if err := container.clean(); err != nil {
		return err
	}

	defer func() {
		if err != nil {
			container.clean()
			clnt.deleteContainer(containerID)
		}
	}()

	if err := idtools.MkdirAllAs(container.dir, 0700, uid, gid); err != nil && !os.IsExist(err) {
		return err
	}

	//在/var/lib/docker/container/containerID创建config.json文件
	f, err := os.Create(filepath.Join(container.dir, configFilename))
	if err != nil {
		return err
	}
	defer f.Close()
	//把spec配置序列化写入config.json文件
	if err := json.NewEncoder(f).Encode(spec); err != nil {
		return err
	}

	//启动container   libcontainerd\container_unix.go中的start函数
	return container.start(checkpoint, checkpointDir, attachStdio)
}

func (clnt *client) Signal(containerID string, sig int) error {
	clnt.lock(containerID)
	defer clnt.unlock(containerID)
	_, err := clnt.remote.apiClient.Signal(context.Background(), &containerd.SignalRequest{
		Id:     containerID,
		Pid:    InitFriendlyName,
		Signal: uint32(sig),
	})
	return err
}

func (clnt *client) newContainer(dir string, options ...CreateOption) *container {
	container := &container{
		containerCommon: containerCommon{
			process: process{
				dir: dir,
				processCommon: processCommon{
					containerID:  filepath.Base(dir),
					client:       clnt,
					friendlyName: InitFriendlyName,
				},
			},
			processes: make(map[string]*process),
		},
	}
	for _, option := range options {
		if err := option.Apply(container); err != nil {
			logrus.Errorf("libcontainerd: newContainer(): %v", err)
		}
	}
	return container
}

type exitNotifier struct {
	id     string
	client *client
	c      chan struct{}
	once   sync.Once
}

func (en *exitNotifier) close() {
	en.once.Do(func() {
		close(en.c)
		en.client.mapMutex.Lock()
		if en == en.client.exitNotifiers[en.id] {
			delete(en.client.exitNotifiers, en.id)
		}
		en.client.mapMutex.Unlock()
	})
}
func (en *exitNotifier) wait() <-chan struct{} {
	return en.c
}
