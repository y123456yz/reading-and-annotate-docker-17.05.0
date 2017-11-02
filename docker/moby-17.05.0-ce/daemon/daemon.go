// Package daemon exposes the functions that occur on the host server
// that the Docker daemon is running.
//
// In implementing the various functions of the daemon, there is often
// a method-specific struct for configuring the runtime behavior.
package daemon

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	containerd "github.com/docker/containerd/api/grpc/types"
	"github.com/docker/docker/api"
	"github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/container"
	"github.com/docker/docker/daemon/config"
	"github.com/docker/docker/daemon/discovery"
	"github.com/docker/docker/daemon/events"
	"github.com/docker/docker/daemon/exec"
	"github.com/docker/docker/daemon/logger"
	// register graph drivers
	_ "github.com/docker/docker/daemon/graphdriver/register"
	"github.com/docker/docker/daemon/initlayer"
	"github.com/docker/docker/daemon/stats"
	dmetadata "github.com/docker/docker/distribution/metadata"
	"github.com/docker/docker/distribution/xfer"
	"github.com/docker/docker/dockerversion"
	"github.com/docker/docker/image"
	"github.com/docker/docker/layer"
	"github.com/docker/docker/libcontainerd"
	"github.com/docker/docker/migrate/v1"
	"github.com/docker/docker/pkg/fileutils"
	"github.com/docker/docker/pkg/idtools"
	"github.com/docker/docker/pkg/plugingetter"
	"github.com/docker/docker/pkg/registrar"
	"github.com/docker/docker/pkg/signal"
	"github.com/docker/docker/pkg/sysinfo"
	"github.com/docker/docker/pkg/system"
	"github.com/docker/docker/pkg/truncindex"
	"github.com/docker/docker/plugin"
	refstore "github.com/docker/docker/reference"
	"github.com/docker/docker/registry"
	"github.com/docker/docker/runconfig"
	volumedrivers "github.com/docker/docker/volume/drivers"
	"github.com/docker/docker/volume/local"
	"github.com/docker/docker/volume/store"
	"github.com/docker/libnetwork"
	"github.com/docker/libnetwork/cluster"
	nwconfig "github.com/docker/libnetwork/config"
	"github.com/docker/libtrust"
	"github.com/pkg/errors"
)

var (
	// DefaultRuntimeBinary is the default runtime to be used by
	// containerd if none is specified
	DefaultRuntimeBinary = "docker-runc"

	errSystemNotSupported = errors.New("The Docker daemon is not supported on this platform.")
)

// Daemon holds information about the Docker daemon.
type Daemon struct { //赋值见NewDaemon 见 NewDaemon
	//根据传入的证书生成的容器ID，若没有传入则自动使用ECDSA加密算法生成
	ID                        string
	//部署所有docker容器的路径，当dockerd重启的时候，会去查看在daemon.repository也就是在/var/lib/docker/container
	//中的内容。若有已经存在的docker容器，则将相应的信息收集并进行维护，同时重启restart policy为always的容器
	repository                string   //  /var/lib/docker/containers    赋值见NewDaemon
	//用于存储具体docker容器信息的对象
	containers                container.Store
	//docker容器所执行的命令
	execCommands              *exec.Store
	//存储docker容器仓库名和镜像ID的映射
	referenceStore            refstore.Store
	downloadManager           *xfer.LayerDownloadManager
	uploadManager             *xfer.LayerUploadManager
	//V2版registry相关的元数据存储
	distributionMetadataStore dmetadata.Store
	//可信任证书
	trustKey                  libtrust.PrivateKey
	//用于通过简短有效的字符串前缀定位唯一的镜像
	idIndex                   *truncindex.TruncIndex
	//docker所需要的配置信息
	configStore               *config.Config
	//收集容器网络及cgroup状态信息
	statsCollector            *stats.Collector
	//提供日志的默认配置信息
	defaultLogConfig          containertypes.LogConfig
	//镜像存储服务相关信息
	RegistryService           registry.Service
	//事件服务相关信息
	EventsService             *events.Events
	netController             libnetwork.NetworkController
	//volume所使用的驱动，默认为local类型
	volumes                   *store.VolumeStore
	discoveryWatcher          discovery.Reloader
	//docker运行的工作跟目录
	root                      string
	//释放使用secompute
	seccompEnabled            bool
	apparmorEnabled           bool
	shutdown                  bool
	uidMaps                   []idtools.IDMap
	gidMaps                   []idtools.IDMap
	//赋值见 NewDaemon   通过NewStoreFromOptions返回  实际上为 layerStore 类型，实现有type Store interface {}中包含的函数
	//layerStore 存储相关的接口方法，结构，源头都在这里
	layerStore                layer.Store
	//Store 存储相关的接口方法，结构，源头都在这里
	imageStore                image.Store   //赋值见 NewDaemon  Daemon.imageStore
	PluginStore               *plugin.Store // todo: remove
	pluginManager             *plugin.Manager
	//记录键和其名字的对应关系  存放容器名和ID的KV对数组，赋值见 reserveName
	nameIndex                 *registrar.Registrar
	//容器的link目录，记录容器的link关系
	linkIndex                 *linkIndex
	containerd                libcontainerd.Client
	containerdRemote          libcontainerd.Remote
	defaultIsolation          containertypes.Isolation // Default isolation mode on Windows
	clusterProvider           cluster.Provider
	cluster                   Cluster

	machineMemory uint64

	seccompProfile     []byte
	seccompProfilePath string
}

// HasExperimental returns whether the experimental features of the daemon are enabled or not
func (daemon *Daemon) HasExperimental() bool {
	if daemon.configStore != nil && daemon.configStore.Experimental {
		return true
	}
	return false
}

