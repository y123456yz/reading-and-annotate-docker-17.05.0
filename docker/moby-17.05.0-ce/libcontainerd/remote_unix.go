// +build linux solaris

package libcontainerd

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Sirupsen/logrus"
	containerd "github.com/docker/containerd/api/grpc/types"
	"github.com/docker/docker/pkg/locker"
	"github.com/docker/docker/pkg/system"
	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/timestamp"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/transport"
)

//默认文件在/run/docker/libcontainerd目录下面
const (
	maxConnectionRetryCount      = 3
	containerdHealthCheckTimeout = 3 * time.Second
	containerdShutdownTimeout    = 15 * time.Second
	containerdBinary             = "docker-containerd"
	containerdPidFilename        = "docker-containerd.pid"
	containerdSockFilename       = "docker-containerd.sock"
	containerdStateDir           = "containerd"
	//run/docker/libcontainerd/event.ts
	eventTimestampFilename       = "event.ts"
)

//dockerd和docker-containerd之间的rpc通信用  对应的服务端RPC为containerd程序中的 apiServer 中实现的相关接口
//参数赋值可以参考 getPlatformRemoteOptions   和remote_unix.go中的New，配合阅读
//client_linux.go中包含 remote 结构成员， (r *remote) Client 中remote被赋值个client.remote c
type remote struct { //func New （libcontainerd\remote_unix.go中的 New中构造该结构）
	//例如 (r *remote) Client->r.Lock()就会用到该锁
	sync.RWMutex
	//dockerd和docker-containerd之间的rpc client结构   对应的服务端RPC为containerd程序中的 apiServer, 例如dockerd create 容器对应的是 (s *apiServer) CreateContainer
	apiClient            containerd.APIClient
	//赋值见//赋值见runContainerdDaemon
	daemonPid            int  //"docker-containerd"进程的进程号
	//getLibcontainerdRoot   /run/docker/libcontainerd/
	stateDir             string
	rpcAddr              string //域套接字docker-containerd.sock
	//默认ture
	startDaemon          bool
	closeManually        bool
	//是否启用containerd的debug
	debugLog             bool
	//dockerd和docker-containerd之间的rpc链接
	rpcConn              *grpc.ClientConn
	//获取grpc的client结构，见(r *remote) Client
	clients              []*client
	//run/docker/libcontainerd/event.ts
	eventTsPath          string
	runtime              string
	runtimeArgs          []string
	//赋值见runContainerdDaemon，和 handleConnectionChange 配合阅读，等待containerd进程执行结束，及等待cmd.wait
	daemonWaitCh         chan struct{}
	/*
	docker 1.12增加了--live-restore的选项，去掉了docker 进程的依赖，就是说在节点，如果service docker stop或者docker服务进程异常退出，
	在原来的docker版本，那么所有开启的docker container都会挂掉，惨了，相当于很多个container就失效了；也造成了单机的docker是单点的；
	那么1.12来了，解决了这个实际运用的问题，就是dockerd服务怎样关闭，容器照样运行，服务恢复后，容器也可以再被服务抓到并可管理。
	参考 http://blog.sina.com.cn/s/blog_67fbe4650102x2po.html
	*/
	//(r *remote) Client 赋值给 client.liveRestore
	liveRestore          bool
	// --oom-score-adjust int                  Set the oom_score_adj for the daemon (default -500)
	oomScore             int
	restoreFromTimestamp *timestamp.Timestamp
}

