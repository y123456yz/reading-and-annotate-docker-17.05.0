package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/distribution/uuid"
	"github.com/docker/docker/api"
	apiserver "github.com/docker/docker/api/server"
	"github.com/docker/docker/api/server/middleware"
	"github.com/docker/docker/api/server/router"
	"github.com/docker/docker/api/server/router/build"
	checkpointrouter "github.com/docker/docker/api/server/router/checkpoint"
	"github.com/docker/docker/api/server/router/container"
	"github.com/docker/docker/api/server/router/image"
	"github.com/docker/docker/api/server/router/network"
	pluginrouter "github.com/docker/docker/api/server/router/plugin"
	swarmrouter "github.com/docker/docker/api/server/router/swarm"
	systemrouter "github.com/docker/docker/api/server/router/system"
	"github.com/docker/docker/api/server/router/volume"
	"github.com/docker/docker/builder/dockerfile"
	cliconfig "github.com/docker/docker/cli/config"
	"github.com/docker/docker/cli/debug"
	cliflags "github.com/docker/docker/cli/flags"
	"github.com/docker/docker/daemon"
	"github.com/docker/docker/daemon/cluster"
	"github.com/docker/docker/daemon/config"
	"github.com/docker/docker/daemon/logger"
	"github.com/docker/docker/dockerversion"
	"github.com/docker/docker/libcontainerd"
	dopts "github.com/docker/docker/opts"
	"github.com/docker/docker/pkg/authorization"
	"github.com/docker/docker/pkg/jsonlog"
	"github.com/docker/docker/pkg/listeners"
	"github.com/docker/docker/pkg/pidfile"
	"github.com/docker/docker/pkg/plugingetter"
	"github.com/docker/docker/pkg/signal"
	"github.com/docker/docker/pkg/system"
	"github.com/docker/docker/plugin"
	"github.com/docker/docker/registry"
	"github.com/docker/docker/runconfig"
	"github.com/docker/go-connections/tlsconfig"
	"github.com/spf13/pflag"
)

// DaemonCli represents the daemon CLI.
type DaemonCli struct { //使用见NewDaemonCli
	*config.Config
	configFile *string //默认/etc/docker/daemon.json
	flags      *pflag.FlagSet

	api             *apiserver.Server
	d               *daemon.Daemon
	authzMiddleware *authorization.Middleware // authzMiddleware enables to dynamically reload the authorization plugins
}

// NewDaemonCli returns a daemon CLI
func NewDaemonCli() *DaemonCli {
	return &DaemonCli{}
}

func migrateKey(config *config.Config) (err error) {
	// No migration necessary on Windows
	if runtime.GOOS == "windows" {
		return nil
	}

	// Migrate trust key if exists at ~/.docker/key.json and owned by current user
	oldPath := filepath.Join(cliconfig.Dir(), cliflags.DefaultTrustKeyFile)
	newPath := filepath.Join(getDaemonConfDir(config.Root), cliflags.DefaultTrustKeyFile)
	if _, statErr := os.Stat(newPath); os.IsNotExist(statErr) && currentUserIsOwner(oldPath) {
		defer func() {
			// Ensure old path is removed if no error occurred
			if err == nil {
				err = os.Remove(oldPath)
			} else {
				logrus.Warnf("Key migration failed, key file not removed at %s", oldPath)
				os.Remove(newPath)
			}
		}()

		if err := system.MkdirAll(getDaemonConfDir(config.Root), os.FileMode(0644)); err != nil {
			return fmt.Errorf("Unable to create daemon configuration directory: %s", err)
		}

		newFile, err := os.OpenFile(newPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return fmt.Errorf("error creating key file %q: %s", newPath, err)
		}
		defer newFile.Close()

		oldFile, err := os.Open(oldPath)
		if err != nil {
			return fmt.Errorf("error opening key file %q: %s", oldPath, err)
		}
		defer oldFile.Close()

		if _, err := io.Copy(newFile, oldFile); err != nil {
			return fmt.Errorf("error copying key: %s", err)
		}

		logrus.Infof("Migrated key from %s to %s", oldPath, newPath)
	}

	return nil
}