func (daemon *Daemon) restore() error {
	var (
		currentDriver = daemon.GraphDriverName()
		containers    = make(map[string]*container.Container)
	)

	logrus.Info("Loading containers: start.")

	dir, err := ioutil.ReadDir(daemon.repository)
	if err != nil {
		return err
	}

	for _, v := range dir {
		id := v.Name()
		container, err := daemon.load(id)
		if err != nil {
			logrus.Errorf("Failed to load container %v: %v", id, err)
			continue
		}

		// Ignore the container if it does not support the current driver being used by the graph
		if (container.Driver == "" && currentDriver == "aufs") || container.Driver == currentDriver {
			rwlayer, err := daemon.layerStore.GetRWLayer(container.ID)
			if err != nil {
				logrus.Errorf("Failed to load container mount %v: %v", id, err)
				continue
			}
			container.RWLayer = rwlayer
			logrus.Debugf("Loaded container %v", container.ID)

			containers[container.ID] = container
		} else {
			logrus.Debugf("Cannot load container %s because it was created with another graph driver.", container.ID)
		}
	}

	removeContainers := make(map[string]*container.Container)
	restartContainers := make(map[*container.Container]chan struct{})
	activeSandboxes := make(map[string]interface{})
	for id, c := range containers {
		if err := daemon.registerName(c); err != nil {
			logrus.Errorf("Failed to register container %s: %s", c.ID, err)
			delete(containers, id)
			continue
		}
		daemon.Register(c)

		// verify that all volumes valid and have been migrated from the pre-1.7 layout
		if err := daemon.verifyVolumesInfo(c); err != nil {
			// don't skip the container due to error
			logrus.Errorf("Failed to verify volumes for container '%s': %v", c.ID, err)
		}

		// The LogConfig.Type is empty if the container was created before docker 1.12 with default log driver.
		// We should rewrite it to use the daemon defaults.
		// Fixes https://github.com/docker/docker/issues/22536
		if c.HostConfig.LogConfig.Type == "" {
			if err := daemon.mergeAndVerifyLogConfig(&c.HostConfig.LogConfig); err != nil {
				logrus.Errorf("Failed to verify log config for container %s: %q", c.ID, err)
				continue
			}
		}
	}

	var wg sync.WaitGroup
	var mapLock sync.Mutex
	for _, c := range containers {
		wg.Add(1)
		go func(c *container.Container) {
			defer wg.Done()
			if err := backportMountSpec(c); err != nil {
				logrus.Error("Failed to migrate old mounts to use new spec format")
			}

			if c.IsRunning() || c.IsPaused() {
				c.RestartManager().Cancel() // manually start containers because some need to wait for swarm networking
				if err := daemon.containerd.Restore(c.ID, c.InitializeStdio); err != nil {
					logrus.Errorf("Failed to restore %s with containerd: %s", c.ID, err)
					return
				}

				// we call Mount and then Unmount to get BaseFs of the container
				if err := daemon.Mount(c); err != nil {
					// The mount is unlikely to fail. However, in case mount fails
					// the container should be allowed to restore here. Some functionalities
					// (like docker exec -u user) might be missing but container is able to be
					// stopped/restarted/removed.
					// See #29365 for related information.
					// The error is only logged here.
					logrus.Warnf("Failed to mount container on getting BaseFs path %v: %v", c.ID, err)
				} else {
					// if mount success, then unmount it
					if err := daemon.Unmount(c); err != nil {
						logrus.Warnf("Failed to umount container on getting BaseFs path %v: %v", c.ID, err)
					}
				}

				c.ResetRestartManager(false)
				if !c.HostConfig.NetworkMode.IsContainer() && c.IsRunning() {
					options, err := daemon.buildSandboxOptions(c)
					if err != nil {
						logrus.Warnf("Failed build sandbox option to restore container %s: %v", c.ID, err)
					}
					mapLock.Lock()
					activeSandboxes[c.NetworkSettings.SandboxID] = options
					mapLock.Unlock()
				}

			}
			// fixme: only if not running
			// get list of containers we need to restart
			if !c.IsRunning() && !c.IsPaused() {
				// Do not autostart containers which
				// has endpoints in a swarm scope
				// network yet since the cluster is
				// not initialized yet. We will start
				// it after the cluster is
				// initialized.
				if daemon.configStore.AutoRestart && c.ShouldRestart() && !c.NetworkSettings.HasSwarmEndpoint {
					mapLock.Lock()
					restartContainers[c] = make(chan struct{})
					mapLock.Unlock()
				} else if c.HostConfig != nil && c.HostConfig.AutoRemove {
					mapLock.Lock()
					removeContainers[c.ID] = c
					mapLock.Unlock()
				}
			}

			if c.RemovalInProgress {
				// We probably crashed in the middle of a removal, reset
				// the flag.
				//
				// We DO NOT remove the container here as we do not
				// know if the user had requested for either the
				// associated volumes, network links or both to also
				// be removed. So we put the container in the "dead"
				// state and leave further processing up to them.
				logrus.Debugf("Resetting RemovalInProgress flag from %v", c.ID)
				c.ResetRemovalInProgress()
				c.SetDead()
				c.ToDisk()
			}
		}(c)
	}
	wg.Wait()


	daemon.netController, err = daemon.initNetworkController(daemon.configStore, activeSandboxes)
	if err != nil {
		return fmt.Errorf("Error initializing network controller: %v", err)
	}

	// Now that all the containers are registered, register the links
	for _, c := range containers {
		if err := daemon.registerLinks(c, c.HostConfig); err != nil {
			logrus.Errorf("failed to register link for container %s: %v", c.ID, err)
		}
	}

	group := sync.WaitGroup{}
	for c, notifier := range restartContainers {
		group.Add(1)

		go func(c *container.Container, chNotify chan struct{}) {
			defer group.Done()

			logrus.Debugf("Starting container %s", c.ID)

			// ignore errors here as this is a best effort to wait for children to be
			//   running before we try to start the container
			children := daemon.children(c)
			timeout := time.After(5 * time.Second)
			for _, child := range children {
				if notifier, exists := restartContainers[child]; exists {
					select {
					case <-notifier:
					case <-timeout:
					}
				}
			}

			// Make sure networks are available before starting
			daemon.waitForNetworks(c)
			if err := daemon.containerStart(c, "", "", true); err != nil {
				logrus.Errorf("Failed to start container %s: %s", c.ID, err)
			}
			close(chNotify)
		}(c, notifier)

	}
	group.Wait()

	removeGroup := sync.WaitGroup{}
	for id := range removeContainers {
		removeGroup.Add(1)
		go func(cid string) {
			if err := daemon.ContainerRm(cid, &types.ContainerRmConfig{ForceRemove: true, RemoveVolume: true}); err != nil {
				logrus.Errorf("Failed to remove container %s: %s", cid, err)
			}
			removeGroup.Done()
		}(id)
	}
	removeGroup.Wait()

	// any containers that were started above would already have had this done,
	// however we need to now prepare the mountpoints for the rest of the containers as well.
	// This shouldn't cause any issue running on the containers that already had this run.
	// This must be run after any containers with a restart policy so that containerized plugins
	// can have a chance to be running before we try to initialize them.
	for _, c := range containers {
		// if the container has restart policy, do not
		// prepare the mountpoints since it has been done on restarting.
		// This is to speed up the daemon start when a restart container
		// has a volume and the volume driver is not available.
		if _, ok := restartContainers[c]; ok {
			continue
		} else if _, ok := removeContainers[c.ID]; ok {
			// container is automatically removed, skip it.
			continue
		}

		group.Add(1)
		go func(c *container.Container) {
			defer group.Done()
			if err := daemon.prepareMountPoints(c); err != nil {
				logrus.Error(err)
			}
		}(c)
	}

	group.Wait()

	logrus.Info("Loading containers: done.")

	return nil
}

