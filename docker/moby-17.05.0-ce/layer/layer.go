// Package layer is package for managing read-only
// and read-write mounts on the union file system
// driver. Read-only mounts are referenced using a
// content hash and are protected from mutation in
// the exposed interface. The tar format is used
// to create read-only layers and export both
// read-only and writable layers. The exported
// tar data for a read-only layer should match
// the tar used to create the layer.
package layer

import (
	"errors"
	"io"

	"github.com/Sirupsen/logrus"
	"github.com/docker/distribution"
	"github.com/docker/docker/pkg/archive"
	"github.com/opencontainers/go-digest"
)

var (
	// ErrLayerDoesNotExist is used when an operation is
	// attempted on a layer which does not exist.
	ErrLayerDoesNotExist = errors.New("layer does not exist")

	// ErrLayerNotRetained is used when a release is
	// attempted on a layer which is not retained.
	ErrLayerNotRetained = errors.New("layer not retained")

	// ErrMountDoesNotExist is used when an operation is
	// attempted on a mount layer which does not exist.
	ErrMountDoesNotExist = errors.New("mount does not exist")

	// ErrMountNameConflict is used when a mount is attempted
	// to be created but there is already a mount with the name
	// used for creation.
	ErrMountNameConflict = errors.New("mount already exists with name")

	// ErrActiveMount is used when an operation on a
	// mount is attempted but the layer is still
	// mounted and the operation cannot be performed.
	ErrActiveMount = errors.New("mount still active")

	// ErrNotMounted is used when requesting an active
	// mount but the layer is not mounted.
	ErrNotMounted = errors.New("not mounted")

	// ErrMaxDepthExceeded is used when a layer is attempted
	// to be created which would result in a layer depth
	// greater than the 125 max.
	ErrMaxDepthExceeded = errors.New("max depth exceeded")

	// ErrNotSupported is used when the action is not supported
	// on the current platform
	ErrNotSupported = errors.New("not support on this platform")
)

// ChainID is the content-addressable ID of a layer.

/*镜像xxxx的属性信息存在下面这里：  参考https://segmentfault.com/a/1190000009730986
/var/lib/docker/image/devicemapper/imagedb/content/sha256/xxxx
chainID计算过程：假设某个镜像diff_ids如下  cat /var/lib/docker/image/devicemapper/imagedb/content/sha256/xxxx | jq .
  ....
  "rootfs": {
    "type": "layers",
    "diff_ids": [
      "sha256:51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62",  #1
      "sha256:5792d8202a821076989a52ced68d1382fc0596f937e7808abbd5ffc1db93fffb",  #2
      "sha256:b7bbef1946d74cdfd84b0db815b4fe9fc9405451190aa65b9eab6ae198c560b4",  #3
    ]
  }

      镜像xxxx包含三层只读层，每层的diff_id如上。
      docker计算chainid时，用到了所有祖先layer的信息，从而能保证根据chainid得到的rootfs是唯一的。比如我在debian和ubuntu的image基
  础上都添加了一个同样的文件，那么commit之后新增加的这两个layer具有相同的内容，相同的diffid，但由于他们的父layer不一样，所以他们
  的chainid会不一样，从而根据chainid能找到唯一的rootfs。
      docker通过#1 #2 #3从仓库里面拉取各层内容的时候，存放在那呢？例如#1对应的只读层内容存到哪里？给每层计算一个chainid，然后在把该层
  相关内容记录到/var/lib/docker/image/devicemapper/layerdb/sha256/$chainID目录下的相关文件。
      #1的chainID就是#1的sha256,因为他没有parent父层ID，它就是最底层。
      root@fd-mesos-xxx.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ ls 51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62/
	cache-id  diff  size  tar-split.json.gz   //注意没有parent文件
      root@fd-mesos-xxx.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$cat 51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62/diff
	sha256:51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62 //diff和diff_ids第一层一样


      #2的chainID计算方法：(父层的chainID(第一层chainID)和第二层的diff_id计算sha256sum的结果)
      root@fd-mesos-xxx.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ echo -n "sha256:51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62 sha256:5792d8202a821076989a52ced68d1382fc0596f937e7808abbd5ffc1db93fffb" | sha256sum
      e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445  -
      root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ cat e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445/diff
      sha256:5792d8202a821076989a52ced68d1382fc0596f937e7808abbd5ffc1db93fffb  //diff内容就和diff_ids第二层一样
      root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$
      root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ cat e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445/parent
      sha256:51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62  //parent内容就是第一层chainID
      root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$
      root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$

      #3的chainID计算方法：(父层的chainID(第二层chainID)和第三层的diff_id计算sha256sum的结果)
        root@fd-mesos-xxx.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ echo -n "sha256:e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445 sha256:b7bbef1946d74cdfd84b0db815b4fe9fc9405451190aa65b9eab6ae198c560b4" | sha256sum
	c6c38436b063046117fb9b4210a54c0d29aa8b5f350964d1723468e6a324e1a8  -
	root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ ls c6c38436b063046117fb9b4210a54c0d29aa8b5f350964d1723468e6a324e1a8/
	cache-id  diff  parent  size  tar-split.json.gz
	root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ cat c6c38436b063046117fb9b4210a54c0d29aa8b5f350964d1723468e6a324e1a8/parent
	sha256:e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445  //parent内容就是第二层chainID
	root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$
	root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ cat c6c38436b063046117fb9b4210a54c0d29aa8b5f350964d1723468e6a324e1a8/diff
	sha256:b7bbef1946d74cdfd84b0db815b4fe9fc9405451190aa65b9eab6ae198c560b4 //diff内容就和diff_ids第二层一样
	root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$
*/