// New creates a fresh instance of libcontainerd remote.
//  /var/run/docker路径 ，创建 containerd Remote，container相关处理启动grpc的client api，事件监控等
//runContainerdDaemon 在该函数中运行
//func (cli *DaemonCli) start(opts daemonOptions) (err error) 中调用libcontainerd.New()执行该函数
//用来构造和docker-containerd通信的相关remote类，并启动 docker-containerd 进程
func New(stateDir string, options ...RemoteOption) (_ Remote, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("Failed to connect to containerd. Please make sure containerd is installed in your PATH or you have specified the correct address. Got error: %v", err)
		}
	}()
	r := &remote{
		stateDir:    stateDir,  //getLibcontainerdRoot  // /run/docker/libcontainerd/libcontainerd
		daemonPid:   -1,
		eventTsPath: filepath.Join(stateDir, eventTimestampFilename),
	}

	//获取 Remote 配置信息
	for _, option := range options { //options来源 getPlatformRemoteOptions
		if err := option.Apply(r); err != nil {
			return nil, err
		}
	}

	if err := system.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}

	if r.rpcAddr == "" {
		r.rpcAddr = filepath.Join(stateDir, containerdSockFilename)
	}

	if r.startDaemon {
		//run container daemon
		if err := r.runContainerdDaemon(); err != nil {
			return nil, err
		}
	}

	// don't output the grpc reconnect logging
	grpclog.SetLogger(log.New(ioutil.Discard, "", log.LstdFlags))
	dialOpts := append([]grpc.DialOption{grpc.WithInsecure()},
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	)
	conn, err := grpc.Dial(r.rpcAddr, dialOpts...) //域套接字链接
	if err != nil {
		return nil, fmt.Errorf("error connecting to containerd: %v", err)
	}

	r.rpcConn = conn
	r.apiClient = containerd.NewAPIClient(conn)

	// Get the timestamp to restore from
	t := r.getLastEventTimestamp()
	//转换/run/docker/libcontainerd$ cat event.ts     2017-11-16T08:02:40.39566466Z 为timestamp
	tsp, err := ptypes.TimestampProto(t)
	if err != nil {
		logrus.Errorf("libcontainerd: failed to convert timestamp: %q", err)
	}
	r.restoreFromTimestamp = tsp

	//500ms一次进行保活检查
	go r.handleConnectionChange()

	if err := r.startEventsMonitor(); err != nil {
		return nil, err
	}

	return r, nil
}

//reloadLiveRestore 中 加载到 "live-restore" 执行
func (r *remote) UpdateOptions(options ...RemoteOption) error {
	for _, option := range options {
		if err := option.Apply(r); err != nil {
			return err
		}
	}
	return nil
}

//用于containerd和dockerd的保活，见dockerd的handleConnectionChange，和containerd的 containerd\main.go 中的 startServer
//500ms一次进行保活检查
func (r *remote) handleConnectionChange() {
	var transientFailureCount = 0

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	//返回 healthClient 结构
	healthClient := grpc_health_v1.NewHealthClient(r.rpcConn)

	for {
		<-ticker.C
		ctx, cancel := context.WithTimeout(context.Background(), containerdHealthCheckTimeout)
		_, err := healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
		cancel()
		if err == nil { //正常，则继续下一个定时保活
			continue
		}

		logrus.Debugf("libcontainerd: containerd health check returned error: %v", err)

		if r.daemonPid != -1 {
			if strings.Contains(err.Error(), "is closing") { //是我们主动stop 容器的
				// Well, we asked for it to stop, just return
				return
			}
			// all other errors are transient
			// Reset state to be notified of next failure
			transientFailureCount++
			//多次保护活失败，，说明docker-containerd异常了，则需要重启容器docker-containerd
			if transientFailureCount >= maxConnectionRetryCount {
				transientFailureCount = 0
				if system.IsProcessAlive(r.daemonPid) {
					system.KillProcess(r.daemonPid)
				}
				<-r.daemonWaitCh
				if err := r.runContainerdDaemon(); err != nil { //FIXME: Handle error
					logrus.Errorf("libcontainerd: error restarting containerd: %v", err)
				}
				continue
			}
		}
	}
}

//(cli *DaemonCli) start 中执行，当dockerd api server异常的时候才会走到这里
//dockerd api server异常，则需要Kill  docker-container进程
func (r *remote) Cleanup() {
	if r.daemonPid == -1 {
		return
	}
	r.closeManually = true
	r.rpcConn.Close()
	// Ask the daemon to quit
	syscall.Kill(r.daemonPid, syscall.SIGTERM)

	// Wait up to 15secs for it to stop
	for i := time.Duration(0); i < containerdShutdownTimeout; i += time.Second {
		if !system.IsProcessAlive(r.daemonPid) {
			break
		}
		time.Sleep(time.Second)
	}

	if system.IsProcessAlive(r.daemonPid) {
		logrus.Warnf("libcontainerd: containerd (%d) didn't stop within 15 secs, killing it\n", r.daemonPid)
		syscall.Kill(r.daemonPid, syscall.SIGKILL)
	}

	// cleanup some files
	os.Remove(filepath.Join(r.stateDir, containerdPidFilename))
	os.Remove(filepath.Join(r.stateDir, containerdSockFilename))
}