//dockerd\daemon.go中start函数和daemon\daemon.go中的NewDaemon是理解的主线
func (cli *DaemonCli) start(opts daemonOptions) (err error) {
	stopc := make(chan bool) // 创建一个通道，看上去退出时候阻塞用的
	defer close(stopc) // 函数结束时候关闭通道

	// warn from uuid package when running the daemon
	uuid.Loggerf = logrus.Warnf

	opts.common.SetDefaultOptions(opts.flags) // 同client，设置默认参数

	if cli.Config, err = loadDaemonCliConfig(opts); err != nil {// 从opt里面将参数导入到config
		return err
	}
	cli.configFile = &opts.configFile //默认/etc/docker/daemon.json
	cli.flags = opts.flags

	if opts.common.TrustKey == "" {
		opts.common.TrustKey = filepath.Join(
			getDaemonConfDir(cli.Config.Root),
			cliflags.DefaultTrustKeyFile)
	}

	if cli.Config.Debug {
		debug.Enable()
	}

	if cli.Config.Experimental {
		logrus.Warn("Running experimental build")
	}

	logrus.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: jsonlog.RFC3339NanoFixed,
		DisableColors:   cli.Config.RawLogs,
	})

	if err := setDefaultUmask(); err != nil {
		return fmt.Errorf("Failed to set umask: %v", err)
	}

	if len(cli.LogConfig.Config) > 0 {
		if err := logger.ValidateLogOpts(cli.LogConfig.Type, cli.LogConfig.Config); err != nil {
			return fmt.Errorf("Failed to set log opts: %v", err)
		}
	}

	// Create the daemon root before we create ANY other files (PID, or migrate keys)
	// to ensure the appropriate ACL is set (particularly relevant on Windows)
	if err := daemon.CreateDaemonRoot(cli.Config); err != nil { //  daemon/daemon.go  先创建daemon 的root路径,linux 为 /var/lib/docker
		return err
	}

	if cli.Pidfile != "" {//  "/var/run/docker.pid "  非空则创建一个pidfile
		pf, err := pidfile.New(cli.Pidfile)
		if err != nil {
			return fmt.Errorf("Error starting daemon: %v", err)
		}
		defer func() { //start函数结束的时候清除文件
			if err := pf.Remove(); err != nil {
				logrus.Error(err)
			}
		}()
	}

	serverConfig := &apiserver.Config{ ///  server 的config文件创建以及一些赋值
		Logging:     true,
		SocketGroup: cli.Config.SocketGroup,
		Version:     dockerversion.Version,
		EnableCors:  cli.Config.EnableCors,
		CorsHeaders: cli.Config.CorsHeaders,
	}

	if cli.Config.TLS {
		tlsOptions := tlsconfig.Options{
			CAFile:   cli.Config.CommonTLSOptions.CAFile,
			CertFile: cli.Config.CommonTLSOptions.CertFile,
			KeyFile:  cli.Config.CommonTLSOptions.KeyFile,
		}

		if cli.Config.TLSVerify {
			// server requires and verifies client's certificate
			tlsOptions.ClientAuth = tls.RequireAndVerifyClientCert
		}
		tlsConfig, err := tlsconfig.Server(tlsOptions)
		if err != nil {
			return err
		}
		serverConfig.TLSConfig = tlsConfig
	}

	if len(cli.Config.Hosts) == 0 {
		cli.Config.Hosts = make([]string, 1)
	}
	// api/server/server.go 新建一个server的实例
	api := apiserver.New(serverConfig) //api用来处理客户端Http请求信息
	cli.api = api
	// 循环根据配置文件里面所有host增加server的监听
	for i := 0; i < len(cli.Config.Hosts); i++ { //bind监听指定IP:port
		var err error
		if cli.Config.Hosts[i], err = dopts.ParseHost(cli.Config.TLS, cli.Config.Hosts[i]); err != nil {
			return fmt.Errorf("error parsing -H %s : %v", cli.Config.Hosts[i], err)
		}

		protoAddr := cli.Config.Hosts[i]
		protoAddrParts := strings.SplitN(protoAddr, "://", 2)
		if len(protoAddrParts) != 2 {
			return fmt.Errorf("bad format %s, expected PROTO://ADDR", protoAddr)
		}

		proto := protoAddrParts[0]
		addr := protoAddrParts[1]

		// It's a bad idea to bind to TCP without tlsverify.
		if proto == "tcp" && (serverConfig.TLSConfig == nil || serverConfig.TLSConfig.ClientAuth != tls.RequireAndVerifyClientCert) {
			logrus.Warn("[!] DON'T BIND ON ANY IP ADDRESS WITHOUT setting --tlsverify IF YOU DON'T KNOW WHAT YOU'RE DOING [!]")
		}

		//绑定监听的IP端口
		ls, err := listeners.Init(proto, addr, serverConfig.SocketGroup, serverConfig.TLSConfig)
		if err != nil {
			return err
		}
		ls = wrapListeners(proto, ls)
		// If we're binding to a TCP port, make sure that a container doesn't try to use it.
		if proto == "tcp" {
			if err := allocateDaemonPort(addr); err != nil {
				return err
			}
		}
		logrus.Debugf("Listener created for HTTP on %s (%s)", proto, addr)
		api.Accept(addr, ls...)
	}
	// 把 key.json 移动到 /etc/docker 路径
	if err := migrateKey(cli.Config); err != nil {
		return err
	}

	// FIXME: why is this down here instead of with the other TrustKey logic above?
	cli.TrustKeyPath = opts.common.TrustKey
	// registry/service.go 新建一个default的registryserver
	registryService := registry.NewService(cli.Config.ServiceOptions)

	//libcontainerd: new containerd process, pid:   启动libcontainerd   // libcontainerd/remote_unix.go
	//  /var/run/docker路径 ，创建 containerd Remote，container相关处理启动grpc的client api，事件监控等
	//libcontainerd->remote_unix.go中的type remote struct 类型
	containerdRemote, err := libcontainerd.New(cli.getLibcontainerdRoot(), cli.getPlatformRemoteOptions()...)
	if err != nil {
		return err
	}

	// start时收到异常信息量的处理，关闭cli，其他地方处理结束才会给通道一个消息结束
	signal.Trap(func() { //signal.Trap() 用来接收 用户发出的命令，例如 control ＋ c之类的
		cli.stop()
		<-stopc // wait for daemonCli.start() to return
	})

	// Notify that the API is active, but before daemon is set up.
	preNotifySystem()  // 只是window系统有用

	pluginStore := plugin.NewStore()
	//  给apiserver初始化中间件，要求cli.d必须被赋值
	if err := cli.initMiddlewares(api, serverConfig, pluginStore); err != nil {
		logrus.Fatalf("Error creating middlewares: %v", err)
	}
	// daemon/daemon.go  新建daemon的所有东西
	d, err := daemon.NewDaemon(cli.Config, registryService, containerdRemote, pluginStore)
	if err != nil {
		return fmt.Errorf("Error starting daemon: %v", err)
	}

	// validate after NewDaemon has restored enabled plugins. Dont change order.
	if err := validateAuthzPlugins(cli.Config.AuthorizationPlugins, pluginStore); err != nil {
		return fmt.Errorf("Error validating authorization plugin: %v", err)
	}

	if cli.Config.MetricsAddress != "" { //  metrics 地址存在的话，起一个metrics server 监听
		if !d.HasExperimental() {
			return fmt.Errorf("metrics-addr is only supported when experimental is enabled")
		}
		if err := startMetricsServer(cli.Config.MetricsAddress); err != nil {
			return err
		}
	}

	name, _ := os.Hostname()

	c, err := cluster.New(cluster.Config{  // daemon/cluster/cluster.go  新建一个cluster 实例
		Root:                   cli.Config.Root,
		Name:                   name,
		Backend:                d,
		NetworkSubnetsProvider: d,
		DefaultAdvertiseAddr:   cli.Config.SwarmDefaultAdvertiseAddr,
		RuntimeRoot:            cli.getSwarmRunRoot(),
	})
	if err != nil {
		logrus.Fatalf("Error creating cluster component: %v", err)
	}

	// Restart all autostart containers which has a swarm endpoint
	// and is not yet running now that we have successfully
	// initialized the cluster.
	d.RestartSwarmContainers() // daemon/daemon.go   重启 container ，条件见函数内

	logrus.Info("Daemon has completed initialization")

	logrus.WithFields(logrus.Fields{
		"version":     dockerversion.Version,
		"commit":      dockerversion.GitCommit,
		"graphdriver": d.GraphDriverName(),
	}).Info("Docker daemon")

	cli.d = d

	d.SetCluster(c)
	// 注册api消息处理函数,daemon命令的处理可以在这里面查
	initRouter(api, d, c)

	cli.setupConfigReloadTrap()  //  设置一个系统调用重新加载配置

	// The serve API routine never exits unless an error occurs
	// We need to start it as a goroutine and wait on it so
	// daemon doesn't exit
	serveAPIWait := make(chan error)
	go api.Wait(serveAPIWait) //在这里面处理docker xxx的各种命令请求

	// after the daemon is done setting up we can notify systemd api
	notifySystem() //通知init进程，docker已经开始正常工作了

	// Daemon is fully initialized and handling API traffic
	// Wait for serve API to complete
	errAPI := <-serveAPIWait //前面的go api.Wait(serveAPIWait)处理Url请求，如果server异常，则会走到这里，最终结束服务
	c.Cleanup()// 关闭cluster
	fmt.Printf("yang test...... shutdowndaemon\n");
	shutdownDaemon(d)// 关闭daemon
	containerdRemote.Cleanup()// 关闭 container
	if errAPI != nil {
		return fmt.Errorf("Shutting down due to ServeAPI error: %v", errAPI)
	}

	return nil
}

