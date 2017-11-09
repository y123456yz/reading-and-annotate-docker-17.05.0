package supervisor

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/runtime"
)

const (
	defaultBufferSize = 2048 // size of queue in eventloop
)

// New returns an initialized Process supervisor.
//containerd\main.go中的daemon函数执行  创建Supervisor对象，管理containerd进程。   supervisor.go中的New函数
//supervisor.New(stateDir, context.String("runtime"), context.String("shim"), context.StringSlice("runtime-args"), context.Duration("start-timeout"), context.Int("retain-count"))
func New(stateDir string, runtimeName, shimName string, runtimeArgs []string, timeout time.Duration, retainCount int) (*Supervisor, error) {
	startTasks := make(chan *startTask, 10)
	//检查机器信息，返回cpu数量，内存数量。
	machine, err := CollectMachineInformation()
	if err != nil {
		return nil, err
	}

	//epoll create  监视器
	// 创建epoll epoll_wait等待时间发生
	monitor, err := NewMonitor()
	if err != nil {
		return nil, err
	}

	//创建Supervisor对象
	s := &Supervisor{
		stateDir:          stateDir,
		containers:        make(map[string]*containerInfo),
		startTasks:        startTasks,
		machine:           machine,
		subscribers:       make(map[chan Event]struct{}),
		tasks:             make(chan Task, defaultBufferSize),
		monitor:           monitor,
		runtime:           runtimeName,
		runtimeArgs:       runtimeArgs,
		shim:              shimName,
		timeout:           timeout,
		containerExecSync: make(map[string]map[string]chan struct{}),
	}
	if err := setupEventLog(s, retainCount); err != nil {
		return nil, err
	}
	go s.exitHandler()
	go s.oomHandler()

	//s.restore()加载之前已经存在的容器
	if err := s.restore(); err != nil {
		return nil, err
	}
	return s, nil
}

//Supervisor中的containers成员包含该结构，
type containerInfo struct { //赋值见(s *Supervisor) start  create.go
	//赋值为 container.go 中的type container struct {}
	container runtime.Container
}

func setupEventLog(s *Supervisor, retainCount int) error {
	if err := readEventLog(s); err != nil {
		return err
	}
	logrus.WithField("count", len(s.eventLog)).Debug("containerd: read past events")
	events := s.Events(time.Time{}, false, "")
	return eventLogger(s, filepath.Join(s.stateDir, "events.log"), events, retainCount)
}

func eventLogger(s *Supervisor, path string, events chan Event, retainCount int) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	go func() {
		var (
			count = len(s.eventLog)
			enc   = json.NewEncoder(f)
		)
		for e := range events {
			// if we have a specified retain count make sure the truncate the event
			// log if it grows past the specified number of events to keep.
			if retainCount > 0 {
				if count > retainCount {
					logrus.Debug("truncating event log")
					// close the log file
					if f != nil {
						f.Close()
					}
					slice := retainCount - 1
					l := len(s.eventLog)
					if slice >= l {
						slice = l
					}
					s.eventLock.Lock()
					s.eventLog = s.eventLog[len(s.eventLog)-slice:]
					s.eventLock.Unlock()
					if f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|os.O_TRUNC, 0755); err != nil {
						logrus.WithField("error", err).Error("containerd: open event to journal")
						continue
					}
					enc = json.NewEncoder(f)
					count = 0
					for _, le := range s.eventLog {
						if err := enc.Encode(le); err != nil {
							logrus.WithField("error", err).Error("containerd: write event to journal")
						}
					}
				}
			}
			s.eventLock.Lock()
			s.eventLog = append(s.eventLog, e)
			s.eventLock.Unlock()
			count++
			if err := enc.Encode(e); err != nil {
				logrus.WithField("error", err).Error("containerd: write event to journal")
			}
		}
	}()
	return nil
}