// RestartSwarmContainers restarts any autostart container which has a
// swarm endpoint.
func (daemon *Daemon) RestartSwarmContainers() {
	group := sync.WaitGroup{}
	for _, c := range daemon.List() {
		if !c.IsRunning() && !c.IsPaused() {
			// Autostart all the containers which has a
			// swarm endpoint now that the cluster is
			// initialized.
			if daemon.configStore.AutoRestart && c.ShouldRestart() && c.NetworkSettings.HasSwarmEndpoint {
				group.Add(1)
				go func(c *container.Container) {
					defer group.Done()
					if err := daemon.containerStart(c, "", "", true); err != nil {
						logrus.Error(err)
					}
				}(c)
			}
		}

	}
	group.Wait()
}

// waitForNetworks is used during daemon initialization when starting up containers
// It ensures that all of a container's networks are available before the daemon tries to start the container.
// In practice it just makes sure the discovery service is available for containers which use a network that require discovery.
func (daemon *Daemon) waitForNetworks(c *container.Container) {
	if daemon.discoveryWatcher == nil {
		return
	}
	// Make sure if the container has a network that requires discovery that the discovery service is available before starting
	for netName := range c.NetworkSettings.Networks {
		// If we get `ErrNoSuchNetwork` here, we can assume that it is due to discovery not being ready
		// Most likely this is because the K/V store used for discovery is in a container and needs to be started
		if _, err := daemon.netController.NetworkByName(netName); err != nil {
			if _, ok := err.(libnetwork.ErrNoSuchNetwork); !ok {
				continue
			}
			// use a longish timeout here due to some slowdowns in libnetwork if the k/v store is on anything other than --net=host
			// FIXME: why is this slow???
			logrus.Debugf("Container %s waiting for network to be ready", c.Name)
			select {
			case <-daemon.discoveryWatcher.ReadyCh():
			case <-time.After(60 * time.Second):
			}
			return
		}
	}
}

func (daemon *Daemon) children(c *container.Container) map[string]*container.Container {
	return daemon.linkIndex.children(c)
}

// parents returns the names of the parent containers of the container
// with the given name.
func (daemon *Daemon) parents(c *container.Container) map[string]*container.Container {
	return daemon.linkIndex.parents(c)
}

func (daemon *Daemon) registerLink(parent, child *container.Container, alias string) error {
	fullName := path.Join(parent.Name, alias)
	if err := daemon.nameIndex.Reserve(fullName, child.ID); err != nil {
		if err == registrar.ErrNameReserved {
			logrus.Warnf("error registering link for %s, to %s, as alias %s, ignoring: %v", parent.ID, child.ID, alias, err)
			return nil
		}
		return err
	}
	daemon.linkIndex.link(parent, child, fullName)
	return nil
}

// DaemonJoinsCluster informs the daemon has joined the cluster and provides
// the handler to query the cluster component
func (daemon *Daemon) DaemonJoinsCluster(clusterProvider cluster.Provider) {
	daemon.setClusterProvider(clusterProvider)
}

// DaemonLeavesCluster informs the daemon has left the cluster
func (daemon *Daemon) DaemonLeavesCluster() {
	// Daemon is in charge of removing the attachable networks with
	// connected containers when the node leaves the swarm
	daemon.clearAttachableNetworks()
	// We no longer need the cluster provider, stop it now so that
	// the network agent will stop listening to cluster events.
	daemon.setClusterProvider(nil)
	// Wait for the networking cluster agent to stop
	daemon.netController.AgentStopWait()
	// Daemon is in charge of removing the ingress network when the
	// node leaves the swarm. Wait for job to be done or timeout.
	// This is called also on graceful daemon shutdown. We need to
	// wait, because the ingress release has to happen before the
	// network controller is stopped.
	if done, err := daemon.ReleaseIngress(); err == nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			logrus.Warnf("timeout while waiting for ingress network removal")
		}
	} else {
		logrus.Warnf("failed to initiate ingress network removal: %v", err)
	}
}

// setClusterProvider sets a component for querying the current cluster state.
func (daemon *Daemon) setClusterProvider(clusterProvider cluster.Provider) {
	daemon.clusterProvider = clusterProvider
	daemon.netController.SetClusterProvider(clusterProvider)
}

// IsSwarmCompatible verifies if the current daemon
// configuration is compatible with the swarm mode
func (daemon *Daemon) IsSwarmCompatible() error {
	if daemon.configStore == nil {
		return nil
	}
	return daemon.configStore.IsSwarmCompatible()
}

