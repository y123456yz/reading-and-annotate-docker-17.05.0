package client

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/docker/docker/api/types"
	"golang.org/x/net/context"
)

func (cli *Client) LxcfsInfo(ctx context.Context) (types.LxcfsInfo, error) {
	var info types.LxcfsInfo
	//(cli *Client) get
	serverResp, err := cli.get(ctx, "/lxcfs/info", url.Values{}, nil)
	if err != nil {
		return info, err
	}
	defer ensureReaderClosed(serverResp)

	if err := json.NewDecoder(serverResp.body).Decode(&info); err != nil {
		return info, fmt.Errorf("Error reading remote lxcfs info: %v", err)
	}

	return info, nil
}

