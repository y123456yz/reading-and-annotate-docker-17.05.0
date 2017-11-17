package reference

import (
	"errors"
	"fmt"
	"strings"

	"github.com/docker/distribution/digestset"
	"github.com/opencontainers/go-digest"
)

var (
	legacyDefaultDomain = "index.docker.io"
	defaultDomain       = "docker.io"
	officialRepoName    = "library"
	defaultTag          = "latest"
)

// normalizedNamed represents a name which has been
// normalized and has a familiar form. A familiar name
// is what is used in Docker UI. An example normalized
// name is "docker.io/library/ubuntu" and corresponding
// familiar name of "ubuntu".
type normalizedNamed interface {
	Named
	Familiar() Named
}

// ParseNormalizedNamed parses a string into a named reference
// transforming a familiar name from Docker UI to a fully
// qualified reference. If the value may be an identifier
// use ParseAnyReference.
//docker pull [OPTIONS] NAME[:TAG|@DIGEST]
//name格式: xxx:yyy | @zzz  xxx 代表镜像名,如果没有加上仓库地址:docker.io，会使用默认的仓库地址， yyy ：代表版本tag zzz： 代表摘要
//获取仓库项目url全路径地址，例如harbor.XXX.xxx.com/xxx/centos:201708101210  docker.io/library/redis:latest(docker pull redis,则默认加上docker.io/library/xxx:latest)
func ParseNormalizedNamed(s string) (Named, error) { //ImagePull 中会调用
	if ok := anchoredIdentifierRegexp.MatchString(s); ok {
		return nil, fmt.Errorf("invalid repository name (%s), cannot specify 64-byte hexadecimal strings", s)
	}

	//domain表示url，remainder为url后面的仓库用户名和项目镜像名称及版本tag
	//例如name:harbor.XXX.xxx.com/xxx/centos:201708101210    domain:harbor.XXX.xxx.com    remainder:xxx/centos:201708101210
	domain, remainder := splitDockerDomain(s)
	var remoteName string
	if tagSep := strings.IndexRune(remainder, ':'); tagSep > -1 {
		remoteName = remainder[:tagSep]
	} else {
		remoteName = remainder
	}
	if strings.ToLower(remoteName) != remoteName { //remainder必须小写
		return nil, errors.New("invalid reference format: repository name must be lowercase")
	}

	ref, err := Parse(domain + "/" + remainder)
	if err != nil {
		return nil, err
	}
	named, isNamed := ref.(Named)
	if !isNamed {
		return nil, fmt.Errorf("reference %s has no name", ref.String())
	}

	//harbor.XXX.xxx.com/xxx/centos:201708101210
	return named, nil
}

// splitDockerDomain splits a repository name to domain and remotename string.
// If no valid domain is found, the default domain is used. Repository name
// needs to be already validated before.
/*
[root@newnamespace bundle]# ./latest/binary-client/docker images
yang test ..name:redis:latest domain:docker.io remainder:library/redis:latest
yang test ..name:redis@sha256:1145aa525c0c651fd69276e2918dd4e0ecbb4844209a3e3fe7992adb464532c6 domain:docker.io remainder:library/redis@sha256:1145aa525c0c651fd69276e2918dd4e0ecbb4844209a3e3fe7992adb464532c6
yang test ..name:busybox:latest domain:docker.io remainder:library/busybox:latest
yang test ..name:busybox@sha256:e3789c406237e25d6139035a17981be5f1ccdae9c392d1623a02d31621a12bcc domain:docker.io remainder:library/busybox@sha256:e3789c406237e25d6139035a17981be5f1ccdae9c392d1623a02d31621a12bcc
yang test ..name:harbor.intra.xxx.com/xxx/centos:201708101210 domain:harbor.intra.xxx.com remainder:oceanbank/centos:201708101210
yang test ..name:harbor.intra.xxx.com/xxx/centos@sha256:0523162a0b344143315c28ca9d02cd15d232975a6809c6832e8d9c416ec48922 domain:harbor.intra.xxx.com remainder:oceanbank/centos@sha256:0523162a0b344143315c28ca9d02cd15d232975a6809c6832e8d9c416ec48922
yang test ..name:leijitang/dockercore-docker:latest domain:docker.io remainder:leijitang/dockercore-docker:latest
yang test ..name:leijitang/dockercore-docker@sha256:fa08b63a9d32482fca2a441b2b70b155208b3ad7baa1986765e0acb42bc4c64b domain:docker.io remainder:leijitang/dockercore-docker@sha256:fa08b63a9d32482fca2a441b2b70b155208b3ad7baa1986765e0acb42bc4c64b
yang test ..name:leijitang/dockercore-docker:v17.05.0 domain:docker.io remainder:leijitang/dockercore-docker:v17.05.0
yang test ..name:leijitang/dockercore-docker@sha256:0779f0354297f1376b1b4aedb7884a356f77431fe9cd9f7c4466a0a5ff73b02e domain:docker.io remainder:leijitang/dockercore-docker@sha256:0779f0354297f1376b1b4aedb7884a356f77431fe9cd9f7c4466a0a5ff73b02e
yang test ..name:leijitang/dockercore-docker@sha256:778d802fc65c157cec89176efeb6c76b7bff8eebbe510c4272ef41cfc63d8c03 domain:docker.io remainder:leijitang/dockercore-docker@sha256:778d802fc65c157cec89176efeb6c76b7bff8eebbe510c4272ef41cfc63d8c03
yang test ..name:dockercore/docker:latest domain:docker.io remainder:dockercore/docker:latest
yang test ..name:dockercore/docker@sha256:e1d0740fe09e38f412987a72d684975609cb2ae708d259724c1daa2d092e87ac domain:docker.io remainder:dockercore/docker@sha256:e1d0740fe09e38f412987a72d684975609cb2ae708d259724c1daa2d092e87ac

REPOSITORY                                     TAG                 IMAGE ID            CREATED             SIZE
redis                                          latest              8f2e175b3bd1        12 days ago         107MB
busybox                                        latest              6ad733544a63        13 days ago         1.13MB
harbor.intra.xxx.com/xxx/centos   201708101210        fb47fe6aa16c        3 months ago        4.52GB
leijitang/dockercore-docker                    latest              d80c06ccafb1        3 months ago        1.79GB
leijitang/dockercore-docker                    v17.05.0            69f833c62773        6 months ago        2.31GB
dockercore/docker                              latest              11ec050c92f3        7 months ago        2.31GB
*/
//domain表示url，remainder为url后面的仓库用户名和项目镜像名称及版本tag
//例如name:harbor.intra.xxx.com/xxx/centos:201708101210    domain:harbor.intra.xxx.com    remainder:xxx/centos:201708101210
func splitDockerDomain(name string) (domain, remainder string) {
	i := strings.IndexRune(name, '/')
	if i == -1 || (!strings.ContainsAny(name[:i], ".:") && name[:i] != "localhost") { //第一个/前面没有.字符，说明没有携带url,则默认加上docker.io
		//如果没有
		domain, remainder = defaultDomain, name
	} else {
		domain, remainder = name[:i], name[i+1:]
	}
	if domain == legacyDefaultDomain {
		domain = defaultDomain
	}
	if domain == defaultDomain && !strings.ContainsRune(remainder, '/') {
		remainder = officialRepoName + "/" + remainder
	}

	//fmt.Printf("yang test .. %s  %s\n\n", domain, remainder);
	return
}