//ChainID 通过 createChainIDFromParent 计算得到，根据/var/lib/docker/image/devicemapper/imagedb/content/sha256/XXX文件内容中的diff_ids递归计算得到，
// 见createChainIDFromParent,计算出的chainID和/var/lib/docker/image/devicemapper/layerdb/sha256目录下的文件夹名相同，对应一个镜像
//可以参考 https://segmentfault.com/a/1190000009730986
type ChainID digest.Digest
//ChainID 通过 createChainIDFromParent 计算得到，根据/var/lib/docker/image/devicemapper/imagedb/content/sha256/XXX文件内容中的diff_ids递归计算得到，见 createChainIDFromParent

// String returns a string rendition of a layer ID
func (id ChainID) String() string {
	return string(id)
}

//digest.Digest类型，其实都是string类型,都是sha256:uuid，uuid为64个16进制数
// DiffID is the hash of an individual layer tar.
type DiffID digest.Digest  //type Digest string

// String returns a string rendition of a layer DiffID
func (diffID DiffID) String() string {
	return string(diffID)
}

// TarStreamer represents an object which may
// have its contents exported as a tar stream.
type TarStreamer interface {
	// TarStream returns a tar archive stream
	// for the contents of a layer.
	TarStream() (io.ReadCloser, error)
}

// Layer represents a read-only layer
/*
docker中定义了 Layer 和 RWLayer 两种接口，分别用来定义只读层和可读写层的一些操作，又定义了 roLayer 和 mountedLayer,分别实现这两种接口。
其中 roLayer 用于藐视不可改变的镜像层，mountedLayer 用于藐视可读写的容器层
*/
type Layer interface { //roLayer 中实现这些方法
	TarStreamer

	// TarStreamFrom returns a tar archive stream for all the layer chain with
	// arbitrary depth.
	TarStreamFrom(ChainID) (io.ReadCloser, error)

	// ChainID returns the content hash of the entire layer chain. The hash
	// chain is made up of DiffID of top layer and all of its parents.
	ChainID() ChainID

	// DiffID returns the content hash of the layer
	// tar stream used to create this layer.
	DiffID() DiffID

	// Parent returns the next layer in the layer chain.
	Parent() Layer

	// Size returns the size of the entire layer chain. The size
	// is calculated from the total size of all files in the layers.
	Size() (int64, error)

	// DiffSize returns the size difference of the top layer
	// from parent layer.
	DiffSize() (int64, error)

	// Metadata returns the low level storage metadata associated
	// with layer.
	Metadata() (map[string]string, error)
}

// RWLayer represents a layer which is
// read and writable
/*
docker中定义了 Layer 和 RWLayer 两种接口，分别用来定义只读层和可读写层的一些操作，又定义了roLayer和mountedLayer,分别实现这两种接口。
其中 roLayer 用于表视不可改变的镜像层，mountedLayer 用于表视可读写的容器层
*/
type RWLayer interface { //mountedLayer 中实现
	TarStreamer

	// Name of mounted layer
	Name() string

	// Parent returns the layer which the writable
	// layer was created from.
	Parent() Layer

	// Mount mounts the RWLayer and returns the filesystem path
	// the to the writable layer.
	Mount(mountLabel string) (string, error)

	// Unmount unmounts the RWLayer. This should be called
	// for every mount. If there are multiple mount calls
	// this operation will only decrement the internal mount counter.
	Unmount() error

	// Size represents the size of the writable layer
	// as calculated by the total size of the files
	// changed in the mutable layer.
	Size() (int64, error)

	// Changes returns the set of changes for the mutable layer
	// from the base layer.
	Changes() ([]archive.Change, error)

	// Metadata returns the low level metadata for the mutable layer
	Metadata() (map[string]string, error)
}