/*
{"id":"63b247fa2c3c782ceb5b3aaafe3b7104aac4aa222bfec62ade9ebe6bab98664d","type":"start-container","timestamp":"2017-11-08T14:22:42.825555736+08:00"}
{"id":"63b247fa2c3c782ceb5b3aaafe3b7104aac4aa222bfec62ade9ebe6bab98664d","type":"exit","timestamp":"2017-11-08T14:23:52.246504551+08:00","pid":"init"}
{"id":"c0efd5284e6e0539ff77d76f64fd1f117c37bc2fb4245f5576009d21a8f43d7c","type":"start-container","timestamp":"2017-11-08T15:01:35.126886477+08:00"}
{"id":"c0efd5284e6e0539ff77d76f64fd1f117c37bc2fb4245f5576009d21a8f43d7c","type":"exit","timestamp":"2017-11-08T15:09:40.003760182+08:00","pid":"init"}
{"id":"8be0e38f7ed49b193103483181c8f1218bdb9e6a8385406b1422d08303d2ab0a","type":"start-container","timestamp":"2017-11-08T15:09:45.508301624+08:00"}
{"id":"8be0e38f7ed49b193103483181c8f1218bdb9e6a8385406b1422d08303d2ab0a","type":"exit","timestamp":"2017-11-08T17:02:53.180061942+08:00","pid":"init","status":130}
*/
func readEventLog(s *Supervisor) error {
	f, err := os.Open(filepath.Join(s.stateDir, "events.log"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var e eventV1
		if err := dec.Decode(&e); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		// We need to take care of -1 Status for backward compatibility
		ev := e.Event
		ev.Status = uint32(e.Status)
		if ev.Status > runtime.UnknownStatus {
			ev.Status = runtime.UnknownStatus
		}
		s.eventLog = append(s.eventLog, ev)
	}
	return nil
}

// Supervisor represents a container supervisor
/*
containerd 最重要的就是Supervisot中的两个go chan:Task 和startTask。还有三个重要的go协程。
1. api server协程，主要负责将Task放入Task chan
2. supervisor协程，主要将Task从Task chan中取出放入startTask chan
3. spuervisor worker协程（十个）主要负责从startTask chan中取出startTask，做相应的操作。
*/
//apiServer中包含该结构，supervisor源头在apiServer
type Supervisor struct { //Supervisor.go 中的New中构造该类
	// stateDir is the directory on the system to store container runtime state information.
	//默认/var/run/docker/libcontainerd/containerd
	stateDir string
	// name of the OCI compatible runtime used to execute containers
	////docker-runc
	runtime     string
	runtimeArgs []string
	////docker-containerd-shim
	shim        string
	//container都存入该hash中，赋值见create.go中的 (s *Supervisor) start
	//  (s *Supervisor) start 注册新增加的容器  deleteContainer 中移除
	containers  map[string]*containerInfo
	//supervisor.go中的New函数赋值，10个task, task 生效使用见 (w *worker) Start
	//handleTask->(s *Supervisor) start(create.go)中放入startTasks
	/*
	例如 CreateContainer 的时候会调用SendTask把StartTask放入 Supervisor.tasks 中，然后
	 supervisor.go中的(s *Supervisor) start 会从tasks中取出对应的task，然后调用(s *Supervisor) handleTask 处理这些task
	 然后handleTask->(s *Supervisor) start(create.go)中放入 startTasks chan
	 然后触发执行 create.go 中的(s *Supervisor) start
	*/
	startTasks  chan *startTask
	// we need a lock around the subscribers map only because additions and deletions from
	// the map are via the API so we cannot really control the concurrency
	subscriberLock sync.RWMutex
	subscribers    map[chan Event]struct{}
	//CollectMachineInformation 中获取
	machine        Machine
	/*
	containerd 最重要的就是Supervisot中的两个go chan:Task 和startTask。还有三个重要的go协程。
	1. api server协程，主要负责将Task放入Task chan
	2. supervisor协程，主要将Task从Task chan中取出放入startTask chan
	3. spuervisor worker协程（十个）主要负责从startTask chan中取出startTask，做相应的操作。

	例如 CreateContainer 的时候会调用SendTask把StartTask放入 Supervisor.tasks 中，然后
	 supervisor.go中的(s *Supervisor) start 会从tasks中取出对应的task，然后调用(s *Supervisor) handleTask 处理这些task
	 然后handleTask->(s *Supervisor) start(create.go)中放入 startTasks chan
	 然后触发执行 crate.go 中的(s *Supervisor) start
	*/
	tasks          chan Task  //见 SendTask 中放入task到tasks，
	//NewMonitor 返回值
	monitor        *Monitor
	//readEventLog 中赋值
	eventLog       []Event
	eventLock      sync.Mutex
	//默认--start-timeout 2m
	timeout        time.Duration
	// This is used to ensure that exec process death events are sent
	// before the init process death
	containerExecSyncLock sync.Mutex
	containerExecSync     map[string]map[string]chan struct{}
}

// Stop closes all startTasks and sends a SIGTERM to each container's pid1 then waits for they to
// terminate.  After it has handled all the SIGCHILD events it will close the signals chan
// and exit.  Stop is a non-blocking call and will return after the containers have been signaled
func (s *Supervisor) Stop() {
	// Close the startTasks channel so that no new containers get started
	close(s.startTasks)
}

// Close closes any open files in the supervisor but expects that Stop has been
// callsed so that no more containers are started.
func (s *Supervisor) Close() error {
	return nil
}

// Event represents a container event
type Event struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	PID       string    `json:"pid,omitempty"`
	Status    uint32    `json:"status,omitempty"`
}

