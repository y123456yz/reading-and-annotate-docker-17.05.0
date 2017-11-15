// +build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/Sirupsen/logrus"
	"github.com/coreos/go-systemd/activation"
	"github.com/opencontainers/runc/libcontainer"
	"github.com/opencontainers/runc/libcontainer/cgroups/systemd"
	"github.com/opencontainers/runc/libcontainer/specconv"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

var errEmptyID = errors.New("container id cannot be empty")

var container libcontainer.Container

// loadFactory returns the configured factory instance for execing containers.
//获取root dir的绝对路径abs(默认为/run/runc，用于存储容器的状态)以及cgroupManager
//生产一个Factory实例  返回LinuxFactory
func loadFactory(context *cli.Context) (libcontainer.Factory, error) {
	root := context.GlobalString("root")
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cgroupManager := libcontainer.Cgroupfs
	if context.GlobalBool("systemd-cgroup") {
		if systemd.UseSystemd() {
			cgroupManager = libcontainer.SystemdCgroups
		} else {
			return nil, fmt.Errorf("systemd cgroup flag passed, but systemd support for managing cgroups is not available")
		}
	}

	//返回 LinuxFactory 结构
	return libcontainer.New(abs, cgroupManager, libcontainer.CriuPath(context.GlobalString("criu")))
}

// getContainer returns the specified container instance by loading it from state
// with the default factory.
func getContainer(context *cli.Context) (libcontainer.Container, error) {
	id := context.Args().First()
	if id == "" {
		return nil, errEmptyID
	}
	factory, err := loadFactory(context)
	if err != nil {
		return nil, err
	}
	return factory.Load(id)
}

func fatalf(t string, v ...interface{}) {
	fatal(fmt.Errorf(t, v...))
}

func getDefaultImagePath(context *cli.Context) string {
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return filepath.Join(cwd, "checkpoint")
}

// newProcess returns a new libcontainer Process with the arguments from the
// spec and stdio from the current process.
/*
  "process": {
    "terminal": true,
    "consoleSize": {
      "height": 0,
      "width": 0
    },
    "user": {
      "uid": 0,
      "gid": 0
    },
    "args": [
      "/sbin/init"
    ],
    "env": [
      "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
      "HOSTNAME=9be24974a6a7",
      "TERM=xterm",
      "OCEANBANK_VM=1"
    ],
    "cwd": "/",
    "capabilities": [
      "CAP_CHOWN",
      "CAP_DAC_OVERRIDE",
      "CAP_DAC_READ_SEARCH",
      "CAP_FOWNER",
      "CAP_FSETID",
      "CAP_KILL",
      "CAP_SETGID",
      "CAP_SETUID",
      "CAP_SETPCAP",
      "CAP_LINUX_IMMUTABLE",
      "CAP_NET_BIND_SERVICE",
      "CAP_NET_BROADCAST",
      "CAP_NET_ADMIN",
      "CAP_NET_RAW",
      "CAP_IPC_LOCK",
      "CAP_IPC_OWNER",
      "CAP_SYS_MODULE",
      "CAP_SYS_RAWIO",
      "CAP_SYS_CHROOT",
      "CAP_SYS_PTRACE",
      "CAP_SYS_PACCT",
      "CAP_SYS_ADMIN",
      "CAP_SYS_BOOT",
      "CAP_SYS_NICE",
      "CAP_SYS_RESOURCE",
      "CAP_SYS_TIME",
      "CAP_SYS_TTY_CONFIG",
      "CAP_MKNOD",
      "CAP_LEASE",
      "CAP_AUDIT_WRITE",
      "CAP_AUDIT_CONTROL",
      "CAP_SETFCAP",
      "CAP_MAC_OVERRIDE",
      "CAP_MAC_ADMIN",
      "CAP_SYSLOG",
      "CAP_WAKE_ALARM",
      "CAP_BLOCK_SUSPEND"
    ]
  },   参考/run/docker/libcontainerd/xxxx/config.json
*/
//根据config配置获取一个libcontainer.Process对象
func newProcess(p specs.Process) (*libcontainer.Process, error) {
	lp := &libcontainer.Process{
		Args: p.Args,
		Env:  p.Env,
		// TODO: fix libcontainer's API to better support uid/gid in a typesafe way.
		User:            fmt.Sprintf("%d:%d", p.User.UID, p.User.GID),
		Cwd:             p.Cwd,
		Capabilities:    p.Capabilities,
		Label:           p.SelinuxLabel,
		NoNewPrivileges: &p.NoNewPrivileges,
		AppArmorProfile: p.ApparmorProfile,
	}
	for _, gid := range p.User.AdditionalGids {
		lp.AdditionalGroups = append(lp.AdditionalGroups, strconv.FormatUint(uint64(gid), 10))
	}
	for _, rlimit := range p.Rlimits {
		rl, err := createLibContainerRlimit(rlimit)
		if err != nil {
			return nil, err
		}
		lp.Rlimits = append(lp.Rlimits, rl)
	}
	return lp, nil
}