//NewDaemon 中调用执行  b对应Daemon
func (r *remote) Client(b Backend) (Client, error) {
	c := &client{
		clientCommon: clientCommon{
			backend:    b,
			containers: make(map[string]*container),
			locker:     locker.New(),
		},
		remote:        r,
		exitNotifiers: make(map[string]*exitNotifier),
		/*
		docker 1.12增加了--live-restore的选项，去掉了docker 进程的依赖，就是说在节点，如果service docker stop或者docker服务进程异常退出，
		在原来的docker版本，那么所有开启的docker container都会挂掉，惨了，相当于很多个container就失效了；也造成了单机的docker是单点的；
		那么1.12来了，解决了这个实际运用的问题，就是dockerd服务怎样关闭，容器照样运行，服务恢复后，容器也可以再被服务抓到并可管理。
		参考 http://blog.sina.com.cn/s/blog_67fbe4650102x2po.html
		*/
		liveRestore:   r.liveRestore,
	}

	r.Lock()
	r.clients = append(r.clients, c)
	r.Unlock()
	return c, nil
}

//本文件中的 handleEventStream 调用执行
func (r *remote) updateEventTimestamp(t time.Time) {
	f, err := os.OpenFile(r.eventTsPath, syscall.O_CREAT|syscall.O_WRONLY|syscall.O_TRUNC, 0600)
	if err != nil {
		logrus.Warnf("libcontainerd: failed to open event timestamp file: %v", err)
		return
	}
	defer f.Close()

	b, err := t.MarshalText()
	if err != nil {
		logrus.Warnf("libcontainerd: failed to encode timestamp: %v", err)
		return
	}

	n, err := f.Write(b)
	if err != nil || n != len(b) {
		logrus.Warnf("libcontainerd: failed to update event timestamp file: %v", err)
		f.Truncate(0)
		return
	}
}

//从//run/docker/libcontainerd/event.ts文件中读出最近一次event时间
func (r *remote) getLastEventTimestamp() time.Time {
	t := time.Now()

	fi, err := os.Stat(r.eventTsPath)
	if os.IsNotExist(err) || fi.Size() == 0 {
		return t
	}

	f, err := os.Open(r.eventTsPath)
	if err != nil {
		logrus.Warnf("libcontainerd: Unable to access last event ts: %v", err)
		return t
	}
	defer f.Close()

	b := make([]byte, fi.Size())
	n, err := f.Read(b)
	if err != nil || n != len(b) {
		logrus.Warnf("libcontainerd: Unable to read last event ts: %v", err)
		return t
	}

	t.UnmarshalText(b)

	return t
}

//记录 StateStart  StatePause 等事件
//记录 StateStart  StatePause 等事件，可以参考(ctr *container) start 记录事件过程
func (r *remote) startEventsMonitor() error {
	// First, get past events
	t := r.getLastEventTimestamp()
	tsp, err := ptypes.TimestampProto(t)
	if err != nil {
		logrus.Errorf("libcontainerd: failed to convert timestamp: %q", err)
	}
	er := &containerd.EventsRequest{
		Timestamp: tsp,
	}

	events, err := r.apiClient.Events(context.Background(), er, grpc.FailFast(false))
	if err != nil {
		return err
	}

	//记录 StateStart  StatePause 等事件
	go r.handleEventStream(events)
	return nil
}

