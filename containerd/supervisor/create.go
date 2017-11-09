package supervisor

import (
	"path/filepath"
	"time"

	"github.com/docker/containerd/runtime"
	"golang.org/x/net/context"
)

// StartTask holds needed parameters to create a new container
type StartTask struct { //例如创建容器，对应的rpc回调(s *apiServer) CreateContainer 中会构造该结构
	baseTask
	ID            string
	BundlePath    string
	Stdout        string
	Stderr        string
	Stdin         string
	StartResponse chan StartResponse
	Labels        []string
	NoPivotRoot   bool
	Checkpoint    *runtime.Checkpoint
	CheckpointDir string
	Runtime       string
	RuntimeArgs   []string
	Ctx           context.Context
}

//注意create.go( (s *Supervisor) handleTask 中执行)和supervisor.go(main.go中的 daemon 中执行)中的(s *Supervisor) start 和 container.go中的(c *container) Start 的区别
func (s *Supervisor) start(t *StartTask) error { //handleTask->(s *Supervisor) start(create.go)
	start := time.Now()
	rt := s.runtime
	rtArgs := s.runtimeArgs
	if t.Runtime != "" {
		rt = t.Runtime
		rtArgs = t.RuntimeArgs
	}

	//创建/var/run/docker/libcontainerd/containerd/state.json文件并序列化写入相关内容
	container, err := runtime.New(runtime.ContainerOpts{
		Root:        s.stateDir,
		ID:          t.ID,
		Bundle:      t.BundlePath,
		Runtime:     rt,
		RuntimeArgs: rtArgs,
		Shim:        s.shim,
		Labels:      t.Labels,
		NoPivotRoot: t.NoPivotRoot,
		Timeout:     s.timeout,
	})
	if err != nil {
		return err
	}

	//container都存入该HASH中  注册新增加的容器
	s.containers[t.ID] = &containerInfo{
		container: container,
	}
	ContainersCounter.Inc(1)
	//根据新获得的container实例和t的内容，填充获得startTask，再调用s.startTask <- task 交由worker处理
	task := &startTask{
		Err:           t.ErrorCh(),
		Container:     container,
		StartResponse: t.StartResponse,
		Stdin:         t.Stdin,
		Stdout:        t.Stdout,
		Stderr:        t.Stderr,
		Ctx:           t.Ctx,
	}
	if t.Checkpoint != nil {
		task.CheckpointPath = filepath.Join(t.CheckpointDir, t.Checkpoint.Name)
	}

	//Maxx  构造一个新的startTask，并传递给startTasks channel
	// then go to \supervisor\worker.go
	// Supervisor.worker的Start方法中，读取startTasks channel，并调用runtime.Container接口的Start方法
	s.startTasks <- task //upervisor将Task转化后放入了startTask chan   触发执行supervisor\worker.go 中的(w *worker) Start()
	ContainerCreateTimer.UpdateSince(start)
	return errDeferredResponse
}
