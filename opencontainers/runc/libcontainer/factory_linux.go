// +build linux

package libcontainer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"syscall"

	"github.com/docker/docker/pkg/mount"
	"github.com/opencontainers/runc/libcontainer/cgroups"
	"github.com/opencontainers/runc/libcontainer/cgroups/fs"
	"github.com/opencontainers/runc/libcontainer/cgroups/systemd"
	"github.com/opencontainers/runc/libcontainer/configs"
	"github.com/opencontainers/runc/libcontainer/configs/validate"
	"github.com/opencontainers/runc/libcontainer/utils"
)

const (
	stateFilename    = "state.json"
	execFifoFilename = "exec.fifo"
)

var (
	idRegex  = regexp.MustCompile(`^[\w+-\.]+$`)
	maxIdLen = 1024
)

// InitArgs returns an options func to configure a LinuxFactory with the
// provided init binary path and arguments.
func InitArgs(args ...string) func(*LinuxFactory) error {
	return func(l *LinuxFactory) error {
		l.InitArgs = args
		return nil
	}
}

// SystemdCgroups is an options func to configure a LinuxFactory to return
// containers that use systemd to create and manage cgroups.
func SystemdCgroups(l *LinuxFactory) error {
	l.NewCgroupsManager = func(config *configs.Cgroup, paths map[string]string) cgroups.Manager {
		return &systemd.Manager{
			Cgroups: config,
			Paths:   paths,
		}
	}
	return nil
}

// Cgroupfs is an options func to configure a LinuxFactory to return
// containers that use the native cgroups filesystem implementation to
// create and manage cgroups.
func Cgroupfs(l *LinuxFactory) error {
	l.NewCgroupsManager = func(config *configs.Cgroup, paths map[string]string) cgroups.Manager {
		return &fs.Manager{
			Cgroups: config,
			Paths:   paths,
		}
	}
	return nil
}

// TmpfsRoot is an option func to mount LinuxFactory.Root to tmpfs.
func TmpfsRoot(l *LinuxFactory) error {
	mounted, err := mount.Mounted(l.Root)
	if err != nil {
		return err
	}
	if !mounted {
		if err := syscall.Mount("tmpfs", l.Root, "tmpfs", 0, ""); err != nil {
			return err
		}
	}
	return nil
}

// CriuPath returns an option func to configure a LinuxFactory with the
// provided criupath  loadFactory 中执行
func CriuPath(criupath string) func(*LinuxFactory) error {
	return func(l *LinuxFactory) error {
		l.CriuPath = criupath
		return nil
	}
}

// New returns a linux based container factory based in the root directory and
// configures the factory with the provided option funcs.
//loadFactory 中调用该函数  返回 LinuxFactory 结构      initCommand 也会调用该函数
func New(root string, options ...func(*LinuxFactory) error) (Factory, error) {
	if root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			return nil, newGenericError(err, SystemError)
		}
	}
	l := &LinuxFactory{
		Root:      root,  //默认/run/runc
		// 参考 (p *initProcess) start()，在该函数中执行本代码中 initCommand 对应的程序
		InitArgs:  []string{"/proc/self/exe", "init"},
		Validator: validate.New(),
		CriuPath:  "criu",
	}
	Cgroupfs(l)
	for _, opt := range options {
		if err := opt(l); err != nil {
			return nil, err
		}
	}
	return l, nil
}

// LinuxFactory implements the default factory interface for linux based systems.
//loadFactory->libcontainer.new中构造该结构
type LinuxFactory struct {
	// Root directory for the factory to store state.
	Root string //默认/run/runc

	// InitArgs are arguments for calling the init responsibilities for spawning
	// a container.   []string{"/proc/self/exe", "init"},
	InitArgs []string

	// CriuPath is the path to the criu binary used for checkpoint and restore of
	// containers.
	//热迁移相关，可以参考http://www.infoq.com/cn/articles/docker-container-management-libcontainer-depth-analysis
	//criu 配置指定，赋值见loadFactory
	CriuPath string

	// Validator provides validation to container configurations.
	Validator validate.Validator //ConfigValidator

	// NewCgroupsManager returns an initialized cgroups manager for a single container.
	//见 Cgroupfs
	NewCgroupsManager func(config *configs.Cgroup, paths map[string]string) cgroups.Manager
}

