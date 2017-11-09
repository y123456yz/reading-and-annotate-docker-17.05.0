package supervisor

import (
	"sync"

	"github.com/docker/containerd/runtime"
)

// StartResponse is the response containing a started container
//StartTask 中包含该chan成员
type StartResponse struct {
	ExecPid   int
	Container runtime.Container
}

// Task executes an action returning an error chan with either nil or
// the error from executing the task
type Task interface {
	// ErrorCh returns a channel used to report and error from an async task
	ErrorCh() chan error
}

//StartTask DeleteTask ExitTask  SignalTask CreateCheckpointTask 中包含该chan成员， 具体的task种类可以参考 handleTask
//(s *apiServer) Signal 这些task构造来源在 apiServer 对应的rpc服务端处理接口中，例如SignalTask对应(s *apiServer) Signal中构造
// containerd的中的各种操作都是通过Task来进行的，因此对于容器的create, start, delete等等操作其实都是一个个的Task而已。
type baseTask struct { //各种task的处理见 handleTask
	//ErrorCh 中make 赋值
	errCh chan error
	mu    sync.Mutex
}

func (t *baseTask) ErrorCh() chan error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.errCh == nil {
		t.errCh = make(chan error, 1)
	}
	return t.errCh
}
