package client

import (
	"io"
	"net/http"
	"net/url"

	"golang.org/x/net/context"

	"github.com/docker/distribution/reference"
	"github.com/docker/docker/api/types"
)

// ImagePull requests the docker host to pull an image from a remote registry.
// It executes the privileged function if the operation is unauthorized
// and it tries one more time.
// It's up to the caller to handle the io.ReadCloser and close it properly.
//
// FIXME(vdemeester): there is currently used in a few way in docker/docker
// - if not in trusted content, ref is used to pass the whole reference, and tag is empty
// - if in trusted content, ref is used to pass the reference name, and tag for the digest
//客户端 (cli *Client) ImagePull 和 服务端  r.postImagesCreate) 对应
func (cli *Client) ImagePull(ctx context.Context, refStr string, options types.ImagePullOptions) (io.ReadCloser, error) {
	//获取仓库项目url全路径地址，例如harbor.XXX.xxx.com/xxx/centos:201708101210
	//docker.io/library/redis:latest(docker pull redis,则默认加上docker.io/library/xxx:latest)
	ref, err := reference.ParseNormalizedNamed(refStr)
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	//harbor.intra.xxxx.com/xxxx/centos:XXX 去掉:后面的tag，变为harbor.intra.xxxx.com/xxxx/centos
	query.Set("fromImage", reference.FamiliarName(ref))
	if !options.All {
		query.Set("tag", getAPITagFromNamedRef(ref)) //tag记录在这里，也就是上面的XXX
	}

	//发送post  /images/create 请求
	resp, err := cli.tryImageCreate(ctx, query, options.RegistryAuth)
	if resp.statusCode == http.StatusUnauthorized && options.PrivilegeFunc != nil {
		newAuthHeader, privilegeErr := options.PrivilegeFunc()
		if privilegeErr != nil {
			return nil, privilegeErr
		}
		resp, err = cli.tryImageCreate(ctx, query, newAuthHeader)
	}
	if err != nil {
		return nil, err
	}
	return resp.body, nil
}

// getAPITagFromNamedRef returns a tag from the specified reference.
// This function is necessary as long as the docker "server" api expects
// digests to be sent as tags and makes a distinction between the name
// and tag/digest part of a reference.
func getAPITagFromNamedRef(ref reference.Named) string {
	if digested, ok := ref.(reference.Digested); ok {
		return digested.Digest().String()
	}
	ref = reference.TagNameOnly(ref)
	if tagged, ok := ref.(reference.Tagged); ok {
		return tagged.Tag()
	}
	return ""
}