func (cli *DaemonCli) reloadConfig() {
	reload := func(config *config.Config) {

		// Revalidate and reload the authorization plugins
		if err := validateAuthzPlugins(config.AuthorizationPlugins, cli.d.PluginStore); err != nil {
			logrus.Fatalf("Error validating authorization plugin: %v", err)
			return
		}
		cli.authzMiddleware.SetPlugins(config.AuthorizationPlugins)

		if err := cli.d.Reload(config); err != nil {
			logrus.Errorf("Error reconfiguring the daemon: %v", err)
			return
		}

		if config.IsValueSet("debug") {
			debugEnabled := debug.IsEnabled()
			switch {
			case debugEnabled && !config.Debug: // disable debug
				debug.Disable()
				cli.api.DisableProfiler()
			case config.Debug && !debugEnabled: // enable debug
				debug.Enable()
				cli.api.EnableProfiler()
			}

		}
	}

	if err := config.Reload(*cli.configFile, cli.flags, reload); err != nil {
		logrus.Error(err)
	}
}

func (cli *DaemonCli) stop() {
	cli.api.Close()
}

// shutdownDaemon just wraps daemon.Shutdown() to handle a timeout in case
// d.Shutdown() is waiting too long to kill container or worst it's
// blocked there
func shutdownDaemon(d *daemon.Daemon) {
	/*
	创建并设置一个channel,使用select监听数据。在正确完成关闭daemon工作后将该channel关闭，标识该工作的完成；否则在超时后(15S)报错
	*/
	shutdownTimeout := d.ShutdownTimeout()
	ch := make(chan struct{})
	go func() {
		d.Shutdown()
		close(ch)
	}()
	if shutdownTimeout < 0 {
		<-ch
		logrus.Debug("Clean shutdown succeeded")
		return
	}
	select {
	case <-ch:
		logrus.Debug("Clean shutdown succeeded")
	case <-time.After(time.Duration(shutdownTimeout) * time.Second):
		logrus.Error("Force shutdown daemon")
	}
}

