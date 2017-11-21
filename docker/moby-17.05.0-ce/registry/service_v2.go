package registry

import (
	"net/url"
	"strings"

	"github.com/docker/go-connections/tlsconfig"
)

/*
[root@newnamespace ~]# cat /etc/docker/daemon.json
{"registry-mirrors": ["http://a9e61d46.m.daocloud.io"]}

例如:docker pull abcdef:456456   如果没有指定url，则默认优先使用daemon.json中的配置registry-mirrors，如果该地址连不上或者无镜像，则从默认的registry-1.docker.io获取，并且都是V2
Trying to pull abcdef from http://a9e61d46.m.daocloud.io/ v2
Trying to pull abcdef from https://registry-1.docker.io v2

例如： docker pull abc.com/xx/abcdef:456456  如果指定有url，则可以通过V2和V1从指定的url获取镜像
Trying to pull abc.com/xx/abcdef from https://abc.com v2
Trying to pull abc.com/xx/abcdef from https://abc.com v1
*/

// hostname为//docker pull harbor.intra.XXX.com/XXX/centos:20150101 hostname对应 harbor.intra.XXX.com
func (s *DefaultService) lookupV2Endpoints(hostname string) (endpoints []APIEndpoint, err error) {
	tlsConfig := tlsconfig.ServerDefault()
	//如果是官方的 docker.io 或者 index.docker.io
	if hostname == DefaultNamespace || hostname == IndexHostname {
		// v2 mirrors
		for _, mirror := range s.config.Mirrors {
			if !strings.HasPrefix(mirror, "http://") && !strings.HasPrefix(mirror, "https://") {
				mirror = "https://" + mirror
			}
			mirrorURL, err := url.Parse(mirror)
			if err != nil {
				return nil, err
			}
			mirrorTLSConfig, err := s.tlsConfigForMirror(mirrorURL)
			if err != nil {
				return nil, err
			}
			endpoints = append(endpoints, APIEndpoint{
				URL: mirrorURL,
				// guess mirrors are v2
				Version:      APIVersion2,
				Mirror:       true,
				TrimHostname: true,
				TLSConfig:    mirrorTLSConfig,
			})
		}
		// v2 registry
		endpoints = append(endpoints, APIEndpoint{
			URL:          DefaultV2Registry,
			Version:      APIVersion2,
			Official:     true,
			TrimHostname: true,
			TLSConfig:    tlsConfig,
		})

		return endpoints, nil  //这里返回
	}

	tlsConfig, err = s.tlsConfig(hostname)
	if err != nil {
		return nil, err
	}

	endpoints = []APIEndpoint{ //hostname的https v2请求
		{
			URL: &url.URL{
				Scheme: "https",
				Host:   hostname,
			},
			Version:      APIVersion2,
			TrimHostname: true,
			TLSConfig:    tlsConfig,
		},
	}

	if tlsConfig.InsecureSkipVerify { //加上这个配置这允许 http方式访问
		endpoints = append(endpoints, APIEndpoint{  //hostname的http v2请求
			URL: &url.URL{
				Scheme: "http",
				Host:   hostname,
			},
			Version:      APIVersion2,
			TrimHostname: true,
			// used to check if supposed to be secure via InsecureSkipVerify
			TLSConfig: tlsConfig,
		})
	}

	return endpoints, nil
}