// NewDaemon sets up everything for the daemon to be able to service
// requests from the webserver.
//dockerd\daemon.go中start函数和daemon\daemon.go中的NewDaemon是理解的主线
//dockerd\daemon.go中start  func (cli *DaemonCli) start中执行该函数    各个客户端请求，daemon 对应的处理见 initRouter
func NewDaemon(config *config.Config, registryService registry.Service, containerdRemote libcontainerd.Remote, pluginStore *plugin.Store) (daemon *Daemon, err error) {
	setDefaultMtu(config)//  设置默认MTU 1500

	// Ensure that we have a correct root key limit for launching containers.
	if err := ModifyRootKeyLimit(); err != nil {//  "/proc/sys/kernel/keys/root_maxkeys"  key权限不足的话修改权限
		logrus.Warnf("unable to modify root key limit, number of containers could be limited by this quota: %v", err)
	}

	//检查是否出现有冲突的配置，主要有(1)config.Bridge.Iface 与 config.Bridge.IP 这两项配置不能都有，设置一个就可；
	//(2) config.Bridge.EnableIPTables 与 config.Bridge.InterContainerCommunication，后者是icc，表示docker容器间是否可以互相通信，
	// icc是通过iptables来实现的，即在iptables的FORWARD链中增加规则，所以不能在两者同时为false，但不清楚icc＝true，但EntableIPTables为false的时候会怎么样；
	// (3) 当config.Bridge.EnableIPTables为false时，config.Bridge.EnableIPMasq，ip伪装功能不能为true，和(2) 一样，因为ip伪装是通过iptables来实现的；
	// Ensure we have compatible and valid configuration options
	if err := verifyDaemonSettings(config); err != nil {//   检查配置是否正确，不正确退出
		return nil, err
	}

	// Do we have a disabled network?
	// 判断config.Bridge.Iface 与 disableNetworkBridge是否相等，犹豫Iface默认值为空，disableNetworkBridge默认值为none，所以这个config.DisableBridge  为false
	config.DisableBridge = isBridgeNetworkDisabled(config)

	// Verify the platform is supported as a daemon
	if !platformSupported {
		return nil, errSystemNotSupported
	}

	// Validate platform-specific requirements
	//checkSystem主要验证运行docker的进程是否为root，需要root权限；还有包括验证linux kernel的版本；
	if err := checkSystem(); err != nil {//   系统是否支持
		return nil, err
	}

	 /*// 根据username、groupname创建两个map*/
	uidMaps, gidMaps, err := setupRemappedRoot(config)
	if err != nil {
		return nil, err
	}


	rootUID, rootGID, err := idtools.GetRootUIDGID(uidMaps, gidMaps) // pkg/idtools/idtools.go 创建根uid、gid
	if err != nil {
		return nil, err
	}
	// linux   设置 /proc/self/oom_score_adj
	if err := setupDaemonProcess(config); err != nil {// setup the daemons oom_score_adj
		return nil, err
	}

	// set up the tmpDir to use a canonical path  //  创建/var/lib/docker/tmp文件夹
	tmp, err := prepareTempDir(config.Root, rootUID, rootGID)
	if err != nil {
		return nil, fmt.Errorf("Unable to get the TempDir under %s: %s", config.Root, err)
	}
	realTmp, err := fileutils.ReadSymlinkedDirectory(tmp)//  pkg/fileutils/fileutils.go 给tmp创建一个symlink
	if err != nil {
		return nil, fmt.Errorf("Unable to get the full path to the TempDir (%s): %s", tmp, err)
	}
	//建立temp文件的存放路径，读取环境变量DOCKER_TMPDIR值，如果没有那么直接在根路径下建立tmp子目录 ，即/var/lib/docker/tmp
	os.Setenv("TMPDIR", realTmp)

	d := &Daemon{configStore: config}  //创建个daem 实例
	// Ensure the daemon is properly shutdown if there is a failure during
	// initialization
	defer func() {  // 函数退出时 daemon 关闭
		if err != nil {
			if err := d.Shutdown(); err != nil {
				logrus.Error(err)
			}
		}
	}()

	// set up SIGUSR1 handler on Unix-like systems, or a Win32 global event
	// on Windows to dump Go routine stacks
	//获取docker运行时的根路径，后续的流程会在这个根目录下创建各种子目录，默认的是在"/var/lib/docker"路径下；
	stackDumpDir := config.Root
	if execRoot := config.GetExecRoot(); execRoot != "" {
		stackDumpDir = execRoot
	}
	d.setupDumpStackTrap(stackDumpDir) //setupDumpStackTrap// 创建处理系统信号的通道和协程

	if err := d.setupSeccompProfile(); err != nil {// 需要的话创建 seccompProfile
		return nil, err
	}

	// Set the default isolation mode (only applicable on Windows)
	if err := d.setDefaultIsolation(); err != nil { // 只是windows使用，决定 isolation mode
		return nil, fmt.Errorf("error setting default isolation mode: %v", err)
	}

	//进行log driver的配置，默认的log driver 是json-file， 在deamon/logger 目录下是各种log driver，包括： fluentd,syslogd,journald, gelf等
	logrus.Debugf("Using default logging driver %s", config.LogConfig.Type)

	if err := configureMaxThreads(config); err != nil { // linux 下面设置最大线程 /proc/sys/kernel/threads-max
		logrus.Warnf("Failed to configure golang's threads limit: %v", err)
	}

	if err := ensureDefaultAppArmorProfile(); err != nil {// 如果使能 AppArmor ，则加载
		logrus.Errorf(err.Error())
	}
	//创建容器的存储目录，在根目录下创建container子目录，/var/log/docker/container
	daemonRepo := filepath.Join(config.Root, "containers") // 创建一个containers的目录
	if err := idtools.MkdirAllAs(daemonRepo, 0700, rootUID, rootGID); err != nil && !os.IsExist(err) {
		return nil, err
	}

	if runtime.GOOS == "windows" {
		if err := system.MkdirAll(filepath.Join(config.Root, "credentialspecs"), 0); err != nil && !os.IsExist(err) {
			return nil, err
		}
	}

	driverName := os.Getenv("DOCKER_DRIVER") /* //取环境变量设置值，未设置取配置 */
	if driverName == "" {
		driverName = config.GraphDriver
	}

	d.RegistryService = registryService
	d.PluginStore = pluginStore
	logger.RegisterPluginGetter(d.PluginStore)

	// Plugin system initialization should happen before restore. Do not change order.
	d.pluginManager, err = plugin.NewManager(plugin.ManagerConfig{ // plugin/manager.go  创建plugin manager 实例
		Root:               filepath.Join(config.Root, "plugins"),
		ExecRoot:           getPluginExecRoot(config.Root),
		Store:              d.PluginStore,
		Executor:           containerdRemote,
		RegistryService:    registryService,
		LiveRestoreEnabled: config.LiveRestoreEnabled,
		LogPluginEvent:     d.LogPluginEvent, // todo: make private
		AuthzMiddleware:    config.AuthzMiddleware,
	})
	if err != nil {
		return nil, errors.Wrap(err, "couldn't create plugin manager")
	}

	//设立graph driver，graphdriver主要是来管理镜像，以及镜像与镜像之间关系的实现方法。由于config.GraphDriver的默认值为空，所以主要的处理流程在graphdriver.New()中；
	//加载的优先级的顺序为 priority = []string{"aufs","btrfs","zfs","devicemapper","overlay","vfs"}，
	// NewStoreFromOptions creates a new Store instance  // lay/layer_store.go 创建graphDriver实例，从driver创建layer仓库的实例
	d.layerStore, err = layer.NewStoreFromOptions(layer.StoreOptions { //初始化/var/lib/docker/image/devicemapper/layerdb/相关操作的接口并初始化存储驱动devicemapper等
		StorePath:                 config.Root,
		MetadataStorePathTemplate: filepath.Join(config.Root, "image", "%s", "layerdb"),
		GraphDriver:               driverName,
		GraphDriverOptions:        config.GraphOptions,
		UIDMaps:                   uidMaps,
		GIDMaps:                   gidMaps,
		PluginGetter:              d.PluginStore,
		ExperimentalEnabled:       config.Experimental,
	})
	if err != nil {
		return nil, err
	}

	graphDriver := d.layerStore.DriverName() // 取layer里面的graphDriver   devicemapper等
	imageRoot := filepath.Join(config.Root, "image", graphDriver)  ///var/lib/docker/image/devicemapper/
	//设置系统是否使用SELinux，SElinux有个问题是不能和btrfs的graphdriver一起使用；
	// Configure and validate the kernels security support
	if err := configureKernelSecuritySupport(config, graphDriver); err != nil {// linux内核安全支持的配置
		return nil, err
	}

	logrus.Debugf("Max Concurrent Downloads: %d", *config.MaxConcurrentDownloads)
	d.downloadManager = xfer.NewLayerDownloadManager(d.layerStore, *config.MaxConcurrentDownloads)// distribution/xfer/download.go 创建层下载管理实例
	logrus.Debugf("Max Concurrent Uploads: %d", *config.MaxConcurrentUploads)
	d.uploadManager = xfer.NewLayerUploadManager(*config.MaxConcurrentUploads)// distribution/xfer/upload.go 创建层上传管理实例

	//fs存放了image的原信息，存储的目录位于/var/lib/docker/image/{driver}/imagedb，该目录下主要包含两个目录content和metadata
	ifs, err := image.NewFSStoreBackend(filepath.Join(imageRoot, "imagedb")) // image/fs.go   创建仓库后端的文件系统
	if err != nil {
		return nil, err
	}

	/*
	遍历/var/lib/docker/image/{driver}/imagedb/content/sha256文件夹中的hex目录,然后读取其中的文件内容，通过diff_ids计算出chainID,然后获取
	/var/lib/docker/image/devicemapper/layerdb/sha256/$chainID的 roLayer 信息，然后存入 store.images[]中
	*/
	//ifs为type fs struct类型结构，实现有 StoreBackend 接口函数信息, ifs对应的目录为/var/lib/docker/image/{driver}/imagedb
	d.imageStore, err = image.NewImageStore(ifs, d.layerStore)
	if err != nil {
		return nil, err
	}

	// Configure the volumes driver
	volStore, err := d.configureVolumes(rootUID, rootGID) // 创建 volumes driver实例
	if err != nil {
		return nil, err
	}
	//  api/common.go 按照路径加载libtrust key ，没有就新建一个
	trustKey, err := api.LoadOrCreateTrustKey(config.TrustKeyPath)
	if err != nil {
		return nil, err
	}

	trustDir := filepath.Join(config.Root, "trust")

	if err := system.MkdirAll(trustDir, 0700); err != nil {
		return nil, err
	}
	// distribution/metadata/metadata.go  按照路径创建一个基于文件系统的元数据仓库实例
	distributionMetadataStore, err := dmetadata.NewFSMetadataStore(filepath.Join(imageRoot, "distribution")) ///var/lib/docker/image/devicemapper/distribution
	if err != nil {
		return nil, err
	}

	eventsService := events.New()  //  daemon/events/events.go  创建event服务实例
	// reference/store.go  按照路径创建 reference仓库实例
	referenceStore, err := refstore.NewReferenceStore(filepath.Join(imageRoot, "repositories.json"))
	if err != nil {
		return nil, fmt.Errorf("Couldn't create Tag store repositories: %s", err)
	}

	migrationStart := time.Now()
	// migrate/v1/migrate.go  迁移metadata
	if err := v1.Migrate(config.Root, graphDriver, d.layerStore, d.imageStore, referenceStore, distributionMetadataStore); err != nil {
		logrus.Errorf("Graph migration failed: %q. Your old graph data was found to be too inconsistent for upgrading to content-addressable storage. Some of the old data was probably not upgraded. We recommend starting over with a clean storage directory if possible.", err)
	}
	logrus.Infof("Graph migration to content-addressability took %.2f seconds", time.Since(migrationStart).Seconds())

	// Discovery is only enabled when the daemon is launched with an address to advertise.  When
	// initialized, the daemon is registered and we can store the discovery backend as it's read-only
	if err := d.initDiscovery(config); err != nil { // 创建 discoveryWatcher 实例
		return nil, err
	}

	sysInfo := sysinfo.New(false) // 获取系统信息，linux 不支持cgroup则返回
	// Check if Devices cgroup is mounted, it is hard requirement for container security,
	// on Linux.
	if runtime.GOOS == "linux" && !sysInfo.CgroupDevicesEnabled {
		return nil, errors.New("Devices cgroup isn't mounted")
	}

	d.ID = trustKey.PublicKey().KeyID() // 赋值
	d.repository = daemonRepo
	d.containers = container.NewMemoryStore()
	d.execCommands = exec.NewStore()
	d.referenceStore = referenceStore
	d.distributionMetadataStore = distributionMetadataStore
	d.trustKey = trustKey
	d.idIndex = truncindex.NewTruncIndex([]string{})
	d.statsCollector = d.newStatsCollector(1 * time.Second)
	d.defaultLogConfig = containertypes.LogConfig{
		Type:   config.LogConfig.Type,
		Config: config.LogConfig.Config,
	}
	d.EventsService = eventsService
	d.volumes = volStore
	d.root = config.Root
	d.uidMaps = uidMaps
	d.gidMaps = gidMaps
	d.seccompEnabled = sysInfo.Seccomp
	d.apparmorEnabled = sysInfo.AppArmor

	d.nameIndex = registrar.NewRegistrar()
	d.linkIndex = newLinkIndex()
	d.containerdRemote = containerdRemote

	go d.execCommandGC() // 新建协程清理容器不需要的命令

	d.containerd, err = containerdRemote.Client(d) //  创建和daemon相关的容器客户端 libcontainerd
	if err != nil {
		return nil, err
	}

	if err := d.restore(); err != nil { //  restart  container
		return nil, err
	}

	// FIXME: this method never returns an error
	info, _ := d.SystemInfo()  // 获取host server系统信息

	engineVersion.WithValues(
		dockerversion.Version,
		dockerversion.GitCommit,
		info.Architecture,
		info.Driver,
		info.KernelVersion,
		info.OperatingSystem,
	).Set(1)
	engineCpus.Set(float64(info.NCPU))
	engineMemory.Set(float64(info.MemTotal))

	return d, nil
}