//首先对id和config进行验证，接着获取uid, gid, containerRoot并且创建containerRoot(默认为/run/runc/container-id)。
// 之后再创建一个FIFO文件，填充一个 linuxContainer 对象     createContainer 中执行该函数接口
func (l *LinuxFactory) Create(id string, config *configs.Config) (Container, error) {
	if l.Root == "" {
		return nil, newGenericError(fmt.Errorf("invalid root"), ConfigInvalid)
	}

	if err := l.validateID(id); err != nil {
		return nil, err
	}

	//检查config内容是否合规
	if err := l.Validator.Validate(config); err != nil {
		return nil, newGenericError(err, ConfigInvalid)
	}
	uid, err := config.HostUID()
	if err != nil {
		return nil, newGenericError(err, SystemError)
	}
	gid, err := config.HostGID()
	if err != nil {
		return nil, newGenericError(err, SystemError)
	}
	///run/runc/9be24974a6a7cf064f4a238f70260b13b15359248b3267602bfc49e00f13d670/
	containerRoot := filepath.Join(l.Root, id)

	if _, err := os.Stat(containerRoot); err == nil { //已经创建过了
		return nil, newGenericError(fmt.Errorf("container with id exists: %v", id), IdInUse)
	} else if !os.IsNotExist(err) {
		return nil, newGenericError(err, SystemError)
	}

	//创建/run/runc/$containerID
	if err := os.MkdirAll(containerRoot, 0711); err != nil {
		return nil, newGenericError(err, SystemError)
	}

	//设置用户组
	if err := os.Chown(containerRoot, uid, gid); err != nil {
		return nil, newGenericError(err, SystemError)
	}

	//建立实名管道"exec.fifo"
	fifoName := filepath.Join(containerRoot, execFifoFilename)
	oldMask := syscall.Umask(0000)
	if err := syscall.Mkfifo(fifoName, 0622); err != nil {
		syscall.Umask(oldMask)
		return nil, newGenericError(err, SystemError)
	}
	syscall.Umask(oldMask)

	if err := os.Chown(fifoName, uid, gid); err != nil {
		return nil, newGenericError(err, SystemError)
	}
	c := &linuxContainer{
		id:            id,
		root:          containerRoot,
		config:        config,
		initArgs:      l.InitArgs,
		criuPath:      l.CriuPath,
		cgroupManager: l.NewCgroupsManager(config.Cgroups, nil),
	}
	c.state = &stoppedState{c: c}
	return c, nil
}

func (l *LinuxFactory) Load(id string) (Container, error) {
	if l.Root == "" {
		return nil, newGenericError(fmt.Errorf("invalid root"), ConfigInvalid)
	}
	containerRoot := filepath.Join(l.Root, id)
	state, err := l.loadState(containerRoot, id)
	if err != nil {
		return nil, err
	}
	r := &nonChildProcess{
		processPid:       state.InitProcessPid,
		processStartTime: state.InitProcessStartTime,
		fds:              state.ExternalDescriptors,
	}
	c := &linuxContainer{
		initProcess:          r,
		initProcessStartTime: state.InitProcessStartTime,
		id:                   id,
		config:               &state.Config,
		initArgs:             l.InitArgs,
		criuPath:             l.CriuPath,
		cgroupManager:        l.NewCgroupsManager(state.Config.Cgroups, state.CgroupPaths),
		root:                 containerRoot,
		created:              state.Created,
	}
	c.state = &loadedState{c: c}
	if err := c.refreshState(); err != nil {
		return nil, err
	}
	return c, nil
}

func (l *LinuxFactory) Type() string {
	return "libcontainer"
}

// StartInitialization loads a container by opening the pipe fd from the parent to read the configuration and state
// This is a low level implementation detail of the reexec and should not be consumed externally
//initCommand 中执行
func (l *LinuxFactory) StartInitialization() (err error) {
	var pipefd, rootfd int

	//从"_LIBCONTAINER_INITPIPE"等环境变量中获取pipefd, rootfd(/run/runc/container-id),
	for _, pair := range []struct {
		k string
		v *int
	} {     //获取环境变量对应的值
		// commandTemplate 和 StartInitialization(init进程) 对应，本进程和init进程通过这两个管道通信
		{"_LIBCONTAINER_INITPIPE", &pipefd}, //对应 childPipe
		{"_LIBCONTAINER_STATEDIR", &rootfd}, //对应 rootDir，也就是 /run/runc/container-id的fd
	} {

		s := os.Getenv(pair.k)

		i, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("unable to convert %s=%s to int", pair.k, s)
		}
		*pair.v = i
	}
	var (
		pipe = os.NewFile(uintptr(pipefd), "pipe")
		it   = initType(os.Getenv("_LIBCONTAINER_INITTYPE"))
	)
	// clear the current process's environment to clean any libcontainer
	// specific env vars.
	os.Clearenv()

	var i initer
	defer func() {
		// We have an error during the initialization of the container's init,
		// send it back to the parent process in the form of an initError.
		// If container's init successed, syscall.Exec will not return, hence
		// this defer function will never be called.
		if _, ok := i.(*linuxStandardInit); ok {
			//  Synchronisation only necessary for standard init.
			if werr := utils.WriteJSON(pipe, syncT{procError}); werr != nil {
				fmt.Fprintln(os.Stderr, err)
				return
			}
		}
		if werr := utils.WriteJSON(pipe, newSystemError(err)); werr != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		// ensure that this pipe is always closed
		pipe.Close()
	}()
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("panic from initialization: %v, %v", e, string(debug.Stack()))
		}
	}()

	//返回 linuxStandardInit 或者 linuxStandardInit
	i, err = newContainerInit(it, pipe, rootfd)
	if err != nil {
		return err
	}

	// (l *linuxStandardInit) Init()
	return i.Init()
}

func (l *LinuxFactory) loadState(root, id string) (*State, error) {
	f, err := os.Open(filepath.Join(root, stateFilename))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, newGenericError(fmt.Errorf("container %q does not exist", id), ContainerNotExists)
		}
		return nil, newGenericError(err, SystemError)
	}
	defer f.Close()
	var state *State
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return nil, newGenericError(err, SystemError)
	}
	return state, nil
}

//id字符串是否合法
func (l *LinuxFactory) validateID(id string) error {
	if !idRegex.MatchString(id) {
		return newGenericError(fmt.Errorf("invalid id format: %v", id), InvalidIdFormat)
	}
	if len(id) > maxIdLen {
		return newGenericError(fmt.Errorf("invalid id format: %v", id), InvalidIdFormat)
	}
	return nil
}
