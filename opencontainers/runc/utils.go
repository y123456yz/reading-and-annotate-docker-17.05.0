package main

import (
	"fmt"
	"os"

	"github.com/Sirupsen/logrus"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
	"github.com/opencontainers/runc/Godeps/_workspace/src/github.com/opencontainers/runtime-spec/specs-go"
)

// fatal prints the error's details if it is a libcontainer specific error type
// then exits the program with an exit status of 1.
func fatal(err error) {
	// make sure the error is written to the logger
	logrus.Error(err)
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// setupSpec performs initial setup based on the cli.Context for the container
/*
首先调用spec, err := setupSpec(context)加载配置文件config.json的内容。
*/
func setupSpec(context *cli.Context) (*specs.Spec, error) {
	//bundle指定bundle目录，默认为当前目录  //runc  create -b /home/XXX/lxc/yyztest mytest -b参数指定
	bundle := context.String("bundle")  //bundle指定bundle目录，默认为当前目录
	if bundle != "" {
		if err := os.Chdir(bundle); err != nil {
			return nil, err
		}
	}
	spec, err := loadSpec(specConfig)
	if err != nil {
		return nil, err
	}
	notifySocket := os.Getenv("NOTIFY_SOCKET")
	if notifySocket != "" {
		setupSdNotify(spec, notifySocket)
	}
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("runc should be run as root")
	}
	return spec, nil
}