// familiarizeName returns a shortened version of the name familiar
// to to the Docker UI. Familiar names have the default domain
// "docker.io" and "library/" repository prefix removed.
// For example, "docker.io/library/redis" will have the familiar
// name "redis" and "docker.io/dmcgowan/myapp" will be "dmcgowan/myapp".
// Returns a familiarized named only reference.
func familiarizeName(named namedRepository) repository {
	repo := repository{
		domain: named.Domain(),
		path:   named.Path(),
	}

	if repo.domain == defaultDomain {
		repo.domain = ""
		// Handle official repositories which have the pattern "library/<official repo name>"
		if split := strings.Split(repo.path, "/"); len(split) == 2 && split[0] == officialRepoName {
			repo.path = split[1]
		}
	}
	return repo
}

func (r reference) Familiar() Named {
	return reference{
		namedRepository: familiarizeName(r.namedRepository),
		tag:             r.tag,
		digest:          r.digest,
	}
}

func (r repository) Familiar() Named {
	return familiarizeName(r)
}

func (t taggedReference) Familiar() Named {
	return taggedReference{
		namedRepository: familiarizeName(t.namedRepository),
		tag:             t.tag,
	}
}

func (c canonicalReference) Familiar() Named {
	return canonicalReference{
		namedRepository: familiarizeName(c.namedRepository),
		digest:          c.digest,
	}
}

// TagNameOnly adds the default tag "latest" to a reference if it only has
// a repo name.
func TagNameOnly(ref Named) Named {
	if IsNameOnly(ref) {
		namedTagged, err := WithTag(ref, defaultTag)
		if err != nil {
			// Default tag must be valid, to create a NamedTagged
			// type with non-validated input the WithTag function
			// should be used instead
			panic(err)
		}
		return namedTagged
	}
	return ref
}

// ParseAnyReference parses a reference string as a possible identifier,
// full digest, or familiar name.
func ParseAnyReference(ref string) (Reference, error) {
	if ok := anchoredIdentifierRegexp.MatchString(ref); ok {
		return digestReference("sha256:" + ref), nil
	}
	if dgst, err := digest.Parse(ref); err == nil {
		return digestReference(dgst), nil
	}

	return ParseNormalizedNamed(ref)
}

// ParseAnyReferenceWithSet parses a reference string as a possible short
// identifier to be matched in a digest set, a full digest, or familiar name.
func ParseAnyReferenceWithSet(ref string, ds *digestset.Set) (Reference, error) {
	if ok := anchoredShortIdentifierRegexp.MatchString(ref); ok {
		dgst, err := ds.Lookup(ref)
		if err == nil {
			return digestReference(dgst), nil
		}
	} else {
		if dgst, err := digest.Parse(ref); err == nil {
			return digestReference(dgst), nil
		}
	}

	return ParseNormalizedNamed(ref)
}
