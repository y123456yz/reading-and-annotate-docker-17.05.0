package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/cli"
	cliflags "github.com/docker/docker/cli/flags"
	"github.com/docker/docker/daemon/config"
	"github.com/docker/docker/dockerversion"
	"github.com/docker/docker/pkg/reexec"
	"github.com/docker/docker/pkg/term"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

//命令行参数解析存储在该结构中
type daemonOptions struct {
	//命令行是否是--version
	version      bool
	//命令行携带的config-file
	configFile   string//默认/etc/docker/daemon.json
	//赋值见installConfigFlags(opts.daemonConfig, flags)
	daemonConfig *config.Config
	//赋值见opts.common.InstallFlags(flags)
	common       *cliflags.CommonOptions
	flags        *pflag.FlagSet
}

func newDaemonCommand() *cobra.Command {
	opts := daemonOptions{
		daemonConfig: config.New(),
		common:       cliflags.NewCommonOptions(),
	}

	cmd := &cobra.Command{
		Use:           "dockerd [OPTIONS--yyz]",
		Short:         "A self-sufficient runtime for containers. --yyz dockered",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cli.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.flags = cmd.Flags()
			return runDaemon(opts)
		},
	}
	cli.SetupRootCommand(cmd)

	flags := cmd.Flags()
	//解析命令行中的version   config-file参数
	flags.BoolVarP(&opts.version, "version", "v", false, "Print version information and quit --yyz")
	flags.StringVar(&opts.configFile, "config-file", defaultDaemonConfigFile, "Daemon configuration file -- yyz")

	opts.common.InstallFlags(flags) //opts.common解析赋值
	installConfigFlags(opts.daemonConfig, flags)//opts.daemonConfig
	installServiceFlags(flags)

	return cmd
}

func runDaemon(opts daemonOptions) error {
	if opts.version { //如果是--version,则打印版本号返回
		showVersion()
		return nil
	}

	daemonCli := NewDaemonCli()

	// Windows specific settings as these are not defaulted.
	if runtime.GOOS == "windows" {
		if opts.daemonConfig.Pidfile == "" {
			opts.daemonConfig.Pidfile = filepath.Join(opts.daemonConfig.Root, "docker.pid")
		}
		if opts.configFile == "" {
			opts.configFile = filepath.Join(opts.daemonConfig.Root, `config\daemon.json`)
		}
	}

	// On Windows, this may be launching as a service or with an option to
	// register the service.
	stop, runAsService, err := initService(daemonCli)
	if err != nil {
		logrus.Fatal(err)
	}

	if stop {
		return nil
	}

	// If Windows SCM manages the service - no need for PID files
	if runAsService {
		opts.daemonConfig.Pidfile = ""
	}

	err = daemonCli.start(opts)
	notifyShutdown(err)
	return err
}

func showVersion() {
	fmt.Printf("Dockerd version %s, build %s\n", dockerversion.Version, dockerversion.GitCommit)
}

func main() {  //dockerd入口程序在这个main　　　yang add main入口
	if reexec.Init() { //reexec.Init()（在pkg/reexec/reexec.go文件中），看有没有注册的初始化函数，如果有，就直接return了；
		return
	}
	fmt.Printf("yang test ...main... dockerd main")
	// Set terminal emulation based on platform as required.
	_, stdout, stderr := term.StdStreams() //返回标准输入、输出、错误流；

	// @jhowardmsft - maybe there is a historic reason why on non-Windows, stderr is used
	// here. However, on Windows it makes no sense and there is no need.
	if runtime.GOOS == "windows" {
		logrus.SetOutput(stdout)
	} else {
		logrus.SetOutput(stderr)
	}

	cmd := newDaemonCommand()
	cmd.SetOutput(stdout)

	//主流程在这里面执行
	if err := cmd.Execute(); err != nil {  //执行newDaemonCommand中的 runDaemon
		fmt.Fprintf(stderr, "%s\n", err)
		os.Exit(1)
	}
}