type eventV1 struct {
	Event
	Status int `json:"status,omitempty"`
}

// Events returns an event channel that external consumers can use to receive updates
// on container events
func (s *Supervisor) Events(from time.Time, storedOnly bool, id string) chan Event {
	c := make(chan Event, defaultBufferSize)
	if storedOnly {
		defer s.Unsubscribe(c)
	}
	s.subscriberLock.Lock()
	defer s.subscriberLock.Unlock()
	if !from.IsZero() {
		// replay old event
		s.eventLock.Lock()
		past := s.eventLog[:]
		s.eventLock.Unlock()
		for _, e := range past {
			if e.Timestamp.After(from) {
				if id == "" || e.ID == id {
					c <- e
				}
			}
		}
	}
	if storedOnly {
		close(c)
	} else {
		EventSubscriberCounter.Inc(1)
		s.subscribers[c] = struct{}{}
	}
	return c
}

// Unsubscribe removes the provided channel from receiving any more events
func (s *Supervisor) Unsubscribe(sub chan Event) {
	s.subscriberLock.Lock()
	defer s.subscriberLock.Unlock()
	if _, ok := s.subscribers[sub]; ok {
		delete(s.subscribers, sub)
		close(sub)
		EventSubscriberCounter.Dec(1)
	}
}

// notifySubscribers will send the provided event to the external subscribers
// of the events channel
func (s *Supervisor) notifySubscribers(e Event) {
	s.subscriberLock.RLock()
	defer s.subscriberLock.RUnlock()
	for sub := range s.subscribers {
		// do a non-blocking send for the channel
		select {
		case sub <- e:
		default:
			logrus.WithField("event", e.Type).Warn("containerd: event not sent to subscriber")
		}
	}
}

// Start is a non-blocking call that runs the supervisor for monitoring contianer processes and
// executing new containers.
//
// This event loop is the only thing that is allowed to modify state of containers and processes
// therefore it is save to do operations in the handlers that modify state of the system or
// state of the Supervisor
//注意create.go和supervisor.go中的(s *Supervisor) start 和 container.go中的(c *container) Start 的区别
func (s *Supervisor) Start() error {
	logrus.WithFields(logrus.Fields{
		"stateDir":    s.stateDir,
		"runtime":     s.runtime,
		"runtimeArgs": s.runtimeArgs,
		"memory":      s.machine.Memory,
		"cpus":        s.machine.Cpus,
	}).Debug("containerd: supervisor running")
	go func() { //启动一个goroutine，再for i := range s.tasks，调用s.handlerTask(i)
		for i := range s.tasks {
			s.handleTask(i)
		}
	}()
	return nil
}

// Machine returns the machine information for which the
// supervisor is executing on.
func (s *Supervisor) Machine() Machine {
	return s.machine
}

// SendTask sends the provided event the the supervisors main event loop
//SendTask会将Task放入Task chan  例如CreateContainer 的时候会调用SendTask把StartTask放入tasks中，然后由 handleTask 进行调度处理
func (s *Supervisor) SendTask(evt Task) { //例如创建容器的时候会调用 CreateContainer->SendTask
	TasksCounter.Inc(1)
	s.tasks <- evt
}

/*
在启动daemon的时候，启动过一个exitHandler的goroutine，该函数主要的作用就是从s.monitor.exits这个runtime.Process类型的channel中获取退出的process。
对于每个退出的process，创建 e := &ExitTask{Process: p,}，最后s.SendTask(e)，最终经过 handleTask 的调度，最终会在exit()函数进行处理
*/
func (s *Supervisor) exitHandler() {
	//processEvent
	for p := range s.monitor.Exits() {
		e := &ExitTask{
			Process: p,
		}
		s.SendTask(e)
	}
}

func (s *Supervisor) oomHandler() {
	for id := range s.monitor.OOMs() {
		e := &OOMTask{
			ID: id,
		}
		s.SendTask(e)
	}
}

func (s *Supervisor) monitorProcess(p runtime.Process) error {
	return s.monitor.Monitor(p)
}