func (daemon *Daemon) shutdownContainer(c *container.Container) error {
	stopTimeout := c.StopTimeout()
	// TODO(windows): Handle docker restart with paused containers
	if c.IsPaused() {
		// To terminate a process in freezer cgroup, we should send
		// SIGTERM to this process then unfreeze it, and the process will
		// force to terminate immediately.
		logrus.Debugf("Found container %s is paused, sending SIGTERM before unpausing it", c.ID)
		sig, ok := signal.SignalMap["TERM"]
		if !ok {
			return errors.New("System does not support SIGTERM")
		}
		if err := daemon.kill(c, int(sig)); err != nil {
			return fmt.Errorf("sending SIGTERM to container %s with error: %v", c.ID, err)
		}
		if err := daemon.containerUnpause(c); err != nil {
			return fmt.Errorf("Failed to unpause container %s with error: %v", c.ID, err)
		}
		if _, err := c.WaitStop(time.Duration(stopTimeout) * time.Second); err != nil {
			logrus.Debugf("container %s failed to exit in %d second of SIGTERM, sending SIGKILL to force", c.ID, stopTimeout)
			sig, ok := signal.SignalMap["KILL"]
			if !ok {
				return errors.New("System does not support SIGKILL")
			}
			if err := daemon.kill(c, int(sig)); err != nil {
				logrus.Errorf("Failed to SIGKILL container %s", c.ID)
			}
			c.WaitStop(-1 * time.Second)
			return err
		}
	}
	// If container failed to exit in stopTimeout seconds of SIGTERM, then using the force
	if err := daemon.containerStop(c, stopTimeout); err != nil {
		return fmt.Errorf("Failed to stop container %s with error: %v", c.ID, err)
	}

	c.WaitStop(-1 * time.Second)
	return nil
}

