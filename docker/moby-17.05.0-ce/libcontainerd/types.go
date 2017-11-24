package libcontainerd

import (
	"io"

	containerd "github.com/docker/containerd/api/grpc/types"
	"github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/net/context"
)

// State constants used in state change reporting.
/*
[root@newnamespace containerd]# cat events.log
{"id":"27b9f372d79e0e140340e25a1c55363e6f13d52f88e16b64d20888118fcd3f5f","type":"start-container","timestamp":"2017-11-24T19:44:36.600766798+08:00"}
{"id":"27b9f372d79e0e140340e25a1c55363e6f13d52f88e16b64d20888118fcd3f5f","type":"exit","timestamp":"2017-11-24T19:44:55.409894524+08:00","pid":"init"}
*/
const ( //参考 startEventsMonitor
	//注意 ContainerEventType(daemon.EventsService.Log通知给docker events客户端) 和 StateStart(通过 clnt.backend.StateChanged 设置状态，被记录到events.log文件)等的区别
	StateStart       = "start-container"
	StatePause       = "pause"
	StateResume      = "resume"
	StateExit        = "exit"
	StateRestore     = "restore"
	StateExitProcess = "exit-process"
	StateOOM         = "oom" // fake state
)

// CommonStateInfo contains the state info common to all platforms.
type CommonStateInfo struct { // FIXME: event?
	State     string
	Pid       uint32
	ExitCode  uint32
	ProcessID string
}

// Backend defines callbacks that the client of the library needs to implement.
//Daemon 结构实现该方法，
type Backend interface { //(r *remote) Client(b Backend) 赋值
	// (daemon *Daemon) StateChanged    clnt.backend.StateChanged 调用这里，例如dockerd 退出，对应(clnt *client) setExited
	StateChanged(containerID string, state StateInfo) error
}

// Client provides access to containerd features.
//libcontainerd->client_unix.go 中的 client 中实现这些接口
type Client interface {
	GetServerVersion(ctx context.Context) (*ServerVersion, error)
	Create(containerID string, checkpoint string, checkpointDir string, spec specs.Spec, attachStdio StdioCallback, options ...CreateOption) error
	Signal(containerID string, sig int) error
	SignalProcess(containerID string, processFriendlyName string, sig int) error
	AddProcess(ctx context.Context, containerID, processFriendlyName string, process Process, attachStdio StdioCallback) (int, error)
	Resize(containerID, processFriendlyName string, width, height int) error
	Pause(containerID string) error
	Resume(containerID string) error
	Restore(containerID string, attachStdio StdioCallback, options ...CreateOption) error
	Stats(containerID string) (*Stats, error)
	GetPidsForContainer(containerID string) ([]int, error)
	Summary(containerID string) ([]Summary, error)
	UpdateResources(containerID string, resources Resources) error
	CreateCheckpoint(containerID string, checkpointID string, checkpointDir string, exit bool) error
	DeleteCheckpoint(containerID string, checkpointID string, checkpointDir string) error
	ListCheckpoints(containerID string, checkpointDir string) (*Checkpoints, error)
}

// CreateOption allows to configure parameters of container creation.
type CreateOption interface {
	Apply(interface{}) error
}

// StdioCallback is called to connect a container or process stdio.
type StdioCallback func(IOPipe) error

// IOPipe contains the stdio streams.
type IOPipe struct {
	Stdin    io.WriteCloser
	Stdout   io.ReadCloser
	Stderr   io.ReadCloser
	Terminal bool // Whether stderr is connected on Windows
}

// ServerVersion contains version information as retrieved from the
// server
type ServerVersion struct {
	containerd.GetServerVersionResponse
}
