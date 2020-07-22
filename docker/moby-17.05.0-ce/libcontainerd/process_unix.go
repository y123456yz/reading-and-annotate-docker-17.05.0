// +build linux solaris

package libcontainerd

import (
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	containerd "github.com/docker/containerd/api/grpc/types"
	"github.com/tonistiigi/fifo"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
)

/*
root@fd-mesos-subordinate50.gz01:/var/run/docker/libcontainerd/246d243260dc647eb174421c05b88567a0f0d64f451cbc17fde53d24cc6af340$ ls
config.json  init-stdin  init-stdout
io相关的命名管道，用来和容器之间进行通信，比如这里的init-stdin文件用来向容器的stdin中写数据，init-stdout用来接收容器的stdout输出
上面只有init-stdin和init-stdout，没有init-stderr，那是因为我们创建容器的时候指定了-t参数，意思是让docker为容器创建一个tty（虚拟的），
在这种情况下，stdout和stderr将采用同样的通道，即容器中进程往stderr中输出数据时，会写到init-stdout中。

上面的config.json  init-stdin  init-stdout文件都准备好了之后，通过grpc的方式给containerd发送请求，通知containerd启动容器。

参考https://segmentfault.com/a/1190000010057763
对应这里对应的 (p *process) fifo 中创建
*/
var fdNames = map[int]string{
	unix.Stdin:  "stdin",
	unix.Stdout: "stdout",
	unix.Stderr: "stderr",
}

// process keeps the state for both main container process and exec process.
type process struct {
	processCommon

	// Platform specific fields are below here.
	dir string ///run/docker/libcontainerd/$containerID
}

func (p *process) openFifos(ctx context.Context, terminal bool) (pipe *IOPipe, err error) {
	if err := os.MkdirAll(p.dir, 0700); err != nil {
		return nil, err
	}

	io := &IOPipe{}

	io.Stdin, err = fifo.OpenFifo(ctx, p.fifo(unix.Stdin), unix.O_WRONLY|unix.O_CREAT|unix.O_NONBLOCK, 0700)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			io.Stdin.Close()
		}
	}()

	io.Stdout, err = fifo.OpenFifo(ctx, p.fifo(unix.Stdout), unix.O_RDONLY|unix.O_CREAT|unix.O_NONBLOCK, 0700)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			io.Stdout.Close()
		}
	}()

	if goruntime.GOOS == "solaris" || !terminal {
		// For Solaris terminal handling is done exclusively by the runtime therefore we make no distinction
		// in the processing for terminal and !terminal cases.
		io.Stderr, err = fifo.OpenFifo(ctx, p.fifo(unix.Stderr), unix.O_RDONLY|unix.O_CREAT|unix.O_NONBLOCK, 0700)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err != nil {
				io.Stderr.Close()
			}
		}()
	} else {
		io.Stderr = ioutil.NopCloser(emptyReader{})
	}

	return io, nil
}

func (p *process) sendCloseStdin() error {
	_, err := p.client.remote.apiClient.UpdateProcess(context.Background(), &containerd.UpdateProcessRequest{
		Id:         p.containerID,
		Pid:        p.friendlyName,
		CloseStdin: true,
	})
	if err != nil && (strings.Contains(err.Error(), "container not found") || strings.Contains(err.Error(), "process not found")) {
		return nil
	}
	return err
}

func (p *process) closeFifos(io *IOPipe) {
	io.Stdin.Close()
	io.Stdout.Close()
	io.Stderr.Close()
}

type emptyReader struct{}

func (r emptyReader) Read(b []byte) (int, error) {
	return 0, io.EOF
}

func (p *process) fifo(index int) string {
	return filepath.Join(p.dir, p.friendlyName+"-"+fdNames[index])
}
