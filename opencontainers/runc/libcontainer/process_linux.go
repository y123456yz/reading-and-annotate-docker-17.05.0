// +build linux

package libcontainer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/opencontainers/runc/libcontainer/cgroups"
	"github.com/opencontainers/runc/libcontainer/configs"
	"github.com/opencontainers/runc/libcontainer/system"
	"github.com/opencontainers/runc/libcontainer/utils"
)

//initProcess 中实现这些接口， 赋值见newParentProcess
type parentProcess interface {
	// pid returns the pid for the running process.
	pid() int

	// start starts the process execution.
	start() error

	// send a SIGKILL to the process and wait for the exit.
	terminate() error

	// wait waits on the process returning the process state.
	wait() (*os.ProcessState, error)

	// startTime returns the process start time.
	startTime() (string, error)

	signal(os.Signal) error

	externalDescriptors() []string

	setExternalDescriptors(fds []string)
}

type setnsProcess struct {
	cmd           *exec.Cmd
	//本进程和init进程通过管道 parentPipe 和 childPipe 通信
	parentPipe    *os.File
	childPipe     *os.File
	cgroupPaths   map[string]string
	config        *initConfig
	fds           []string
	process       *Process
	bootstrapData io.Reader
	rootDir       *os.File
}

func (p *setnsProcess) startTime() (string, error) {
	return system.GetProcessStartTime(p.pid())
}

func (p *setnsProcess) signal(sig os.Signal) error {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return errors.New("os: unsupported signal type")
	}
	return syscall.Kill(p.pid(), s)
}

//(c *linuxContainer) start 中如果是runc create则这里对应(p *initProcess) start()，否则对应 (p *setnsProcess) start()
func (p *setnsProcess) start() (err error) {
	defer p.parentPipe.Close()
	err = p.cmd.Start()
	p.childPipe.Close()
	p.rootDir.Close()
	if err != nil {
		return newSystemErrorWithCause(err, "starting setns process")
	}
	if p.bootstrapData != nil {
		if _, err := io.Copy(p.parentPipe, p.bootstrapData); err != nil {
			return newSystemErrorWithCause(err, "copying bootstrap data to pipe")
		}
	}
	if err = p.execSetns(); err != nil {
		return newSystemErrorWithCause(err, "executing setns process")
	}
	if len(p.cgroupPaths) > 0 {
		if err := cgroups.EnterPid(p.cgroupPaths, p.pid()); err != nil {
			return newSystemErrorWithCausef(err, "adding pid %d to cgroups", p.pid())
		}
	}
	// set oom_score_adj
	if err := setOomScoreAdj(p.config.Config.OomScoreAdj, p.pid()); err != nil {
		return newSystemErrorWithCause(err, "setting oom score")
	}
	// set rlimits, this has to be done here because we lose permissions
	// to raise the limits once we enter a user-namespace
	if err := setupRlimits(p.config.Rlimits, p.pid()); err != nil {
		return newSystemErrorWithCause(err, "setting rlimits for process")
	}
	if err := utils.WriteJSON(p.parentPipe, p.config); err != nil {
		return newSystemErrorWithCause(err, "writing config to pipe")
	}

	if err := syscall.Shutdown(int(p.parentPipe.Fd()), syscall.SHUT_WR); err != nil {
		return newSystemErrorWithCause(err, "calling shutdown on init pipe")
	}
	// wait for the child process to fully complete and receive an error message
	// if one was encoutered
	var ierr *genericError
	if err := json.NewDecoder(p.parentPipe).Decode(&ierr); err != nil && err != io.EOF {
		return newSystemErrorWithCause(err, "decoding init error from pipe")
	}
	// Must be done after Shutdown so the child will exit and we can wait for it.
	if ierr != nil {
		p.wait()
		return ierr
	}
	return nil
}

// execSetns runs the process that executes C code to perform the setns calls
// because setns support requires the C process to fork off a child and perform the setns
// before the go runtime boots, we wait on the process to die and receive the child's pid
// over the provided pipe.
func (p *setnsProcess) execSetns() error {
	status, err := p.cmd.Process.Wait()
	if err != nil {
		p.cmd.Wait()
		return newSystemErrorWithCause(err, "waiting on setns process to finish")
	}
	if !status.Success() {
		p.cmd.Wait()
		return newSystemError(&exec.ExitError{ProcessState: status})
	}
	var pid *pid
	if err := json.NewDecoder(p.parentPipe).Decode(&pid); err != nil {
		p.cmd.Wait()
		return newSystemErrorWithCause(err, "reading pid from init pipe")
	}
	process, err := os.FindProcess(pid.Pid)
	if err != nil {
		return err
	}
	p.cmd.Process = process
	p.process.ops = p
	return nil
}