//记录 StateStart  StatePause 等事件，可以参考(ctr *container) start 记录事件过程
func (r *remote) handleEventStream(events containerd.API_EventsClient) {
	for {
		e, err := events.Recv()
		if err != nil {
			if grpc.ErrorDesc(err) == transport.ErrConnClosing.Desc &&
				r.closeManually {
				// ignore error if grpc remote connection is closed manually
				return
			}
			logrus.Errorf("libcontainerd: failed to receive event from containerd: %v", err)
			go r.startEventsMonitor()
			return
		}
		/* 例如  StateStart  StatePause
		libcontainerd: received containerd event: &types.Event{Type:"start-container", Id:"7a6dd7a4d9fa953ea895d549f5b0a66f1d7c400ac7342b0182ec673b33f7233c", Status:0x0, Pid:"", Timestamp:(*timestamp.Timestamp)(0xc420996820)}
		libcontainerd: received containerd event: &types.Event{Type:"exit", Id:"7a6dd7a4d9fa953ea895d549f5b0a66f1d7c400ac7342b0182ec673b33f7233c", Status:0x0, Pid:"init", Timestamp:(*timestamp.Timestamp)(0xc420b101f0)}
		*/
		logrus.Debugf("libcontainerd: received containerd event: %#v", e)

		var container *container
		var c *client
		r.RLock()
		for _, c = range r.clients {
			container, err = c.getContainer(e.Id)
			if err == nil {
				break
			}
		}
		r.RUnlock()
		if container == nil {
			logrus.Warnf("libcontainerd: unknown container %s", e.Id)
			continue
		}

		if err := container.handleEvent(e); err != nil {
			logrus.Errorf("libcontainerd: error processing state change for %s: %v", e.Id, err)
		}

		tsp, err := ptypes.Timestamp(e.Timestamp)
		if err != nil {
			logrus.Errorf("libcontainerd: failed to convert event timestamp: %q", err)
			continue
		}

		r.updateEventTimestamp(tsp)
	}
}

//remote_unix.go中的new接口调用执行
func (r *remote) runContainerdDaemon() error {
	//containerd进程Pid文件
	pidFilename := filepath.Join(r.stateDir, containerdPidFilename)
	f, err := os.OpenFile(pidFilename, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	// File exist, check if the daemon is alive
	b := make([]byte, 8)
	n, err := f.Read(b)
	if err != nil && err != io.EOF {
		return err
	}

	if n > 0 { //说明该进程已经存在了
		pid, err := strconv.ParseUint(string(b[:n]), 10, 64)
		if err != nil {
			return err
		}
		if system.IsProcessAlive(int(pid)) {
			logrus.Infof("libcontainerd: previous instance of containerd still alive (%d)", pid)
			r.daemonPid = int(pid)
			return nil
		}
	}

	// rewind the file
	_, err = f.Seek(0, os.SEEK_SET)
	if err != nil {
		return err
	}

	// Truncate it
	err = f.Truncate(0)
	if err != nil {
		return err
	}

	/*
	/usr/bin/docker-containerd-current -l unix:///var/run/docker/libcontainerd/docker-containerd.sock --metrics-interval=0 --start-timeout 2m
	--state-dir /var/run/docker/libcontainerd/containerd --shim docker-containerd-shim --runtime docker-runc --debug
	*/
	// Start a new instance
	args := []string{
		"-l", fmt.Sprintf("unix://%s", r.rpcAddr),
		"--metrics-interval=0",
		"--start-timeout", "2m",
		"--state-dir", filepath.Join(r.stateDir, containerdStateDir),
	}
	if goruntime.GOOS == "solaris" {
		args = append(args, "--shim", "containerd-shim", "--runtime", "runc")
	} else {
		/*
		docker-containerd-shim 9be24974a6a7cf064f4a238f70260b13b15359248b3267602bfc49e00f13d670
		/var/run/docker/libcontainerd/9be24974a6a7cf064f4a238f70260b13b15359248b3267602bfc49e00f13d670 docker-runc

		docker-container 进程(container 组件)会创建 docker-containerd-shim 进程. 其中 2b9251bcc7a4484662c8b69174d92b3183f0f09a59264b412f14341ebb759626 就是要启动容器的 ID
		*/
		args = append(args, "--shim", "docker-containerd-shim")
		if r.runtime != "" {
			args = append(args, "--runtime")
			args = append(args, r.runtime)
		}
	}
	if r.debugLog {
		args = append(args, "--debug")
	}
	if len(r.runtimeArgs) > 0 {
		for _, v := range r.runtimeArgs {
			args = append(args, "--runtime-args")
			args = append(args, v)
		}
		logrus.Debugf("libcontainerd: runContainerdDaemon: runtimeArgs: %s", args)
	}

	// docker-containerd -l unix:///var/run/docker/libcontainerd/docker-containerd.sock --metrics-interval=0 --start-timeout 2m
	// --state-dir /var/run/docker/libcontainerd/containerd --shim docker-containerd-shim --runtime docker-runc
	cmd := exec.Command(containerdBinary, args...) //启动docker-containerd进程
	// redirect containerd logs to docker logs
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = setSysProcAttr(true)
	cmd.Env = nil
	// clear the NOTIFY_SOCKET from the env when starting containerd
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "NOTIFY_SOCKET") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	logrus.Infof("libcontainerd: new containerd process, pid: %d", cmd.Process.Pid)
	if err := setOOMScore(cmd.Process.Pid, r.oomScore); err != nil {
		system.KillProcess(cmd.Process.Pid)
		return err
	}
	if _, err := f.WriteString(fmt.Sprintf("%d", cmd.Process.Pid)); err != nil {
		system.KillProcess(cmd.Process.Pid)
		return err
	}

	r.daemonWaitCh = make(chan struct{})
	go func() { //专门起一个携程来wait containerd进程结束
		//只有docker-containerd程序执行完毕退出，cmd.wait才会返回
		cmd.Wait()
		close(r.daemonWaitCh)
	}() // Reap our child when needed
	r.daemonPid = cmd.Process.Pid
	return nil
}

