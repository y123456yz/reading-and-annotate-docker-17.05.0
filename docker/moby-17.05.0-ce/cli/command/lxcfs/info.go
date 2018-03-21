package lxcfs

import (
    "fmt"
    
	"github.com/docker/docker/cli"
	"github.com/docker/docker/cli/command"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"
)

type infoOptions struct {
	format string
}


func NewInfoCommand(dockerCli *command.DockerCli) *cobra.Command {

    var opts infoOptions
	cmd := &cobra.Command{
		Use:   "info [OPTIONS]",
		Short: "Display lxcfs information",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInfo(dockerCli, &opts)
		},
	}

	return cmd
}

//NewSystemCommand->NewInfoCommand->runInfo->(cli *Client) Info
func runInfo(dockerCli *command.DockerCli, opts *infoOptions) error {
	ctx := context.Background()

	//(cli *Client) LxcfsInfo
	info, err := dockerCli.Client().LxcfsInfo(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("yang test ... info:%v\n", info)
	return nil
}


