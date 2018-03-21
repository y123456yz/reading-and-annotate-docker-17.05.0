// +build !windows,!solaris

package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/docker/docker/cmd/dockerd/hack"
	"github.com/docker/docker/daemon"
	"github.com/docker/docker/libcontainerd"
	"github.com/docker/docker/pkg/system"
	"github.com/docker/libnetwork/portallocator"
)

//默认配置文件
const defaultDaemonConfigFile = "/etc/docker/daemon.json"

// currentUserIsOwner checks whether the current user is the owner of the given
// file.
func currentUserIsOwner(f string) bool {
	if fileInfo, err := system.Stat(f); err == nil && fileInfo != nil {
		if int(fileInfo.UID()) == os.Getuid() {
			return true
		}
	}
	return false
}

// setDefaultUmask sets the umask to 0022 to avoid problems
// caused by custom umask
func setDefaultUmask() error {
	desiredUmask := 0022
	syscall.Umask(desiredUmask)
	if umask := syscall.Umask(desiredUmask); umask != desiredUmask {
		return fmt.Errorf("failed to set umask: expected %#o, got %#o", desiredUmask, umask)
	}

	return nil
}

func getDaemonConfDir(_ string) string {
	return "/etc/docker"
}

// setupConfigReloadTrap configures the USR2 signal to reload the configuration.
//重新加载配置文件
func (cli *DaemonCli) setupConfigReloadTrap() {//  设置一个系统调用重新加载配置
	c := make(chan os.Signal, 1)
	//该函数会将进程收到的系统Signal转发给channel c。转发哪些信号由该函数的可变参数决定，如果你没有传入sig参数，
	// 那么Notify会将系统收到的所有信号转发给c。
	// kill -HUP dockerd-pid 重新加载配置
	signal.Notify(c, syscall.SIGHUP)
	go func() {
		for range c {
			cli.reloadConfig()
		}
	}()
}

//// (cli *DaemonCli) start 中执行，实际上就是根据命令行配置参数给 remote_unix.go 中的 remote 结构赋值
// getPlatformRemoteOptions   和remote_unix.go中的New，配合阅读
func (cli *DaemonCli) getPlatformRemoteOptions() []libcontainerd.RemoteOption {
	opts := []libcontainerd.RemoteOption{
		libcontainerd.WithDebugLog(cli.Config.Debug),
		libcontainerd.WithOOMScore(cli.Config.OOMScoreAdjust),
	}
	if cli.Config.ContainerdAddr != "" {
		opts = append(opts, libcontainerd.WithRemoteAddr(cli.Config.ContainerdAddr))
	} else {
		opts = append(opts, libcontainerd.WithStartDaemon(true))
	}
	if daemon.UsingSystemd(cli.Config) {
		args := []string{"--systemd-cgroup=true"}
		opts = append(opts, libcontainerd.WithRuntimeArgs(args))
	}

	if cli.Config.LiveRestoreEnabled {
		opts = append(opts, libcontainerd.WithLiveRestore(true))
	}
	opts = append(opts, libcontainerd.WithRuntimePath(daemon.DefaultRuntimeBinary))

	//yang test ... getPlatformRemoteOptions, opts:[true -500 true docker-runc]
	// yang test ... getPlatformRemoteOptions, opts:[true -500 true docker-runc]
	//fmt.Printf("yang test ... getPlatformRemoteOptions, opts:%v", opts)
	//fmt.Printf("yang test ... getPlatformRemoteOptions, opts:%+v", opts)
	return opts
}

func (cli *DaemonCli) getPlatformLxcfsRemoteOptions() []libcontainerd.RemoteOption {
	opts := []libcontainerd.RemoteOption{
		libcontainerd.LxcfsWithDebugLog(cli.Config.LxcfsDebug),
		libcontainerd.LxcfsWithAllowOther(cli.Config.LxcfsAllowOther),
		libcontainerd.LxcfsWithOffMultithread(cli.Config.LxcfsOffMultithread),
		libcontainerd.LxcfsWithOOMScore(cli.Config.OOMScoreAdjust),
	}

	if cli.Config.LxcfsAddr != "" {
		opts = append(opts, libcontainerd.LxcfsWithRemoteAddr(cli.Config.LxcfsAddr))
	}

	if cli.Config.LxcfsLogPath != "" {
		opts = append(opts, libcontainerd.LxcfsWithLogPath(cli.Config.LxcfsLogPath))
	}

	if cli.Config.LxcfsMountPath != "" {
		opts = append(opts, libcontainerd.LxcfsWithMountPath(cli.Config.LxcfsMountPath))
	}

	return opts
}


// getLibcontainerdRoot gets the root directory for libcontainerd/containerd to
// store their state.
/*
只有当容器在运行的时候，目录/run/docker/libcontainerd/967438113fba0b7a3005bcb6efae6a77055d6be53945f30389888802ea8b0368才
存在，容器停止执行后该目录会被删除掉，下一次启动的时候会再次被创建。  参考https://segmentfault.com/a/1190000010057763
*/
// (cli *DaemonCli) start 中执行
func (cli *DaemonCli) getLibcontainerdRoot() string {
	return filepath.Join(cli.Config.ExecRoot, "libcontainerd")
}

// getSwarmRunRoot gets the root directory for swarm to store runtime state
// For example, the control socket
func (cli *DaemonCli) getSwarmRunRoot() string {
	return filepath.Join(cli.Config.ExecRoot, "swarm")
}

// allocateDaemonPort ensures that there are no containers
// that try to use any port allocated for the docker server.
func allocateDaemonPort(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}

	intPort, err := strconv.Atoi(port)
	if err != nil {
		return err
	}

	var hostIPs []net.IP
	if parsedIP := net.ParseIP(host); parsedIP != nil {
		hostIPs = append(hostIPs, parsedIP)
	} else if hostIPs, err = net.LookupIP(host); err != nil {
		return fmt.Errorf("failed to lookup %s address in host specification", host)
	}

	pa := portallocator.Get()
	for _, hostIP := range hostIPs {
		if _, err := pa.RequestPort(hostIP, "tcp", intPort); err != nil {
			return fmt.Errorf("failed to allocate daemon listening port %d (err: %v)", intPort, err)
		}
	}
	return nil
}

// notifyShutdown is called after the daemon shuts down but before the process exits.
func notifyShutdown(err error) {
}

func wrapListeners(proto string, ls []net.Listener) []net.Listener {
	switch proto {
	case "unix":
		ls[0] = &hack.MalformedHostHeaderOverride{ls[0]}
	case "fd":
		for i := range ls {
			ls[i] = &hack.MalformedHostHeaderOverride{ls[i]}
		}
	}
	return ls
}
