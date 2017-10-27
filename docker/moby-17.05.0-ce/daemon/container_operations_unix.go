// +build linux freebsd

package daemon

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/container"
	"github.com/docker/docker/daemon/links"
	"github.com/docker/docker/pkg/idtools"
	"github.com/docker/docker/pkg/mount"
	"github.com/docker/docker/pkg/stringid"
	"github.com/docker/docker/runconfig"
	"github.com/docker/libnetwork"
	"github.com/opencontainers/runc/libcontainer/label"
	"github.com/pkg/errors"
)

//container.setupLinkedContainers() 将通过--link相连的容器中的信息获取过来，然后将其中的信息转成环境变量(是[]string数组的形式，每一个元素类似于"NAME=xxxx")的形式返回；
func (daemon *Daemon) setupLinkedContainers(container *container.Container) ([]string, error) {
	var env []string
	children := daemon.children(container)

	bridgeSettings := container.NetworkSettings.Networks[runconfig.DefaultDaemonNetworkMode().NetworkName()]
	if bridgeSettings == nil || bridgeSettings.EndpointSettings == nil {
		return nil, nil
	}

	for linkAlias, child := range children {
		if !child.IsRunning() {
			return nil, fmt.Errorf("Cannot link to a non running container: %s AS %s", child.Name, linkAlias)
		}

		childBridgeSettings := child.NetworkSettings.Networks[runconfig.DefaultDaemonNetworkMode().NetworkName()]
		if childBridgeSettings == nil || childBridgeSettings.EndpointSettings == nil {
			return nil, fmt.Errorf("container %s not attached to default bridge network", child.ID)
		}

		link := links.NewLink(
			bridgeSettings.IPAddress,
			childBridgeSettings.IPAddress,
			linkAlias,
			child.Config.Env,
			child.Config.ExposedPorts,
		)

		env = append(env, link.ToEnv()...)
	}

	return env, nil
}

func (daemon *Daemon) getIpcContainer(container *container.Container) (*container.Container, error) {
	containerID := container.HostConfig.IpcMode.Container()
	container, err := daemon.GetContainer(containerID)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot join IPC of a non running container: %s", container.ID)
	}
	return container, daemon.checkContainer(container, containerIsRunning, containerIsNotRestarting)
}

func (daemon *Daemon) getPidContainer(container *container.Container) (*container.Container, error) {
	containerID := container.HostConfig.PidMode.Container()
	container, err := daemon.GetContainer(containerID)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot join PID of a non running container: %s", container.ID)
	}
	return container, daemon.checkContainer(container, containerIsRunning, containerIsNotRestarting)
}

func containerIsRunning(c *container.Container) error {
	if !c.IsRunning() {
		return errors.Errorf("container %s is not running", c.ID)
	}
	return nil
}

func containerIsNotRestarting(c *container.Container) error {
	if c.IsRestarting() {
		return errContainerIsRestarting(c.ID)
	}
	return nil
}

func (daemon *Daemon) setupIpcDirs(c *container.Container) error {
	var err error

	c.ShmPath, err = c.ShmResourcePath()
	if err != nil {
		return err
	}

	if c.HostConfig.IpcMode.IsContainer() {
		ic, err := daemon.getIpcContainer(c)
		if err != nil {
			return err
		}
		c.ShmPath = ic.ShmPath
	} else if c.HostConfig.IpcMode.IsHost() {
		if _, err := os.Stat("/dev/shm"); err != nil {
			return fmt.Errorf("/dev/shm is not mounted, but must be for --ipc=host")
		}
		c.ShmPath = "/dev/shm"
	} else {
		rootUID, rootGID := daemon.GetRemappedUIDGID()
		if !c.HasMountFor("/dev/shm") {
			shmPath, err := c.ShmResourcePath()
			if err != nil {
				return err
			}

			if err := idtools.MkdirAllAs(shmPath, 0700, rootUID, rootGID); err != nil {
				return err
			}

			shmSize := int64(daemon.configStore.ShmSize)
			if c.HostConfig.ShmSize != 0 {
				shmSize = c.HostConfig.ShmSize
			}
			shmproperty := "mode=1777,size=" + strconv.FormatInt(shmSize, 10)
			/*
			shm：为容器分配的一个内存文件系统，后面会绑定到容器中的/dev/shm目录，可以由docker create的参数--shm-size控制其大小，默认是64M，
			其本质上就是一个挂载到/dev/shm的tmpfs，由于这个目录的内容是放在内存中的，所以读写速度快，有些程序会利用这个特点而用到这个目录，
			所以docker事先为容器准备好这个目录。
			*/
			if err := syscall.Mount("shm", shmPath, "tmpfs", uintptr(syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV), label.FormatMountLabel(shmproperty, c.GetMountLabel())); err != nil {
				return fmt.Errorf("mounting shm tmpfs: %s", err)
			}
			if err := os.Chown(shmPath, rootUID, rootGID); err != nil {
				return err
			}
		}

	}

	return nil
}

