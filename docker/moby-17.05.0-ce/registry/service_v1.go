package registry

import "net/url"

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
func (s *DefaultService) lookupV1Endpoints(hostname string) (endpoints []APIEndpoint, err error) {
	if hostname == DefaultNamespace || hostname == DefaultV2Registry.Host || hostname == IndexHostname {
		return []APIEndpoint{}, nil
	}

	tlsConfig, err := s.tlsConfig(hostname)
	if err != nil {
		return nil, err
	}

	endpoints = []APIEndpoint{
		{
			URL: &url.URL{
				Scheme: "https",
				Host:   hostname,
			},
			Version:      APIVersion1,
			TrimHostname: true,
			TLSConfig:    tlsConfig,
		},
	}

	if tlsConfig.InsecureSkipVerify {
		endpoints = append(endpoints, APIEndpoint{ // or this
			URL: &url.URL{
				Scheme: "http",
				Host:   hostname,
			},
			Version:      APIVersion1,
			TrimHostname: true,
			// used to check if supposed to be secure via InsecureSkipVerify
			TLSConfig: tlsConfig,
		})
	}
	return endpoints, nil
}