func loadDaemonCliConfig(opts daemonOptions) (*config.Config, error) {
	conf := opts.daemonConfig
	flags := opts.flags
	conf.Debug = opts.common.Debug
	conf.Hosts = opts.common.Hosts
	conf.LogLevel = opts.common.LogLevel
	conf.TLS = opts.common.TLS
	conf.TLSVerify = opts.common.TLSVerify
	conf.CommonTLSOptions = config.CommonTLSOptions{}

	if opts.common.TLSOptions != nil {
		conf.CommonTLSOptions.CAFile = opts.common.TLSOptions.CAFile
		conf.CommonTLSOptions.CertFile = opts.common.TLSOptions.CertFile
		conf.CommonTLSOptions.KeyFile = opts.common.TLSOptions.KeyFile
	}

	if flags.Changed("graph") && flags.Changed("data-root") {
		return nil, fmt.Errorf(`cannot specify both "--graph" and "--data-root" option`)
	}

	if opts.configFile != "" {
		c, err := config.MergeDaemonConfigurations(conf, flags, opts.configFile)
		if err != nil {
			if flags.Changed("config-file") || !os.IsNotExist(err) {
				return nil, fmt.Errorf("unable to configure the Docker daemon with file %s: %v\n", opts.configFile, err)
			}
		}
		// the merged configuration can be nil if the config file didn't exist.
		// leave the current configuration as it is if when that happens.
		if c != nil {
			conf = c
		}
	}

	if err := config.Validate(conf); err != nil {
		return nil, err
	}

	if flags.Changed("graph") {
		logrus.Warnf(`the "-g / --graph" flag is deprecated. Please use "--data-root" instead`)
	}

	// Labels of the docker engine used to allow multiple values associated with the same key.
	// This is deprecated in 1.13, and, be removed after 3 release cycles.
	// The following will check the conflict of labels, and report a warning for deprecation.
	//
	// TODO: After 3 release cycles (17.12) an error will be returned, and labels will be
	// sanitized to consolidate duplicate key-value pairs (config.Labels = newLabels):
	//
	// newLabels, err := daemon.GetConflictFreeLabels(config.Labels)
	// if err != nil {
	//	return nil, err
	// }
	// config.Labels = newLabels
	//
	if _, err := config.GetConflictFreeLabels(conf.Labels); err != nil {
		logrus.Warnf("Engine labels with duplicate keys and conflicting values have been deprecated: %s", err)
	}

	// Regardless of whether the user sets it to true or false, if they
	// specify TLSVerify at all then we need to turn on TLS
	if conf.IsValueSet(cliflags.FlagTLSVerify) {
		conf.TLS = true
	}

	// ensure that the log level is the one set after merging configurations
	cliflags.SetLogLevel(conf.LogLevel)

	return conf, nil
}