func (daemon *Daemon) setupSecretDir(c *container.Container) (setupErr error) {
	if len(c.SecretReferences) == 0 {
		return nil
	}

	localMountPath := c.SecretMountPath()
	logrus.Debugf("secrets: setting up secret dir: %s", localMountPath)

	defer func() {
		if setupErr != nil {
			// cleanup
			_ = detachMounted(localMountPath)

			if err := os.RemoveAll(localMountPath); err != nil {
				logrus.Errorf("error cleaning up secret mount: %s", err)
			}
		}
	}()

	// retrieve possible remapped range start for root UID, GID
	rootUID, rootGID := daemon.GetRemappedUIDGID()
	// create tmpfs
	if err := idtools.MkdirAllAs(localMountPath, 0700, rootUID, rootGID); err != nil {
		return errors.Wrap(err, "error creating secret local mount path")
	}
	tmpfsOwnership := fmt.Sprintf("uid=%d,gid=%d", rootUID, rootGID)
	if err := mount.Mount("tmpfs", localMountPath, "tmpfs", "nodev,nosuid,noexec,"+tmpfsOwnership); err != nil {
		return errors.Wrap(err, "unable to setup secret mount")
	}

	for _, s := range c.SecretReferences {
		if c.SecretStore == nil {
			return fmt.Errorf("secret store is not initialized")
		}

		// TODO (ehazlett): use type switch when more are supported
		if s.File == nil {
			return fmt.Errorf("secret target type is not a file target")
		}

		targetPath := filepath.Clean(s.File.Name)
		// ensure that the target is a filename only; no paths allowed
		if targetPath != filepath.Base(targetPath) {
			return fmt.Errorf("error creating secret: secret must not be a path")
		}

		fPath := filepath.Join(localMountPath, targetPath)
		if err := idtools.MkdirAllAs(filepath.Dir(fPath), 0700, rootUID, rootGID); err != nil {
			return errors.Wrap(err, "error creating secret mount path")
		}

		logrus.WithFields(logrus.Fields{
			"name": s.File.Name,
			"path": fPath,
		}).Debug("injecting secret")
		secret := c.SecretStore.Get(s.SecretID)
		if secret == nil {
			return fmt.Errorf("unable to get secret from secret store")
		}
		if err := ioutil.WriteFile(fPath, secret.Spec.Data, s.File.Mode); err != nil {
			return errors.Wrap(err, "error injecting secret")
		}

		uid, err := strconv.Atoi(s.File.UID)
		if err != nil {
			return err
		}
		gid, err := strconv.Atoi(s.File.GID)
		if err != nil {
			return err
		}

		if err := os.Chown(fPath, rootUID+uid, rootGID+gid); err != nil {
			return errors.Wrap(err, "error setting ownership for secret")
		}
	}

	label.Relabel(localMountPath, c.MountLabel, false)

	// remount secrets ro
	if err := mount.Mount("tmpfs", localMountPath, "tmpfs", "remount,ro,"+tmpfsOwnership); err != nil {
		return errors.Wrap(err, "unable to remount secret dir as readonly")
	}

	return nil
}

func killProcessDirectly(container *container.Container) error {
	if _, err := container.WaitStop(10 * time.Second); err != nil {
		// Ensure that we don't kill ourselves
		if pid := container.GetPID(); pid != 0 {
			logrus.Infof("Container %s failed to exit within 10 seconds of kill - trying direct SIGKILL", stringid.TruncateID(container.ID))
			if err := syscall.Kill(pid, 9); err != nil {
				if err != syscall.ESRCH {
					return err
				}
				e := errNoSuchProcess{pid, 9}
				logrus.Debug(e)
				return e
			}
		}
	}
	return nil
}

func detachMounted(path string) error {
	return syscall.Unmount(path, syscall.MNT_DETACH)
}

func isLinkable(child *container.Container) bool {
	// A container is linkable only if it belongs to the default network
	_, ok := child.NetworkSettings.Networks[runconfig.DefaultDaemonNetworkMode().NetworkName()]
	return ok
}

func enableIPOnPredefinedNetwork() bool {
	return false
}

func (daemon *Daemon) isNetworkHotPluggable() bool {
	return true
}

func setupPathsAndSandboxOptions(container *container.Container, sboxOptions *[]libnetwork.SandboxOption) error {
	var err error

	container.HostsPath, err = container.GetRootResourcePath("hosts")
	if err != nil {
		return err
	}
	*sboxOptions = append(*sboxOptions, libnetwork.OptionHostsPath(container.HostsPath))

	/*
	resolv.conf：里面包含了DNS服务器的IP，来自于hostconfig.json，由docker create命令的--dns参数指定，没有指定的话，
	docker会根据容器的网络类型生成一个默认的，一般是主机配置的DNS服务器或者是docker bridge的IP。
	*/
	container.ResolvConfPath, err = container.GetRootResourcePath("resolv.conf")
	if err != nil {
		return err
	}
	*sboxOptions = append(*sboxOptions, libnetwork.OptionResolvConfPath(container.ResolvConfPath))
	return nil
}

func initializeNetworkingPaths(container *container.Container, nc *container.Container) {
	container.HostnamePath = nc.HostnamePath
	container.HostsPath = nc.HostsPath
	container.ResolvConfPath = nc.ResolvConfPath
}