func dupStdio(process *libcontainer.Process, rootuid, rootgid int) error {
	process.Stdin = os.Stdin
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	for _, fd := range []uintptr{
		os.Stdin.Fd(),
		os.Stdout.Fd(),
		os.Stderr.Fd(),
	} {
		if err := syscall.Fchown(int(fd), rootuid, rootgid); err != nil {
			return err
		}
	}
	return nil
}

// If systemd is supporting sd_notify protocol, this function will add support
// for sd_notify protocol from within the container.
func setupSdNotify(spec *specs.Spec, notifySocket string) {
	spec.Mounts = append(spec.Mounts, specs.Mount{Destination: notifySocket, Type: "bind", Source: notifySocket, Options: []string{"bind"}})
	spec.Process.Env = append(spec.Process.Env, fmt.Sprintf("NOTIFY_SOCKET=%s", notifySocket))
}

func destroy(container libcontainer.Container) {
	if err := container.Destroy(); err != nil {
		logrus.Error(err)
	}
}

// setupIO sets the proper IO on the process depending on the configuration
// If there is a nil error then there must be a non nil tty returned

//调用setupIO来进行io和tty相关配置，对于create来说，这里就是dup将当前进程的io，chown用户/组权限。
func setupIO(process *libcontainer.Process, rootuid, rootgid int, console string, createTTY, detach bool) (*tty, error) {
	// detach and createTty will not work unless a console path is passed
	// so error out here before changing any terminal settings
	if createTTY && detach && console == "" {
		return nil, fmt.Errorf("cannot allocate tty if runc will detach")
	}
	if createTTY {
		return createTty(process, rootuid, rootgid, console)
	}
	if detach {
		//赋值系统的 Stdin Stdout Stderr给process
		if err := dupStdio(process, rootuid, rootgid); err != nil {
			return nil, err
		}
		return &tty{}, nil
	}
	return createStdioPipes(process, rootuid, rootgid)
}

