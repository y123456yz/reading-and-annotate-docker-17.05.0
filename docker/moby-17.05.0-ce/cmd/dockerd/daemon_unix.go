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
func (cli *DaemonCli) setupConfigReloadTrap() {//  设置一个系统调用重新加载配置
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)
	go func() {
		for range c {
			cli.reloadConfig()
		}
	}()
}

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

	fmt.Printf("yang test ... getPlatformRemoteOptions, opts:%v", opts)
	fmt.Printf("yang test ... getPlatformRemoteOptions, opts:%+v", opts)
	return opts
}

// getLibcontainerdRoot gets the root directory for libcontainerd/containerd to
// store their state.
/*
只有当容器在运行的时候，目录/run/docker/libcontainerd/967438113fba0b7a3005bcb6efae6a77055d6be53945f30389888802ea8b0368才
存在，容器停止执行后该目录会被删除掉，下一次启动的时候会再次被创建。  参考https://segmentfault.com/a/1190000010057763
*/
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
