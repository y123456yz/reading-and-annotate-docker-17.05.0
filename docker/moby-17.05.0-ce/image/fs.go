package image

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/pkg/ioutils"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

// DigestWalkFunc is function called by StoreBackend.Walk
type DigestWalkFunc func(id digest.Digest) error

//MetadataStore 是/var/lib/docker/image/devicemapper/layerdb/目录中相关文件操作的接口， 该目录下面的文件内容存储在 layerStore 结构中
//StoreBackend 是/var/lib/docker/image/{driver}/imagedb 目录中相关文件操作的接口， 该目录下面的文件内容存储在 store 结构中
///var/lib/docker/image/{driver}/imagedb/content/sha256/下面的文件和/var/lib/docker/image/devicemapper/layerdb/sha256下面的文件通过(is *store) restore()关联起来

// StoreBackend provides interface for image.Store persistence
//type fs struct{} 结构实现了这些接口  // fs为type fs struct类型结构，实现有 StoreBackend 接口函数信息
type StoreBackend interface { //该接口中的函数具体实现可以参考 NewFSStoreBackend， 具体实也在改fs.go文件中
	Walk(f DigestWalkFunc) error
	Get(id digest.Digest) ([]byte, error)
	Set(data []byte) (digest.Digest, error)
	Delete(id digest.Digest) error
	SetMetadata(id digest.Digest, key string, data []byte) error
	GetMetadata(id digest.Digest, key string) ([]byte, error)
	DeleteMetadata(id digest.Digest, key string) error
}

// fs implements StoreBackend using the filesystem.
type fs struct { //newFSStore 中初始化使用该结构    该接口实现了func (s *fs) contentFile  get 等方法，可以直接赋值给 StoreBackend，参考NewFSStoreBackend
	sync.RWMutex
	root string ///var/lib/docker/image/{driver}/imagedb
}

const (
	contentDirName  = "content"   // /var/lib/docker/image/{driver}/imagedb/content
	metadataDirName = "metadata"  // /var/lib/docker/image/{driver}/imagedb/metadata
)
// image/fs.go   创建仓库后端的文件系统
// NewFSStoreBackend returns new filesystem based backend for image.Store
///var/lib/docker/image/{driver}/imagedb，该目录下主要包含两个目录content和metadata
func NewFSStoreBackend(root string) (StoreBackend, error) {
	/*
	newFSStore返回的是fs类型，为什么可以赋值给StoreBackend接口呢？ 因为下面的 (s *fs) contentFile 等实现有StoreBackend接口的实现
	*/
	return newFSStore(root)
}

///var/lib/docker/image/{driver}/imagedb，该目录下主要包含两个目录content和metadata
func newFSStore(root string) (*fs, error) {
	s := &fs{
		root: root,
	}
	if err := os.MkdirAll(filepath.Join(root, contentDirName, string(digest.Canonical)), 0700); err != nil {
		return nil, errors.Wrap(err, "failed to create storage backend")
	}
	if err := os.MkdirAll(filepath.Join(root, metadataDirName, string(digest.Canonical)), 0700); err != nil {
		return nil, errors.Wrap(err, "failed to create storage backend")
	}
	return s, nil
}

func (s *fs) contentFile(dgst digest.Digest) string {
	return filepath.Join(s.root, contentDirName, string(dgst.Algorithm()), dgst.Hex())
}

// /var/lib/docker/image/{driver}/imagedb/metadata/sha256/$dgst
func (s *fs) metadataDir(dgst digest.Digest) string {
	return filepath.Join(s.root, metadataDirName, string(dgst.Algorithm()), dgst.Hex())
}