//在 cmd/dockerd/daemon.go 文件 initRouter 函数中 初始化 router
//客户端通过//newDockerCommand->AddCommands构建命令请求发往daemon，daemon收到后通过initRouter中初始化的handler来执行对应job
func initRouter(s *apiserver.Server, d *daemon.Daemon, c *cluster.Cluster) {
	decoder := runconfig.ContainerDecoder{}

	//routers数组存储各种router信息，NewRouter中对应各种API的匹配

	/*
	pull命令对应的restful api是"/images/create"， 会调用 imageRouter 的postImagesCreate 函数，函数位于image包的image_routes.go

	所有的client端的请求都使用api/client/请求名称.go文件来定义，在daemon端，所有请求的处理过程也放在daemon/请求名称.go文件中来定义。
	*/ //该routers 被赋值给 ，见(s *Server) InitRouter, 各种回调生效赋值见(s *Server) createMux
	routers := []router.Router{ //各种不同的docker xxx请求会有不同的 NewRouter 实现
		// we need to add the checkpoint router before the container router or the DELETE gets masked
		checkpointrouter.NewRouter(d, decoder),    //NewRouter中的initRoutes会定义各个客户端post请求的相应回调
		container.NewRouter(d, decoder),
		image.NewRouter(d, decoder),
		systemrouter.NewRouter(d, c),
		volume.NewRouter(d),
		build.NewRouter(dockerfile.NewBuildManager(d)),
		swarmrouter.NewRouter(c),
		pluginrouter.NewRouter(d.PluginManager()),
	}

	if d.NetworkControllerEnabled() {
		routers = append(routers, network.NewRouter(d, c))
	}

	if d.HasExperimental() {
		for _, r := range routers {
			for _, route := range r.Routes() {
				if experimental, ok := route.(router.ExperimentalRoute); ok {
					experimental.Enable()
				}
			}
		}
	}

	//dockerd\daemon.go 中的 func initRouter 中执行，注册各种客户端请求的http回调
	s.InitRouter(debug.IsEnabled(), routers...)
}

//(cli *DaemonCli) start 中执行
func (cli *DaemonCli) initMiddlewares(s *apiserver.Server, cfg *apiserver.Config, pluginStore *plugin.Store) error {
	v := cfg.Version

	exp := middleware.NewExperimentalMiddleware(cli.Config.Experimental)
	s.UseMiddleware(exp)

	vm := middleware.NewVersionMiddleware(v, api.DefaultVersion, api.MinVersion)
	s.UseMiddleware(vm)

	if cfg.EnableCors || cfg.CorsHeaders != "" {
		c := middleware.NewCORSMiddleware(cfg.CorsHeaders)
		s.UseMiddleware(c)
	}

	cli.authzMiddleware = authorization.NewMiddleware(cli.Config.AuthorizationPlugins, pluginStore)
	cli.Config.AuthzMiddleware = cli.authzMiddleware
	s.UseMiddleware(cli.authzMiddleware)
	return nil
}

// validates that the plugins requested with the --authorization-plugin flag are valid AuthzDriver
// plugins present on the host and available to the daemon
func validateAuthzPlugins(requestedPlugins []string, pg plugingetter.PluginGetter) error {
	for _, reqPlugin := range requestedPlugins {
		if _, err := pg.Get(reqPlugin, authorization.AuthZApiImplements, plugingetter.Lookup); err != nil {
			return err
		}
	}
	return nil
}