// terminate sends a SIGKILL to the forked process for the setns routine then waits to
// avoid the process becoming a zombie.
func (p *setnsProcess) terminate() error {
	if p.cmd.Process == nil {
		return nil
	}
	err := p.cmd.Process.Kill()
	if _, werr := p.wait(); err == nil {
		err = werr
	}
	return err
}

func (p *setnsProcess) wait() (*os.ProcessState, error) {
	err := p.cmd.Wait()

	// Return actual ProcessState even on Wait error
	return p.cmd.ProcessState, err
}

func (p *setnsProcess) pid() int {
	return p.cmd.Process.Pid
}

func (p *setnsProcess) externalDescriptors() []string {
	return p.fds
}

func (p *setnsProcess) setExternalDescriptors(newFds []string) {
	p.fds = newFds
}

//newInitProcess 中构造使用该类   对应init进程
type initProcess struct {
	/*
	[root@newnamespace runc]# ll /proc/self/exe
	lrwxrwxrwx. 1 root root 0 Nov 14 14:39 /proc/self/exe -> /usr/bin/ls
	*/
	// []string{"/proc/self/exe", "init"},  commandTemplate 中组该cmd
	cmd           *exec.Cmd
	//通信用，可以参考sendConfig
	//本进程和init进程通过管道 parentPipe 和 childPipe 通信
	parentPipe    *os.File
	childPipe     *os.File
	//newInitConfig 中初始化
	config        *initConfig
	manager       cgroups.Manager
	container     *linuxContainer
	//赋值见setExternalDescriptors
	fds           []string
	process       *Process
	//赋值见newInitProcess，数据来源见 对于namespace的隔离，主要通过bootstrapData封装好clone flags。
	bootstrapData io.Reader
	sharePidns    bool
	rootDir       *os.File
}

func (p *initProcess) pid() int {
	return p.cmd.Process.Pid
}

func (p *initProcess) externalDescriptors() []string {
	return p.fds
}

// execSetns runs the process that executes C code to perform the setns calls
// because setns support requires the C process to fork off a child and perform the setns
// before the go runtime boots, we wait on the process to die and receive the child's pid
// over the provided pipe.
// This is called by initProcess.start function
//// 获取容器init进程的pid，
func (p *initProcess) execSetns() error {
	status, err := p.cmd.Process.Wait()
	if err != nil {
		p.cmd.Wait()
		return err
	}
	if !status.Success() {
		p.cmd.Wait()
		return &exec.ExitError{ProcessState: status}
	}
	var pid *pid
	if err := json.NewDecoder(p.parentPipe).Decode(&pid); err != nil {
		p.cmd.Wait()
		return err
	}
	process, err := os.FindProcess(pid.Pid)
	if err != nil {
		return err
	}
	p.cmd.Process = process
	p.process.ops = p
	return nil
}

/*
这里是父进程也就是我们当前进程执行的内容，根据我们上一章介绍的内容，应该比较容易明白
1.这里的/proc/self/exe 调用，其中/proc/self指的是当前运行进程自己的环境，exec其实就是自己调用了自己，我们使用这种方式实现对创建出来的进程进行初始化
2.后面args是参数，其中 init 是传递给本进程的第一个参数，这在本例子中，其实就是会去调用我们的initCommand去初始化进程的一些环境和资源
3. 下面的clone 参数就是去 fork 出来的一个新进程，并且使用了namespace隔离新创建的进程和外部的环境。
4. 如果用户指定了-ti 参数，我们就需要把当前进程的输入输出导入到标准输入输出上
func NewParentProcess(tty bool, command string) *exec.Cmd {
	args := []string{"init", command}
	cmd := exec.Command("/proc/self/exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS |
			syscall.CLONE_NEWNET | syscall.CLONE_NEWIPC,
	}
	if tty {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd
}
*/