// Walk calls the supplied callback for each image ID in the storage backend.
//(is *store) restore() 中执行
//// 遍历/var/lib/docker/image/{driver}/imagedb/content/sha256文件夹中的hex目录获取hex目录名，然后调用f函数执行
func (s *fs) Walk(f DigestWalkFunc) error { ////NewImageStore->(is *store) restore() 中调用walk
	// Only Canonical digest (sha256) is currently supported
	s.RLock()
	dir, err := ioutil.ReadDir(filepath.Join(s.root, contentDirName, string(digest.Canonical)))
	s.RUnlock()
	if err != nil {
		return err
	}

	//扫描/var/lib/docker/image/devicemapper/imagedb/content/sha256 目录的文件夹，判断是否满足Hex要求
	for _, v := range dir {
		//Canonical = "sha256"
		dgst := digest.NewDigestFromHex(string(digest.Canonical), v.Name())
		if err := dgst.Validate(); err != nil {
			logrus.Debugf("skipping invalid digest %s: %s", dgst, err)
			continue
		}
		if err := f(dgst); err != nil {
			return err
		}
	}
	return nil
}

//(is *store) Get(id ID) 中调用  //获取dgst对应的文件内容，通过content返回
// Get returns the content stored under a given digest.
func (s *fs) Get(dgst digest.Digest) ([]byte, error) {
	s.RLock()
	defer s.RUnlock()

	return s.get(dgst)
}

//获取dgst对应的文件内容，通过content返回
func (s *fs) get(dgst digest.Digest) ([]byte, error) {

	//读取/var/lib/docker/image/devicemapper/imagedb/content/sha256/$ID对应的文件内容
	content, err := ioutil.ReadFile(s.contentFile(dgst))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get digest %s", dgst)
	}

	// todo: maybe optional
	if digest.FromBytes(content) != dgst {
		return nil, fmt.Errorf("failed to verify: %v", dgst)
	}

	return content, nil
}

// Set stores content by checksum.
func (s *fs) Set(data []byte) (digest.Digest, error) {
	s.Lock()
	defer s.Unlock()

	if len(data) == 0 {
		return "", fmt.Errorf("invalid empty data")
	}

	dgst := digest.FromBytes(data)
	if err := ioutils.AtomicWriteFile(s.contentFile(dgst), data, 0600); err != nil {
		return "", errors.Wrap(err, "failed to write digest data")
	}

	return dgst, nil
}

// Delete removes content and metadata files associated with the digest.
func (s *fs) Delete(dgst digest.Digest) error {
	s.Lock()
	defer s.Unlock()

	if err := os.RemoveAll(s.metadataDir(dgst)); err != nil {
		return err
	}
	if err := os.Remove(s.contentFile(dgst)); err != nil {
		return err
	}
	return nil
}

// SetMetadata sets metadata for a given ID. It fails if there's no base file.
func (s *fs) SetMetadata(dgst digest.Digest, key string, data []byte) error {
	s.Lock()
	defer s.Unlock()
	if _, err := s.get(dgst); err != nil {
		return err
	}

	baseDir := filepath.Join(s.metadataDir(dgst))
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return err
	}
	return ioutils.AtomicWriteFile(filepath.Join(s.metadataDir(dgst), key), data, 0600)
}

// GetMetadata returns metadata for a given digest.
// (is *store) GetParent( 中执行
// 读取/var/lib/docker/image/{driver}/imagedb/metadata/sha256/$dgst/$key 中的内容通过byte返回
func (s *fs) GetMetadata(dgst digest.Digest, key string) ([]byte, error) {
	s.RLock()
	defer s.RUnlock()

	//读取/var/lib/docker/image/devicemapper/imagedb/content/sha256/$ID对应的文件内容，如果没有改文件，直接返回Nil
	if _, err := s.get(dgst); err != nil {
		return nil, err
	}
	bytes, err := ioutil.ReadFile(filepath.Join(s.metadataDir(dgst), key))
	if err != nil {
		return nil, errors.Wrap(err, "failed to read metadata")
	}
	return bytes, nil
}

// DeleteMetadata removes the metadata associated with a digest.
func (s *fs) DeleteMetadata(dgst digest.Digest, key string) error {
	s.Lock()
	defer s.Unlock()

	return os.RemoveAll(filepath.Join(s.metadataDir(dgst), key))
}