// WithRemoteAddr sets the external containerd socket to connect to.
//getPlatformRemoteOptions 调用执行
func WithRemoteAddr(addr string) RemoteOption {
	return rpcAddr(addr)
}

//remote相关的配置解析相关
type rpcAddr string
func (a rpcAddr) Apply(r Remote) error {
	if remote, ok := r.(*remote); ok {
		remote.rpcAddr = string(a)
		return nil
	}
	return fmt.Errorf("WithRemoteAddr option not supported for this remote")
}

// WithRuntimePath sets the path of the runtime to be used as the
// default by containerd
//remote相关的配置解析相关
func WithRuntimePath(rt string) RemoteOption {
	return runtimePath(rt)
}
type runtimePath string
func (rt runtimePath) Apply(r Remote) error {
	if remote, ok := r.(*remote); ok {
		remote.runtime = string(rt)
		return nil
	}
	return fmt.Errorf("WithRuntime option not supported for this remote")
}

// WithRuntimeArgs sets the list of runtime args passed to containerd
func WithRuntimeArgs(args []string) RemoteOption {
	return runtimeArgs(args)
}

type runtimeArgs []string  //Apply对remote指定参数赋值
func (rt runtimeArgs) Apply(r Remote) error {
	if remote, ok := r.(*remote); ok {
		remote.runtimeArgs = rt
		return nil
	}
	return fmt.Errorf("WithRuntimeArgs option not supported for this remote")
}

// WithStartDaemon defines if libcontainerd should also run containerd daemon.
func WithStartDaemon(start bool) RemoteOption {
	return startDaemon(start)
}

type startDaemon bool

func (s startDaemon) Apply(r Remote) error {
	if remote, ok := r.(*remote); ok {
		remote.startDaemon = bool(s)
		return nil
	}
	return fmt.Errorf("WithStartDaemon option not supported for this remote")
}

// WithDebugLog defines if containerd debug logs will be enabled for daemon.
func WithDebugLog(debug bool) RemoteOption {
	return debugLog(debug)
}

type debugLog bool

func (d debugLog) Apply(r Remote) error {
	if remote, ok := r.(*remote); ok {
		remote.debugLog = bool(d)
		return nil
	}
	return fmt.Errorf("WithDebugLog option not supported for this remote")
}

// WithLiveRestore defines if containers are stopped on shutdown or restored.
func WithLiveRestore(v bool) RemoteOption {
	return liveRestore(v)
}

type liveRestore bool

func (l liveRestore) Apply(r Remote) error {
	if remote, ok := r.(*remote); ok {
		remote.liveRestore = bool(l)
		for _, c := range remote.clients {
			c.liveRestore = bool(l)
		}
		return nil
	}
	return fmt.Errorf("WithLiveRestore option not supported for this remote")
}

// WithOOMScore defines the oom_score_adj to set for the containerd process.
func WithOOMScore(score int) RemoteOption {
	return oomScore(score)
}

type oomScore int

func (o oomScore) Apply(r Remote) error {
	if remote, ok := r.(*remote); ok {
		remote.oomScore = int(o)
		return nil
	}
	return fmt.Errorf("WithOOMScore option not supported for this remote")
}