//(c *linuxContainer) start 中如果是runc create则这里对应(p *initProcess) start()，否则对应 (p *setnsProcess) start()
//(c *linuxContainer) start 中执行
//调用 parent.start() 启动 initCommand 对应的进程
func (p *initProcess) start() error {
	defer p.parentPipe.Close()

	/*
	[root@newnamespace runc]# ps -ef | grep init
	root      1996     1  0 18:08 ?        00:00:00 /proc/self/exe init
	1.这里的/proc/self/exe 调用，其中/proc/self指的是当前运行进程自己的环境，exec其实就是自己调用了自己，我们使用这种方式实现对创建出来的进程进行初始化
	2.后面args是参数，其中 init 是传递给本进程的第一个参数，这在本例子中，其实就是会去调用我们的 initCommand 去初始化进程的一些环境和资源
	*/
	//调用p.cmd.Start启动 容器的 initCommand init进程
	err := p.cmd.Start()
	p.process.ops = p
	p.childPipe.Close()
	p.rootDir.Close()

	//yang test .... initprocess tart:/proc/self/exe
	//fmt.Printf("yang test .... initprocess tart:%s\n\n", p.cmd.Path);
	if err != nil {
		p.process.ops = nil
		return newSystemErrorWithCause(err, "starting init process command")
	}

	// 将bootstrapData的数据(namespace flag等信息)传入runc init ，本进程和init进程通过管道 parentPipe 和 childPipe 通信
	if _, err := io.Copy(p.parentPipe, p.bootstrapData); err != nil {
		return err
	}

	// 获取容器init进程的pid，
	if err := p.execSetns(); err != nil {
		return newSystemErrorWithCause(err, "running exec setns process for init")
	}
	// Save the standard descriptor names before the container process
	// can potentially move them (e.g., via dup2()).  If we don't do this now,
	// we won't know at checkpoint time which file descriptor to look up.
	fds, err := getPipeFds(p.pid())
	if err != nil {
		return newSystemErrorWithCausef(err, "getting pipe fds for pid %d", p.pid())
	}
	p.setExternalDescriptors(fds)
	// Do this before syncing with child so that no children
	// can escape the cgroup
	if err := p.manager.Apply(p.pid()); err != nil {
		return newSystemErrorWithCause(err, "applying cgroup configuration for process")
	}

	defer func() {
		if err != nil {
			// TODO: should not be the responsibility to call here
			p.manager.Destroy()
		}
	}()

	//创建网络接口
	if err := p.createNetworkInterfaces(); err != nil {
		return newSystemErrorWithCause(err, "creating network interfaces")
	}

	// (p *initProcess) sendConfig()  发送配置信息到init进程
	if err := p.sendConfig(); err != nil {
		return newSystemErrorWithCause(err, "sending config to init process")
	}
	var (
		procSync   syncT
		sentRun    bool
		sentResume bool
		ierr       *genericError
	)

	dec := json.NewDecoder(p.parentPipe)
loop:
	for { //进入loop，从p.parenPipe中获取同步状态procSync
		if err := dec.Decode(&procSync); err != nil {
			if err == io.EOF {
				break loop
			}
			return newSystemErrorWithCause(err, "decoding sync type from init pipe")
		}
		switch procSync.Type {
		case procReady:
			if err := p.manager.Set(p.config.Config); err != nil {
				return newSystemErrorWithCause(err, "setting cgroup config for ready process")
			}
			// set oom_score_adj
			if err := setOomScoreAdj(p.config.Config.OomScoreAdj, p.pid()); err != nil {
				return newSystemErrorWithCause(err, "setting oom score for ready process")
			}
			// set rlimits, this has to be done here because we lose permissions
			// to raise the limits once we enter a user-namespace
			if err := setupRlimits(p.config.Rlimits, p.pid()); err != nil {
				return newSystemErrorWithCause(err, "setting rlimits for ready process")
			}
			// call prestart hooks
			if !p.config.Config.Namespaces.Contains(configs.NEWNS) {
				if p.config.Config.Hooks != nil {
					s := configs.HookState{
						Version: p.container.config.Version,
						ID:      p.container.id,
						Pid:     p.pid(),
						Root:    p.config.Config.Rootfs,
					}
					for i, hook := range p.config.Config.Hooks.Prestart {
						if err := hook.Run(s); err != nil {
							return newSystemErrorWithCausef(err, "running prestart hook %d", i)
						}
					}
				}
			}
			// Sync with child. 通知init进程
			if err := utils.WriteJSON(p.parentPipe, syncT{procRun}); err != nil {
				return newSystemErrorWithCause(err, "writing syncT run type")
			}
			sentRun = true
		case procHooks:
			if p.config.Config.Hooks != nil {
				s := configs.HookState{
					Version:    p.container.config.Version,
					ID:         p.container.id,
					Pid:        p.pid(),
					Root:       p.config.Config.Rootfs,
					BundlePath: utils.SearchLabels(p.config.Config.Labels, "bundle"),
				}
				for i, hook := range p.config.Config.Hooks.Prestart {
					if err := hook.Run(s); err != nil {
						return newSystemErrorWithCausef(err, "running prestart hook %d", i)
					}
				}
			}
			// Sync with child.
			if err := utils.WriteJSON(p.parentPipe, syncT{procResume}); err != nil {
				return newSystemErrorWithCause(err, "writing syncT resume type")
			}
			sentResume = true
		case procError:
			// wait for the child process to fully complete and receive an error message
			// if one was encoutered
			if err := dec.Decode(&ierr); err != nil && err != io.EOF {
				return newSystemErrorWithCause(err, "decoding proc error from init")
			}
			if ierr != nil {
				break loop
			}
			// Programmer error.
			panic("No error following JSON procError payload.")
		default:
			return newSystemError(fmt.Errorf("invalid JSON payload from child"))
		}
	}
	if !sentRun {
		return newSystemErrorWithCause(ierr, "container init")
	}
	if p.config.Config.Namespaces.Contains(configs.NEWNS) && !sentResume {
		return newSystemError(fmt.Errorf("could not synchronise after executing prestart hooks with container process"))
	}
	if err := syscall.Shutdown(int(p.parentPipe.Fd()), syscall.SHUT_WR); err != nil {
		return newSystemErrorWithCause(err, "shutting down init pipe")
	}
	// Must be done after Shutdown so the child will exit and we can wait for it.
	if ierr != nil {
		p.wait()
		return ierr
	}
	return nil
}