//加载之前已经存在的容器
func (s *Supervisor) restore() error {
	dirs, err := ioutil.ReadDir(s.stateDir)
	if err != nil {
		return err
	}

	//遍历目录s.stateDir（其实就是/var/run/docker/libcontainerd/containerd）
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		//调用id := d.Name()获取容器id
		id := d.Name()
		//load的作用就是加载s.stateDir/$containerid/state.json获取容器实例  之后，再遍历s.stateDir/id/下的pid 文件，加载容器中的process。
		container, err := runtime.Load(s.stateDir, id, s.shim, s.timeout)
		if err != nil {
			logrus.WithFields(logrus.Fields{"error": err, "id": id}).Warnf("containerd: failed to load container,removing state directory.")
			os.RemoveAll(filepath.Join(s.stateDir, id))
			continue
		}

		//加载容器中的process存入processes数组
		processes, err := container.Processes()
		if err != nil {
			return err
		}

		ContainersCounter.Inc(1)
		s.containers[id] = &containerInfo{
			container: container,
		}
		if err := s.monitor.MonitorOOM(container); err != nil && err != runtime.ErrContainerExited {
			logrus.WithField("error", err).Error("containerd: notify OOM events")
		}

		s.newExecSyncMap(container.ID())

		logrus.WithField("id", id).Debug("containerd: container restored")
		var exitedProcesses []runtime.Process
		for _, p := range processes {
			//如果process的状态为running，则调用s.monitorProcess(p)对其进行监控，并对其中不在运行的process进行处理。
			if p.State() == runtime.Running {
				if err := s.monitorProcess(p); err != nil {
					return err
				}
			} else {
				exitedProcesses = append(exitedProcesses, p)
			}
			if p.ID() != runtime.InitProcessID {
				s.newExecSyncChannel(container.ID(), p.ID())
			}
		}
		if len(exitedProcesses) > 0 { //对不处于running的process进行处理
			// sort processes so that init is fired last because that is how the kernel sends the
			// exit events
			sortProcesses(exitedProcesses)
			for _, p := range exitedProcesses {
				e := &ExitTask{
					Process: p,
				}
				s.SendTask(e)
			}
		}
	}
	return nil
}

//函数根据i的类型，调用相应的处理函数进行处理。例如，i.(type)为*StartTask时，则调用s.start(t)，若i.(type)为*DeleteTask时，则调用s.delete(t)。
func (s *Supervisor) handleTask(i Task) {
	var err error
	switch t := i.(type) {
	case *AddProcessTask:
		err = s.addProcess(t)
	case *CreateCheckpointTask:
		err = s.createCheckpoint(t)
	case *DeleteCheckpointTask:
		err = s.deleteCheckpoint(t)
	case *StartTask:
		err = s.start(t)
	case *DeleteTask:
		err = s.delete(t)
	case *ExitTask:
		err = s.exit(t)
	case *GetContainersTask:
		err = s.getContainers(t)
	case *SignalTask:
		err = s.signal(t)
	case *StatsTask:
		err = s.stats(t)
	case *UpdateTask:
		err = s.updateContainer(t)
	case *UpdateProcessTask:
		err = s.updateProcess(t)
	case *OOMTask:
		err = s.oom(t)
	default:
		err = ErrUnknownTask
	}
	if err != errDeferredResponse {
		i.ErrorCh() <- err
		close(i.ErrorCh())
	}
}

func (s *Supervisor) newExecSyncMap(containerID string) {
	s.containerExecSyncLock.Lock()
	s.containerExecSync[containerID] = make(map[string]chan struct{})
	s.containerExecSyncLock.Unlock()
}

func (s *Supervisor) newExecSyncChannel(containerID, pid string) {
	s.containerExecSyncLock.Lock()
	s.containerExecSync[containerID][pid] = make(chan struct{})
	s.containerExecSyncLock.Unlock()
}

func (s *Supervisor) deleteExecSyncChannel(containerID, pid string) {
	s.containerExecSyncLock.Lock()
	delete(s.containerExecSync[containerID], pid)
	s.containerExecSyncLock.Unlock()
}

func (s *Supervisor) getExecSyncChannel(containerID, pid string) chan struct{} {
	s.containerExecSyncLock.Lock()
	ch := s.containerExecSync[containerID][pid]
	s.containerExecSyncLock.Unlock()
	return ch
}

func (s *Supervisor) getDeleteExecSyncMap(containerID string) map[string]chan struct{} {
	s.containerExecSyncLock.Lock()
	chs := s.containerExecSync[containerID]
	delete(s.containerExecSync, containerID)
	s.containerExecSyncLock.Unlock()
	return chs
}
