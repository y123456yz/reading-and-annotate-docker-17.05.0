package supervisor

import (
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/runtime"
	"golang.org/x/net/context"
)

// Worker interface
type Worker interface {
	Start()
}

////supervisor.go中的New函数中使用，make 10个startTask
//赋值见 create.go中的(s *Supervisor) start
type startTask struct {
	//赋值为 container 结构
	Container      runtime.Container
	CheckpointPath string
	Stdin          string
	Stdout         string
	Stderr         string
	Err            chan error
	StartResponse  chan StartResponse
	Ctx            context.Context
}

// NewWorker return a new initialized worker
//supervisor.NewWorker(sv, wg)
func NewWorker(s *Supervisor, wg *sync.WaitGroup) Worker {
	return &worker{
		s:  s,
		wg: wg,
	}
}

type worker struct { //初始化结构赋值见NewWorker
	wg *sync.WaitGroup
	s  *Supervisor
}

// Start runs a loop in charge of starting new containers
//由(s *Supervisor) start create.go 触发执行到这里
func (w *worker) Start() {
	defer w.wg.Done()
	for t := range w.s.startTasks {
		started := time.Now()
		//执行runtime\container.go中的(c *container) Start 函数  //启动容器，执行docker-containerd-shim
		//启动容器，NewStdio()仅仅只是将参数封装到一个统一的Stdio结构中
		process, err := t.Container.Start(t.Ctx, t.CheckpointPath, runtime.NewStdio(t.Stdin, t.Stdout, t.Stderr))
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error": err,
				"id":    t.Container.ID(),
			}).Error("containerd: start container")
			t.Err <- err
			evt := &DeleteTask{
				ID:      t.Container.ID(),
				NoEvent: true,
				Process: process,
			}
			w.s.SendTask(evt)
			continue
		}

		//监控容器的OOM事件   monitorProcess  MonitorOOM 结合 NewMonitor 阅读
		if err := w.s.monitor.MonitorOOM(t.Container); err != nil && err != runtime.ErrContainerExited {
			if process.State() != runtime.Stopped {
				logrus.WithField("error", err).Error("containerd: notify OOM events")
			}
		}

		//监控进程状态 结合NewMonitor 阅读
		if err := w.s.monitorProcess(process); err != nil {
			logrus.WithField("error", err).Error("containerd: add process to monitor")
			t.Err <- err
			evt := &DeleteTask{
				ID:      t.Container.ID(),
				NoEvent: true,
				Process: process,
			}
			w.s.SendTask(evt)
			continue
		}
		// only call process start if we aren't restoring from a checkpoint
		// if we have restored from a checkpoint then the process is already started
		if t.CheckpointPath == "" {
			//runtime\process.go  中的
			if err := process.Start(); err != nil {
				logrus.WithField("error", err).Error("containerd: start init process")
				t.Err <- err
				evt := &DeleteTask{
					ID:      t.Container.ID(),
					NoEvent: true,
					Process: process,
				}
				w.s.SendTask(evt)
				continue
			}
		}
		ContainerStartTimer.UpdateSince(started)
		w.s.newExecSyncMap(t.Container.ID())
		t.Err <- nil

		//调用t.StartResponse <- StartResponse{Container: t.Container}返回创建成功的容器实例
		t.StartResponse <- StartResponse {
			Container: t.Container,
		}

		//进行消息通知
		w.s.notifySubscribers(Event{
			Timestamp: time.Now(),
			ID:        t.Container.ID(),
			Type:      StateStart,
		})
	}
}
