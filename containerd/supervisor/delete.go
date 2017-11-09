package supervisor

import (
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/runtime"
)

// DeleteTask holds needed parameters to remove a container
type DeleteTask struct {
	baseTask
	ID      string
	Status  uint32
	PID     string
	NoEvent bool
	Process runtime.Process
}

//容器中某个进程退出调用 (s *Supervisor) execExit,如果是init进程退出调用  (s *Supervisor) delete
//若为退出的是init进程，则创建一个ne := &DeleteTask{}，再调用s.delete(ne)进行处理
func (s *Supervisor) delete(t *DeleteTask) error {
	//调用i, ok := s.containers[t.ID]获取容器实例，再调用s.deleteContainer(i.container)
	if i, ok := s.containers[t.ID]; ok {
		start := time.Now()
		if err := s.deleteContainer(i.container); err != nil {
			logrus.WithField("error", err).Error("containerd: deleting container")
		}
		if t.Process != nil {
			t.Process.Wait()
		}
		if !t.NoEvent {
			execMap := s.getDeleteExecSyncMap(t.ID)
			go func() {
				// Wait for all exec processe events to be sent (we seem
				// to sometimes receive them after the init event)
				for _, ch := range execMap {
					<-ch
				}
				s.notifySubscribers(Event{
					Type:      StateExit,
					Timestamp: time.Now(),
					ID:        t.ID,
					Status:    t.Status,
					PID:       t.PID,
				})
			}()
		}
		ContainersCounter.Dec(1)
		ContainerDeleteTimer.UpdateSince(start)
	}
	return nil
}

//利用exec.Command直接调用调用命令行`docker-runc delete contain-id。
//删除目录/var/run/docker/libcontainerd/containerd/container-id，
func (s *Supervisor) deleteContainer(container runtime.Container) error {
	//把ID容器从 s.containers hash中移除
	delete(s.containers, container.ID())

	//利用exec.Command直接调用调用命令行`docker-runc delete contain-id。
	//删除目录/var/run/docker/libcontainerd/containerd/container-id，
	// (c *container) Delete()
	return container.Delete()
}