// ShutdownTimeout returns the shutdown timeout based on the max stopTimeout of the containers,
// and is limited by daemon's ShutdownTimeout.
func (daemon *Daemon) ShutdownTimeout() int {
	// By default we use daemon's ShutdownTimeout.
	shutdownTimeout := daemon.configStore.ShutdownTimeout

	graceTimeout := 5
	if daemon.containers != nil {
		for _, c := range daemon.containers.List() {
			if shutdownTimeout >= 0 {
				stopTimeout := c.StopTimeout()
				if stopTimeout < 0 {
					shutdownTimeout = -1
				} else {
					if stopTimeout+graceTimeout > shutdownTimeout {
						shutdownTimeout = stopTimeout + graceTimeout
					}
				}
			}
		}
	}
	return shutdownTimeout
}

/*
遍历所有运行中的容器，先使用SIGTERM软杀死容器进程，如果10S内不能完成，则使用SIGKILL强制杀死
如果netcontroller被初始化过，调用libnetwork/controller.go GC方法进行垃圾回收
结束运行中的镜像存储驱动进程
*/
// Shutdown stops the daemon.
func (daemon *Daemon) Shutdown() error {
	daemon.shutdown = true
	// Keep mounts and networking running on daemon shutdown if
	// we are to keep containers running and restore them.

	if daemon.configStore.LiveRestoreEnabled && daemon.containers != nil {
		// check if there are any running containers, if none we should do some cleanup
		if ls, err := daemon.Containers(&types.ContainerListOptions{}); len(ls) != 0 || err != nil {
			return nil
		}
	}

	if daemon.containers != nil {
		logrus.Debugf("start clean shutdown of all containers with a %d seconds timeout...", daemon.configStore.ShutdownTimeout)
		daemon.containers.ApplyAll(func(c *container.Container) {
			if !c.IsRunning() {
				return
			}
			logrus.Debugf("stopping %s", c.ID)
			if err := daemon.shutdownContainer(c); err != nil {
				logrus.Errorf("Stop container error: %v", err)
				return
			}
			if mountid, err := daemon.layerStore.GetMountID(c.ID); err == nil {
				daemon.cleanupMountsByID(mountid)
			}
			logrus.Debugf("container stopped %s", c.ID)
		})
	}

	if daemon.volumes != nil {
		if err := daemon.volumes.Shutdown(); err != nil {
			logrus.Errorf("Error shutting down volume store: %v", err)
		}
	}

	if daemon.layerStore != nil {
		if err := daemon.layerStore.Cleanup(); err != nil {
			logrus.Errorf("Error during layer Store.Cleanup(): %v", err)
		}
	}

	// If we are part of a cluster, clean up cluster's stuff
	if daemon.clusterProvider != nil {
		logrus.Debugf("start clean shutdown of cluster resources...")
		daemon.DaemonLeavesCluster()
	}

	// Shutdown plugins after containers and layerstore. Don't change the order.
	daemon.pluginShutdown()

	// trigger libnetwork Stop only if it's initialized
	if daemon.netController != nil {
		daemon.netController.Stop()
	}

	if err := daemon.cleanupMounts(); err != nil {
		return err
	}

	return nil
}