// Metadata holds information about a
// read-only layer
type Metadata struct {
	// ChainID is the content hash of the layer
	ChainID ChainID

	// DiffID is the hash of the tar data used to
	// create the layer
	DiffID DiffID

	// Size is the size of the layer and all parents
	Size int64

	// DiffSize is the size of the top layer
	DiffSize int64
}

// MountInit is a function to initialize a
// writable mount. Changes made here will
// not be included in the Tar stream of the
// RWLayer.
type MountInit func(root string) error

// CreateRWLayerOpts contains optional arguments to be passed to CreateRWLayer
type CreateRWLayerOpts struct { //使用见 setRWLayer
	MountLabel string
	InitFunc   MountInit   //linux 对应 daemon.setupInitLayer   initMount中执行
	StorageOpt map[string]string
}

// Store represents a backend for managing both
// read-only and read-write layers.
type Store interface {  //layerStore 结构会实现这些方法
	Register(io.Reader, ChainID) (Layer, error)
	Get(ChainID) (Layer, error)
	Map() map[ChainID]Layer
	Release(Layer) ([]Metadata, error)

	//create.go中的setRWLayer函数调用
	CreateRWLayer(id string, parent ChainID, opts *CreateRWLayerOpts) (RWLayer, error)
	GetRWLayer(id string) (RWLayer, error)
	GetMountID(id string) (string, error)
	ReleaseRWLayer(RWLayer) ([]Metadata, error)

	Cleanup() error
	DriverStatus() [][2]string
	DriverName() string // 取layer里面的graphDriver
}

// DescribableStore represents a layer store capable of storing
// descriptors for layers.
type DescribableStore interface {
	RegisterWithDescriptor(io.Reader, ChainID, distribution.Descriptor) (Layer, error)
}

// MetadataTransaction represents functions for setting layer metadata
// with a single transaction.
type MetadataTransaction interface {
	SetSize(int64) error
	SetParent(parent ChainID) error
	SetDiffID(DiffID) error
	SetCacheID(string) error
	SetDescriptor(distribution.Descriptor) error
	TarSplitWriter(compressInput bool) (io.WriteCloser, error)

	Commit(ChainID) error
	Cancel() error
	String() string
}

// MetadataStore represents a backend for persisting
// metadata about layers and providing the metadata
// for restoring a Store.
//MetadataStore为接口，主要为获得层基本信息的方法。 metadata 是这个层的额外信息，不仅能够让docker获取运行和构建的信息，也包括父层的层次信息（只读层和读写层都包含元数据）。

//MetadataStore 是/var/lib/docker/image/devicemapper/layerdb/目录中相关文件操作的接口， 该目录下面的文件内容存储在 layerStore 结构中
//StoreBackend 是/var/lib/docker/image/{driver}/imagedb 目录中相关文件操作的接口， 该目录下面的文件内容存储在 store 结构中
///var/lib/docker/image/{driver}/imagedb/content/sha256/下面的文件和/var/lib/docker/image/devicemapper/layerdb/sha256下面的文件通过(is *store) restore()关联起来

//fileMetadataStore 包含该接口成员实现   fileMetadataStore  fileMetadataStore
type MetadataStore interface { //相应接口实现可以参考 NewStoreFromOptions->NewStoreFromGraphDriver
	// StartTransaction starts an update for new metadata
	// which will be used to represent an ID on commit.
	StartTransaction() (MetadataTransaction, error)

	//loadLayer 中执行
	GetSize(ChainID) (int64, error)
	GetParent(ChainID) (ChainID, error)
	GetDiffID(ChainID) (DiffID, error)  //loadLayer 中执行
	GetCacheID(ChainID) (string, error)
	GetDescriptor(ChainID) (distribution.Descriptor, error)
	TarSplitReader(ChainID) (io.ReadCloser, error)

	SetMountID(string, string) error
	SetInitID(string, string) error
	SetMountParent(string, ChainID) error

	//loadMount中执行
	GetMountID(string) (string, error)
	GetInitID(string) (string, error)
	GetMountParent(string) (ChainID, error)

	// List returns the full list of referenced
	// read-only and read-write layers
	List() ([]ChainID, []string, error)  //NewStoreFromGraphDriver 中执行

	Remove(ChainID) error
	RemoveMount(string) error
}