// createPidFile creates a file with the processes pid inside it atomically
// it creates a temp file with the paths filename + '.' infront of it
// then renames the file
func createPidFile(path string, process *libcontainer.Process) error {
	pid, err := process.Pid()
	if err != nil {
		return err
	}
	var (
		tmpDir  = filepath.Dir(path)
		tmpName = filepath.Join(tmpDir, fmt.Sprintf(".%s", filepath.Base(path)))
	)
	f, err := os.OpenFile(tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL|os.O_SYNC, 0666)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%d", pid)
	f.Close()
	if err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

//创建容器，获取 linuxContainer 结构   id为容器名
func createContainer(context *cli.Context, id string, spec *specs.Spec) (libcontainer.Container, error) {
	//将config.json配置转换为符合libcontainer要求的配置格式存储到config结构
	config, err := specconv.CreateLibcontainerConfig(&specconv.CreateOpts{
		CgroupName:       id,
		UseSystemdCgroup: context.GlobalBool("systemd-cgroup"),
		NoPivotRoot:      context.Bool("no-pivot"), //runc create --no-pivot启用
		NoNewKeyring:     context.Bool("no-new-keyring"),  //runc spec --help查看该命令携带该参数
		Spec:             spec,
	})
	if err != nil {
		return nil, err
	}

	//返回 LinuxFactory 结构
	factory, err := loadFactory(context)
	if err != nil {
		return nil, err
	}

	// (l *LinuxFactory) Create   填充一个 linuxContainer 对象
	return factory.Create(id, config)
}

//startContainer 中构造该结构
type runner struct {
	//命令行中 no-subreaper 配置项指定
	enableSubreaper bool
	shouldDestroy   bool
	//命令行"detach"指定
	detach          bool
	//从环境变量 LISTEN_FDS 中获取，见startContainer
	listenFDs       []*os.File
	//命令行"pid-file"指定
	pidFile         string
	//命令行"console"指定
	console         string
	//linuxContainer
	container       libcontainer.Container
	//runc create为ture  runc run为false
	create          bool
}

//config来源是 config.json 文件
func (r *runner) run(config *specs.Process) (int, error) {
	//根据config配置一个libcontainer.Process对象
	process, err := newProcess(*config)
	if err != nil {
		r.destroy()
		return -1, err
	}

	//如果r.listenFDs的数目不为0的话，扩展process.Env和process.ExtraFiles，然后获取rootuid和rootgid。
	if len(r.listenFDs) > 0 {
		process.Env = append(process.Env, fmt.Sprintf("LISTEN_FDS=%d", len(r.listenFDs)), "LISTEN_PID=1")
		process.ExtraFiles = append(process.ExtraFiles, r.listenFDs...)
	}

	rootuid, err := r.container.Config().HostUID()
	if err != nil {
		r.destroy()
		return -1, err
	}
	rootgid, err := r.container.Config().HostGID()
	if err != nil {
		r.destroy()
		return -1, err
	}
	tty, err := setupIO(process, rootuid, rootgid, r.console, config.Terminal, r.detach || r.create)
	if err != nil {
		r.destroy()
		return -1, err
	}

	handler := newSignalHandler(tty, r.enableSubreaper)
	startFn := r.container.Start
	//调用startFn(process)，如果是create容器的话，那么startFn := r.container.Start，否则startFn := r.container.Run
	if !r.create {
		startFn = r.container.Run
	}

	defer tty.Close()
	//调用startFn(process)，如果是create容器的话，那么startFn := r.container.Start，否则startFn := r.container.Run
	if err := startFn(process); err != nil {
		r.destroy()
		return -1, err
	}

	//调用tty.ClosePostStart()关闭已经传递给容器的fds。
	if err := tty.ClosePostStart(); err != nil {
		r.terminate(process)
		r.destroy()
		return -1, err
	}
	if r.pidFile != "" {
		if err := createPidFile(r.pidFile, process); err != nil {
			r.terminate(process)
			r.destroy()
			return -1, err
		}
	}
	if r.detach || r.create {
		return 0, nil
	}

	//event loop 循环等待各种信号处理
	status, err := handler.forward(process)
	if err != nil {
		r.terminate(process)
	}
	r.destroy()
	return status, err
}

func (r *runner) destroy() {
	if r.shouldDestroy {
		destroy(r.container)
	}
}

func (r *runner) terminate(p *libcontainer.Process) {
	p.Signal(syscall.SIGKILL)
	p.Wait()
}

func validateProcessSpec(spec *specs.Process) error {
	if spec.Cwd == "" {
		return fmt.Errorf("Cwd property must not be empty")
	}
	if !filepath.IsAbs(spec.Cwd) {
		return fmt.Errorf("Cwd must be an absolute path")
	}
	if len(spec.Args) == 0 {
		return fmt.Errorf("args must not be empty")
	}
	return nil
}

func startContainer(context *cli.Context, spec *specs.Spec, create bool) (int, error) {
	//获取容器名
	id := context.Args().First()
	if id == "" {
		return -1, errEmptyID
	}

	//创建容器  获取 linuxContainer 结构
	container, err := createContainer(context, id, spec)
	if err != nil {
		return -1, err
	}
	// Support on-demand socket activation by passing file descriptors into the container init process.
	listenFDs := []*os.File{}
	if os.Getenv("LISTEN_FDS") != "" {
		listenFDs = activation.Files(false)
	}
	r := &runner{
		enableSubreaper: !context.Bool("no-subreaper"),
		shouldDestroy:   true,
		container:       container,
		listenFDs:       listenFDs,
		console:         context.String("console"),
		detach:          context.Bool("detach"),
		pidFile:         context.String("pid-file"),
		//runc create为ture  runc run为false
		create:          create,
	}
	return r.run(&spec.Process)
}
