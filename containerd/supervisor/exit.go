package supervisor

import (
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/runtime"
)

// ExitTask holds needed parameters to execute the exit task
//exitHandler 中构造使用
type ExitTask struct {
	baseTask
	Process runtime.Process
}

func (s *Supervisor) exit(t *ExitTask) error {
	start := time.Now()
	proc := t.Process
	//获取进程的退出码
	status, err := proc.ExitStatus()
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error":     err,
			"pid":       proc.ID(),
			"id":        proc.Container().ID(),
			"systemPid": proc.SystemPid(),
		}).Error("containerd: get exit status")
	}
	logrus.WithFields(logrus.Fields{
		"pid":       proc.ID(),
		"status":    status,
		"id":        proc.Container().ID(),
		"systemPid": proc.SystemPid(),
	}).Debug("containerd: process exited")

	// if the process is the the init process of the container then
	// fire a separate event for this process
	//如果proc.ID()不是runtime.InitProcessID，则说明只是一个exec的进程退出，则创建一个ne := &ExecExitTask{}，再调用s.execExit()进行处理
	if proc.ID() != runtime.InitProcessID { //容器中的某个进程退出了
		ne := &ExecExitTask{
			ID:      proc.Container().ID(),
			PID:     proc.ID(),
			Status:  status,
			Process: proc,
		}
		s.execExit(ne)
		return nil
	}

	//若为退出的是init进程，则创建一个ne := &DeleteTask{}，再调用s.delete(ne)进行处理
	container := proc.Container()
	ne := &DeleteTask{
		ID:      container.ID(),
		Status:  status,
		PID:     proc.ID(),
		Process: proc,
	}
	s.delete(ne)

	ExitProcessTimer.UpdateSince(start)

	return nil
}

// ExecExitTask holds needed parameters to execute the exec exit task
type ExecExitTask struct {
	baseTask
	ID      string
	PID     string
	Status  uint32
	Process runtime.Process
}

//容器中某个进程退出调用 (s *Supervisor) execExit,如果是init进程退出调用  (s *Supervisor) delete
func (s *Supervisor) execExit(t *ExecExitTask) error {
	container := t.Process.Container()
	// exec process: we remove this process without notifying the main event loop
	if err := container.RemoveProcess(t.PID); err != nil {
		logrus.WithField("error", err).Error("containerd: find container for pid")
	}
	synCh := s.getExecSyncChannel(t.ID, t.PID)
	// If the exec spawned children which are still using its IO
	// waiting here will block until they die or close their IO
	// descriptors.
	// Hence, we use a go routine to avoid blocking all other operations
	go func() {
		t.Process.Wait()
		s.notifySubscribers(Event{
			Timestamp: time.Now(),
			ID:        t.ID,
			Type:      StateExit,
			PID:       t.PID,
			Status:    t.Status,
		})
		close(synCh)
	}()
	return nil
}