// CreateChainID returns ID for a layerDigest slice
//(is *store) restore() 中调用执行
func CreateChainID(dgsts []DiffID) ChainID { //根据DiffID数组算出ChainID
	return createChainIDFromParent("", dgsts...)
}
/*
 镜像xxxx的属性信息存在下面这里：  参考https://segmentfault.com/a/1190000009730986
/var/lib/docker/image/devicemapper/imagedb/content/sha256/xxxx
chainID计算过程：假设某个镜像diff_ids如下  cat /var/lib/docker/image/devicemapper/imagedb/content/sha256/xxxx | jq .
  ....
  "rootfs": {
    "type": "layers",
    "diff_ids": [
      "sha256:51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62",  #1
      "sha256:5792d8202a821076989a52ced68d1382fc0596f937e7808abbd5ffc1db93fffb",  #2
      "sha256:b7bbef1946d74cdfd84b0db815b4fe9fc9405451190aa65b9eab6ae198c560b4",  #3
    ]
  }

      镜像xxxx包含三层只读层，每层的diff_id如上。
      docker计算chainid时，用到了所有祖先layer的信息，从而能保证根据chainid得到的rootfs是唯一的。比如我在debian和ubuntu的image基
  础上都添加了一个同样的文件，那么commit之后新增加的这两个layer具有相同的内容，相同的diffid，但由于他们的父layer不一样，所以他们
  的chainid会不一样，从而根据chainid能找到唯一的rootfs。
      docker通过#1 #2 #3从仓库里面拉取各层内容的时候，存放在那呢？例如#1对应的只读层内容存到哪里？给每层计算一个chainid，然后在把该层
  相关内容记录到/var/lib/docker/image/devicemapper/layerdb/sha256/$chainID目录下的相关文件。
      #1的chainID就是#1的sha256,因为他没有parent父层ID，它就是最底层。
      root@fd-mesos-xxx.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ ls 51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62/
	cache-id  diff  size  tar-split.json.gz   //注意没有parent文件
      root@fd-mesos-xxx.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$cat 51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62/diff
	sha256:51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62 //diff和diff_ids第一层一样


      #2的chainID计算方法：(父层的chainID(第一层chainID)和第二层的diff_id计算sha256sum的结果)
      root@fd-mesos-xxx.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ echo -n "sha256:51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62 sha256:5792d8202a821076989a52ced68d1382fc0596f937e7808abbd5ffc1db93fffb" | sha256sum
      e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445  -
      root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ cat e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445/diff
      sha256:5792d8202a821076989a52ced68d1382fc0596f937e7808abbd5ffc1db93fffb  //diff内容就和diff_ids第二层一样
      root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$
      root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ cat e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445/parent
      sha256:51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62  //parent内容就是第一层chainID
      root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$
      root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$

      #3的chainID计算方法：(父层的chainID(第二层chainID)和第三层的diff_id计算sha256sum的结果)
        root@fd-mesos-xxx.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ echo -n "sha256:e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445 sha256:b7bbef1946d74cdfd84b0db815b4fe9fc9405451190aa65b9eab6ae198c560b4" | sha256sum
	c6c38436b063046117fb9b4210a54c0d29aa8b5f350964d1723468e6a324e1a8  -
	root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ ls c6c38436b063046117fb9b4210a54c0d29aa8b5f350964d1723468e6a324e1a8/
	cache-id  diff  parent  size  tar-split.json.gz
	root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ cat c6c38436b063046117fb9b4210a54c0d29aa8b5f350964d1723468e6a324e1a8/parent
	sha256:e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445  //parent内容就是第二层chainID
	root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$
	root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ cat c6c38436b063046117fb9b4210a54c0d29aa8b5f350964d1723468e6a324e1a8/diff
	sha256:b7bbef1946d74cdfd84b0db815b4fe9fc9405451190aa65b9eab6ae198c560b4 //diff内容就和diff_ids第二层一样
	root@fd-mesos-main04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$
*/
 //根据parent 和 dgsts 算出一个ChainID   参考https://segmentfault.com/a/1190000009730986
//这里是算出最顶层的 b7bbef1946d74cdfd84b0db815b4fe9fc9405451190aa65b9eab6ae198c560b4 diff_id对应的chainID对应,也就是上面的#3的chainID
func createChainIDFromParent(parent ChainID, dgsts ...DiffID) ChainID {
	if len(dgsts) == 0 {
		return parent
	}
	if parent == "" {
		return createChainIDFromParent(ChainID(dgsts[0]), dgsts[1:]...)
	}
	// H = "H(n-1) SHA256(n)"
	dgst := digest.FromBytes([]byte(string(parent) + " " + string(dgsts[0])))
	return createChainIDFromParent(ChainID(dgst), dgsts[1:]...)
}

// ReleaseAndLog releases the provided layer from the given layer
// store, logging any error and release metadata
func ReleaseAndLog(ls Store, l Layer) {
	metadata, err := ls.Release(l)
	if err != nil {
		logrus.Errorf("Error releasing layer %s: %v", l.ChainID(), err)
	}
	LogReleaseMetadata(metadata)
}

// LogReleaseMetadata logs a metadata array, uses this to
// ensure consistent logging for release metadata
func LogReleaseMetadata(metadatas []Metadata) {
	for _, metadata := range metadatas {
		logrus.Infof("Layer %s cleaned up", metadata.ChainID)
	}
}