// Mount sets container.BaseFS
// (is it not set coming in? why is it unset?)
func (daemon *Daemon) Mount(container *container.Container) error {
	dir, err := container.RWLayer.Mount(container.GetMountLabel())
	if err != nil {
		return err
	}
	logrus.Debugf("container mounted via layerStore: %v", dir)

	if container.BaseFS != dir {
		// The mount path reported by the graph driver should always be trusted on Windows, since the
		// volume path for a given mounted layer may change over time.  This should only be an error
		// on non-Windows operating systems.
		if container.BaseFS != "" && runtime.GOOS != "windows" {
			daemon.Unmount(container)
			return fmt.Errorf("Error: driver %s is returning inconsistent paths for container %s ('%s' then '%s')",
				daemon.GraphDriverName(), container.ID, container.BaseFS, dir)
		}
	}
	container.BaseFS = dir // TODO: combine these fields
	return nil
}

// Unmount unsets the container base filesystem
func (daemon *Daemon) Unmount(container *container.Container) error {
	if err := container.RWLayer.Unmount(); err != nil {
		logrus.Errorf("Error unmounting container %s: %s", container.ID, err)
		return err
	}

	return nil
}

// Subnets return the IPv4 and IPv6 subnets of networks that are manager by Docker.
func (daemon *Daemon) Subnets() ([]net.IPNet, []net.IPNet) {
	var v4Subnets []net.IPNet
	var v6Subnets []net.IPNet

	managedNetworks := daemon.netController.Networks()

	for _, managedNetwork := range managedNetworks {
		v4infos, v6infos := managedNetwork.Info().IpamInfo()
		for _, info := range v4infos {
			if info.IPAMData.Pool != nil {
				v4Subnets = append(v4Subnets, *info.IPAMData.Pool)
			}
		}
		for _, info := range v6infos {
			if info.IPAMData.Pool != nil {
				v6Subnets = append(v6Subnets, *info.IPAMData.Pool)
			}
		}
	}

	return v4Subnets, v6Subnets
}

// GraphDriverName returns the name of the graph driver used by the layer.Store
func (daemon *Daemon) GraphDriverName() string {
	return daemon.layerStore.DriverName()
}

// GetUIDGIDMaps returns the current daemon's user namespace settings
// for the full uid and gid maps which will be applied to containers
// started in this instance.
func (daemon *Daemon) GetUIDGIDMaps() ([]idtools.IDMap, []idtools.IDMap) {
	return daemon.uidMaps, daemon.gidMaps
}

// GetRemappedUIDGID returns the current daemon's uid and gid values
// if user namespaces are in use for this daemon instance.  If not
// this function will return "real" root values of 0, 0.
func (daemon *Daemon) GetRemappedUIDGID() (int, int) {
	uid, gid, _ := idtools.GetRootUIDGID(daemon.uidMaps, daemon.gidMaps)
	return uid, gid
}

// prepareTempDir prepares and returns the default directory to use
// for temporary files.
// If it doesn't exist, it is created. If it exists, its content is removed.
func prepareTempDir(rootDir string, rootUID, rootGID int) (string, error) {
	var tmpDir string
	if tmpDir = os.Getenv("DOCKER_TMPDIR"); tmpDir == "" {
		tmpDir = filepath.Join(rootDir, "tmp")
		newName := tmpDir + "-old"
		if err := os.Rename(tmpDir, newName); err != nil {
			go func() {
				if err := os.RemoveAll(newName); err != nil {
					logrus.Warnf("failed to delete old tmp directory: %s", newName)
				}
			}()
		} else {
			logrus.Warnf("failed to rename %s for background deletion: %s. Deleting synchronously", tmpDir, err)
			if err := os.RemoveAll(tmpDir); err != nil {
				logrus.Warnf("failed to delete old tmp directory: %s", tmpDir)
			}
		}
	}
	// We don't remove the content of tmpdir if it's not the default,
	// it may hold things that do not belong to us.
	return tmpDir, idtools.MkdirAllAs(tmpDir, 0700, rootUID, rootGID)
}

//setupInitLayer(initPath)；创建初始化层，就是创建一个容器需要的基本目录和文件，在 initMount 中执行
func (daemon *Daemon) setupInitLayer(initPath string) error {
	rootUID, rootGID := daemon.GetRemappedUIDGID()
	return initlayer.Setup(initPath, rootUID, rootGID)
}