func (p *initProcess) wait() (*os.ProcessState, error) {
	err := p.cmd.Wait()
	if err != nil {
		return p.cmd.ProcessState, err
	}
	// we should kill all processes in cgroup when init is died if we use host PID namespace
	if p.sharePidns {
		signalAllProcesses(p.manager, syscall.SIGKILL)
	}
	return p.cmd.ProcessState, nil
}

func (p *initProcess) terminate() error {
	if p.cmd.Process == nil {
		return nil
	}
	err := p.cmd.Process.Kill()
	if _, werr := p.wait(); err == nil {
		err = werr
	}
	return err
}

func (p *initProcess) startTime() (string, error) {
	return system.GetProcessStartTime(p.pid())
}

//发送配置信息到init进程  sendconfig将bootstrapData封装的config传给容器起的init process。
func (p *initProcess) sendConfig() error {
	// send the config to the container's init process, we don't use JSON Encode
	// here because there might be a problem in JSON decoder in some cases, see:
	// https://github.com/docker/docker/issues/14203#issuecomment-174177790
	return utils.WriteJSON(p.parentPipe, p.config)
}

//创建网络接口
func (p *initProcess) createNetworkInterfaces() error {
	for _, config := range p.config.Config.Networks {
		strategy, err := getStrategy(config.Type)
		if err != nil {
			return err
		}
		n := &network{
			Network: *config,
		}
		if err := strategy.create(n, p.pid()); err != nil {
			return err
		}
		p.config.Networks = append(p.config.Networks, n)
	}

	return nil
}

func (p *initProcess) signal(sig os.Signal) error {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return errors.New("os: unsupported signal type")
	}
	return syscall.Kill(p.pid(), s)
}

func (p *initProcess) setExternalDescriptors(newFds []string) {
	p.fds = newFds
}

func getPipeFds(pid int) ([]string, error) {
	fds := make([]string, 3)

	dirPath := filepath.Join("/proc", strconv.Itoa(pid), "/fd")
	for i := 0; i < 3; i++ {
		f := filepath.Join(dirPath, strconv.Itoa(i))
		target, err := os.Readlink(f)
		if err != nil {
			return fds, err
		}
		fds[i] = target
	}
	return fds, nil
}

// InitializeIO creates pipes for use with the process's STDIO
// and returns the opposite side for each
func (p *Process) InitializeIO(rootuid, rootgid int) (i *IO, err error) {
	var fds []uintptr
	i = &IO{}
	// cleanup in case of an error
	defer func() {
		if err != nil {
			for _, fd := range fds {
				syscall.Close(int(fd))
			}
		}
	}()
	// STDIN
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	fds = append(fds, r.Fd(), w.Fd())
	p.Stdin, i.Stdin = r, w
	// STDOUT
	if r, w, err = os.Pipe(); err != nil {
		return nil, err
	}
	fds = append(fds, r.Fd(), w.Fd())
	p.Stdout, i.Stdout = w, r
	// STDERR
	if r, w, err = os.Pipe(); err != nil {
		return nil, err
	}
	fds = append(fds, r.Fd(), w.Fd())
	p.Stderr, i.Stderr = w, r
	// change ownership of the pipes incase we are in a user namespace
	for _, fd := range fds {
		if err := syscall.Fchown(int(fd), rootuid, rootgid); err != nil {
			return nil, err
		}
	}
	return i, nil
}
