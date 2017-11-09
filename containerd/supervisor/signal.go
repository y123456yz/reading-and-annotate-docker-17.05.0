package supervisor

import (
	"os"
)

// SignalTask holds needed parameters to signal a container
type SignalTask struct {
	baseTask
	ID     string
	PID    string
	Signal os.Signal
}

//停止容器 kill
func (s *Supervisor) signal(t *SignalTask) error {
	i, ok := s.containers[t.ID]
	if !ok {
		return ErrContainerNotFound
	}
	//获取容器中所有的process实例
	processes, err := i.container.Processes()
	if err != nil {
		return err
	}

	//最后遍历processes，找到t.PID对应的process，调用return p.Signal(t.Signal)
	for _, p := range processes {
		if p.ID() == t.PID {
			return p.Signal(t.Signal) //KILL
		}
	}
	return ErrProcessNotFound
}