func setDefaultMtu(conf *config.Config) {
	// do nothing if the config does not have the default 0 value.
	if conf.Mtu != 0 {
		return
	}
	conf.Mtu = config.DefaultNetworkMtu
}
// 创建 volumes driver实例
// 创建 volumes driver实例
func (daemon *Daemon) configureVolumes(rootUID, rootGID int) (*store.VolumeStore, error) {
	volumesDriver, err := local.New(daemon.configStore.Root, rootUID, rootGID)
	if err != nil {
		return nil, err
	}

	volumedrivers.RegisterPluginGetter(daemon.PluginStore)

	if !volumedrivers.Register(volumesDriver, volumesDriver.Name()) {
		return nil, errors.New("local volume driver could not be registered")
	}
	return store.New(daemon.configStore.Root)
}

// IsShuttingDown tells whether the daemon is shutting down or not
func (daemon *Daemon) IsShuttingDown() bool {
	return daemon.shutdown
}
// 创建 discoveryWatcher 实例
// initDiscovery initializes the discovery watcher for this daemon.
func (daemon *Daemon) initDiscovery(conf *config.Config) error {
	advertise, err := config.ParseClusterAdvertiseSettings(conf.ClusterStore, conf.ClusterAdvertise)
	if err != nil {
		if err == discovery.ErrDiscoveryDisabled {
			return nil
		}
		return err
	}

	conf.ClusterAdvertise = advertise
	discoveryWatcher, err := discovery.Init(conf.ClusterStore, conf.ClusterAdvertise, conf.ClusterOpts)
	if err != nil {
		return fmt.Errorf("discovery initialization failed (%v)", err)
	}

	daemon.discoveryWatcher = discoveryWatcher
	return nil
}

func isBridgeNetworkDisabled(conf *config.Config) bool {
	return conf.BridgeConfig.Iface == config.DisableNetworkBridge
}

func (daemon *Daemon) networkOptions(dconfig *config.Config, pg plugingetter.PluginGetter, activeSandboxes map[string]interface{}) ([]nwconfig.Option, error) {
	options := []nwconfig.Option{}
	if dconfig == nil {
		return options, nil
	}

	options = append(options, nwconfig.OptionExperimental(dconfig.Experimental))
	options = append(options, nwconfig.OptionDataDir(dconfig.Root))
	options = append(options, nwconfig.OptionExecRoot(dconfig.GetExecRoot()))

	dd := runconfig.DefaultDaemonNetworkMode()
	dn := runconfig.DefaultDaemonNetworkMode().NetworkName()
	options = append(options, nwconfig.OptionDefaultDriver(string(dd)))
	options = append(options, nwconfig.OptionDefaultNetwork(dn))

	if strings.TrimSpace(dconfig.ClusterStore) != "" {
		kv := strings.Split(dconfig.ClusterStore, "://")
		if len(kv) != 2 {
			return nil, errors.New("kv store daemon config must be of the form KV-PROVIDER://KV-URL")
		}
		options = append(options, nwconfig.OptionKVProvider(kv[0]))
		options = append(options, nwconfig.OptionKVProviderURL(kv[1]))
	}
	if len(dconfig.ClusterOpts) > 0 {
		options = append(options, nwconfig.OptionKVOpts(dconfig.ClusterOpts))
	}

	if daemon.discoveryWatcher != nil {
		options = append(options, nwconfig.OptionDiscoveryWatcher(daemon.discoveryWatcher))
	}

	if dconfig.ClusterAdvertise != "" {
		options = append(options, nwconfig.OptionDiscoveryAddress(dconfig.ClusterAdvertise))
	}

	options = append(options, nwconfig.OptionLabels(dconfig.Labels))
	options = append(options, driverOptions(dconfig)...)

	if daemon.configStore != nil && daemon.configStore.LiveRestoreEnabled && len(activeSandboxes) != 0 {
		options = append(options, nwconfig.OptionActiveSandboxes(activeSandboxes))
	}

	if pg != nil {
		options = append(options, nwconfig.OptionPluginGetter(pg))
	}

	return options, nil
}

func copyBlkioEntry(entries []*containerd.BlkioStatsEntry) []types.BlkioStatEntry {
	out := make([]types.BlkioStatEntry, len(entries))
	for i, re := range entries {
		out[i] = types.BlkioStatEntry{
			Major: re.Major,
			Minor: re.Minor,
			Op:    re.Op,
			Value: re.Value,
		}
	}
	return out
}

// GetCluster returns the cluster
func (daemon *Daemon) GetCluster() Cluster {
	return daemon.cluster
}

// SetCluster sets the cluster
func (daemon *Daemon) SetCluster(cluster Cluster) {
	daemon.cluster = cluster
}

func (daemon *Daemon) pluginShutdown() {
	manager := daemon.pluginManager
	// Check for a valid manager object. In error conditions, daemon init can fail
	// and shutdown called, before plugin manager is initialized.
	if manager != nil {
		manager.Shutdown()
	}
}

// PluginManager returns current pluginManager associated with the daemon
func (daemon *Daemon) PluginManager() *plugin.Manager { // set up before daemon to avoid this method
	return daemon.pluginManager
}

// PluginGetter returns current pluginStore associated with the daemon
func (daemon *Daemon) PluginGetter() *plugin.Store {
	return daemon.PluginStore
}
//  daemon/daemon.go  先创建daemon 的root路径,linux 为 /var/lib/docker
// CreateDaemonRoot creates the root for the daemon
func CreateDaemonRoot(config *config.Config) error {
	// get the canonical path to the Docker root directory
	var realRoot string
	if _, err := os.Stat(config.Root); err != nil && os.IsNotExist(err) {
		realRoot = config.Root
	} else {
		realRoot, err = fileutils.ReadSymlinkedDirectory(config.Root)
		if err != nil {
			return fmt.Errorf("Unable to get the full path to root (%s): %s", config.Root, err)
		}
	}

	uidMaps, gidMaps, err := setupRemappedRoot(config)
	if err != nil {
		return err
	}
	rootUID, rootGID, err := idtools.GetRootUIDGID(uidMaps, gidMaps)
	if err != nil {
		return err
	}

	if err := setupDaemonRoot(config, realRoot, rootUID, rootGID); err != nil {
		return err
	}

	return nil
}
