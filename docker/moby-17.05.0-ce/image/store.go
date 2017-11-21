package image

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/docker/distribution/digestset"
	"github.com/docker/docker/layer"
	"github.com/opencontainers/go-digest"
)

// Store is an interface for creating and accessing images
type Store interface { //imageConfigStore 包含该接口，赋值见 pullImageWithReference->NewImageConfigStoreFromStore
	Create(config []byte) (ID, error)
	Get(id ID) (*Image, error)
	Delete(id ID) ([]layer.Metadata, error)
	Search(partialID string) (ID, error)
	SetParent(id ID, parent ID) error
	GetParent(id ID) (ID, error)
	Children(id ID) []ID
	Map() map[ID]*Image
	Heads() map[ID]*Image
}

// LayerGetReleaser is a minimal interface for getting and releasing images.
//LayerGetReleaser中的接口从 Daemon.layerStore 获得 ,见 NewImageStore
type LayerGetReleaser interface { //下面的store包含该结构
	Get(layer.ChainID) (layer.Layer, error)
	Release(layer.Layer) ([]layer.Metadata, error)
}

type imageMeta struct { //(is *store) restore()中使用该类
	//imageMeta包含一个layer成员，docker image是由多个只读的roLayer构成，而这里的layer就是最上层的layer。
	layer    layer.Layer   //这里的layer实际上是通过上面的is.ls.Get获取到的 referencedCacheLayer 信息
	children map[ID]struct{}  //赋值见(is *store) restore()
}

//imageStore 存放的是各个docker image的信息。imageStore的类型为image.Store，结构体为
//Docker镜像存储相关数据结构参考http://licyhust.com/%E5%AE%B9%E5%99%A8%E6%8A%80%E6%9C%AF/2016/09/27/docker-image-data-structure/

//MetadataStore 是/var/lib/docker/image/devicemapper/layerdb/目录中相关文件操作的接口， 该目录下面的文件内容存储在 layerStore 结构中
//StoreBackend 是/var/lib/docker/image/{driver}/imagedb 目录中相关文件操作的接口， 该目录下面的文件内容存储在 store 结构中。  docker images看到的就是imagedb/content/xxx中的文件夹
///var/lib/docker/image/{driver}/imagedb/content/sha256/下面的文件和/var/lib/docker/image/devicemapper/layerdb/sha256下面的文件通过(is *store) restore()关联起来

type store struct { //初始化赋值见 NewImageStore   该结构类型源头数据存入 Daemon.imageStore   image\store.go
	sync.Mutex
	//ls类型为LayerGetReleaser接口，初始化时将ls初始化为 layerStore。
	ls        LayerGetReleaser
	//images就是每一个镜像的信息  赋值见(is *store) restore()
	images    map[ID]*imageMeta  ///var/lib/docker/image/{driver}/imagedb/content/sha256目录有几个文件，这里就有几个imageMeta
	//fs存放了image的原信息，存储的目录位于/var/lib/docker/image/{driver}/imagedb，该目录下主要包含两个目录content和metadata
	/*
		content目录：content下面的sha256目录下存放了每个docker image的元数据文件，除了制定了这个image由那些roLayer构成，还包含了部分配置信息，
	了解docker的人应该知道容器的部分配置信息是存放在image里面的，如volume、port、workdir等，这部分信息就存放在这个目录下面，
	docker启动时会读取镜像配置信息，反序列化出image对象，docker images获取到的镜像有多少个，这里就有多少个响应的目录，目录名image id
		metadata目录：metadata目录存放了docker image的parent信息
	*/ //赋值见 NewFSStoreBackend，store 的函数接口在fs.go文件中，
	// fs为type fs struct类型结构，实现有 StoreBackend 接口函数信息
	fs        StoreBackend  ///var/lib/docker/image/{driver}/imagedb

	//digestSet成员，本质上是一个set数据结构，里面存放的其实是每个docker的最上层layer的chain-id。
	digestSet *digestset.Set //集合add见 (is *store) restore()   这个集合里面记录的是/var/lib/docker/image/{driver}/imagedb/content/sha256/目录下的所有dgst
}

// NewImageStore returns new store object for given layer store
/*
遍历/var/lib/docker/image/{driver}/imagedb/content/sha256文件夹中的hex目录,然后读取其中的文件内容，通过diff_ids计算出chainID,然后获取
/var/lib/docker/image/devicemapper/layerdb/sha256/$chainID的 roLayer 信息，然后存入 store.images[]中
*/
// image/store.go  创建镜像仓库实例  LayerGetReleaser中的接口从实参 Daemon.layerStore 获得
func NewImageStore(fs StoreBackend, ls LayerGetReleaser) (Store, error) { //NewDaemon 中调用
	is := &store {
		ls:        ls,
		images:    make(map[ID]*imageMeta),
		fs:        fs,  ///var/lib/docker/image/{driver}/imagedb
		digestSet: digestset.NewSet(),
	}

	// load all current images and retain layers
	/*
	遍历/var/lib/docker/image/{driver}/imagedb/content/sha256文件夹中的hex目录,然后读取其中的文件内容，通过diff_ids计算出chainID,然后获取
	/var/lib/docker/image/devicemapper/layerdb/sha256/$chainID的 roLayer 信息，然后存入 store.images[]中
	*/
	if err := is.restore(); err != nil { //NewImageStore->(is *store) restore()
		return nil, err
	}

	return is, nil
}

/*
遍历/var/lib/docker/image/{driver}/imagedb/content/sha256文件夹中的hex目录,然后读取其中的文件内容，通过diff_ids计算出chainID,然后获取
/var/lib/docker/image/devicemapper/layerdb/sha256/$chainID的 roLayer 信息，然后存入 store.images[]中
*/
//NewImageStore 中调用
func (is *store) restore() error {
	//Walk对应 (s *fs) Walk(f DigestWalkFunc)
	//// 遍历/var/lib/docker/image/{driver}/imagedb/content/sha256文件夹中的hex目录获取hex目录名，然后调用f函数执行
	err := is.fs.Walk(func(dgst digest.Digest) error {
		//dgst对应/var/lib/docker/image/devicemapper/imagedb/content/sha256目录下的文件内容信息存入type Image struct {}结构中
		img, err := is.Get(IDFromDigest(dgst))
		if err != nil {
			logrus.Errorf("invalid image %v, %v", dgst, err)
			return nil
		}

		var l layer.Layer
		//根据/var/lib/docker/image/devicemapper/imagedb/content/sha256/$id文件内容中的rootfs中的diff_ids {}json内容算出一个ChainID
		if chainID := img.RootFS.ChainID(); chainID != "" {
			//根据ChainID 算出来的ID，然后通过 /var/lib/docker/image/devicemapper/layerdb/sha256/$chainID 获取到对应的roLayer
			l, err = is.ls.Get(chainID)//调用 layerStore 中的 Get函数， 获取chainID对应的layer信息，referencedCacheLayer
			if err != nil {
				return err
			}
		}
		if err := is.digestSet.Add(dgst); err != nil {
			return err
		}

		imageMeta := &imageMeta{
			layer:    l, //这里的layer实际上是通过上面的is.ls.Get获取到的 referencedCacheLayer 信息
			children: make(map[ID]struct{}),
		}

		///var/lib/docker/image/{driver}/imagedb/content/sha256/目录中的dgst信息实际上全是存入这里面的
		is.images[IDFromDigest(dgst)] = imageMeta

		return nil
	})
	if err != nil {
		return err
	}

	// Second pass to fill in children maps
	for id := range is.images {
		if parent, err := is.GetParent(id); err == nil {
			if parentMeta := is.images[parent]; parentMeta != nil {
				parentMeta.children[id] = struct{}{}
			}
		}
	}

	return nil
}

func (is *store) Create(config []byte) (ID, error) {
	var img Image
	err := json.Unmarshal(config, &img)
	if err != nil {
		return "", err
	}

	// Must reject any config that references diffIDs from the history
	// which aren't among the rootfs layers.
	rootFSLayers := make(map[layer.DiffID]struct{})
	for _, diffID := range img.RootFS.DiffIDs {
		rootFSLayers[diffID] = struct{}{}
	}

	layerCounter := 0
	for _, h := range img.History {
		if !h.EmptyLayer {
			layerCounter++
		}
	}
	if layerCounter > len(img.RootFS.DiffIDs) {
		return "", errors.New("too many non-empty layers in History section")
	}

	dgst, err := is.fs.Set(config)
	if err != nil {
		return "", err
	}
	imageID := IDFromDigest(dgst)

	is.Lock()
	defer is.Unlock()

	if _, exists := is.images[imageID]; exists {
		return imageID, nil
	}

	layerID := img.RootFS.ChainID()

	var l layer.Layer
	if layerID != "" {
		l, err = is.ls.Get(layerID)
		if err != nil {
			return "", err
		}
	}

	imageMeta := &imageMeta{
		layer:    l,
		children: make(map[ID]struct{}),
	}

	is.images[imageID] = imageMeta
	if err := is.digestSet.Add(imageID.Digest()); err != nil {
		delete(is.images, imageID)
		return "", err
	}

	return imageID, nil
}

func (is *store) Search(term string) (ID, error) {
	is.Lock()
	defer is.Unlock()

	dgst, err := is.digestSet.Lookup(term)
	if err != nil {
		if err == digestset.ErrDigestNotFound {
			err = fmt.Errorf("No such image: %s", term)
		}
		return "", err
	}
	return IDFromDigest(dgst), nil
}

//获取/var/lib/docker/image/devicemapper/imagedb/content/sha256目录下面的ID对应的文件内容配置信息
func (is *store) Get(id ID) (*Image, error) {
	// todo: Check if image is in images
	// todo: Detect manual insertions and start using them
	config, err := is.fs.Get(id.Digest()) //获取ID对应文件的内容
	if err != nil {
		return nil, err
	}

	img, err := NewFromJSON(config)
	if err != nil {
		return nil, err
	}
	img.computedID = id

	//// 读取/var/lib/docker/image/{driver}/imagedb/metadata/sha256/parent 中的内容通过byte返回
	img.Parent, err = is.GetParent(id)
	if err != nil { //没有parent文件parent指向空
		img.Parent = ""
	}

	return img, nil
}

func (is *store) Delete(id ID) ([]layer.Metadata, error) {
	is.Lock()
	defer is.Unlock()

	imageMeta := is.images[id]
	if imageMeta == nil {
		return nil, fmt.Errorf("unrecognized image ID %s", id.String())
	}
	for id := range imageMeta.children {
		is.fs.DeleteMetadata(id.Digest(), "parent")
	}
	if parent, err := is.GetParent(id); err == nil && is.images[parent] != nil {
		delete(is.images[parent].children, id)
	}

	if err := is.digestSet.Remove(id.Digest()); err != nil {
		logrus.Errorf("error removing %s from digest set: %q", id, err)
	}
	delete(is.images, id)
	is.fs.Delete(id.Digest())

	if imageMeta.layer != nil {
		return is.ls.Release(imageMeta.layer)
	}
	return nil, nil
}

func (is *store) SetParent(id, parent ID) error {
	is.Lock()
	defer is.Unlock()
	parentMeta := is.images[parent]
	if parentMeta == nil {
		return fmt.Errorf("unknown parent image ID %s", parent.String())
	}
	if parent, err := is.GetParent(id); err == nil && is.images[parent] != nil {
		delete(is.images[parent].children, id)
	}
	parentMeta.children[id] = struct{}{}
	return is.fs.SetMetadata(id.Digest(), "parent", []byte(parent))
}

/// 读取/var/lib/docker/image/{driver}/imagedb/metadata/sha256/$dgst/$key 中的内容通过byte返回
func (is *store) GetParent(id ID) (ID, error) {
	// (s *fs) GetMetadata  // 读取/var/lib/docker/image/{driver}/imagedb/metadata/sha256/$dgst/$key 中的内容通过byte返回
	d, err := is.fs.GetMetadata(id.Digest(), "parent")
	if err != nil {  //没有这个文件直接返回""
		return "", err
	}
	return ID(d), nil // todo: validate?
}

func (is *store) Children(id ID) []ID {
	is.Lock()
	defer is.Unlock()

	return is.children(id)
}

func (is *store) children(id ID) []ID {
	var ids []ID
	if is.images[id] != nil {
		for id := range is.images[id].children {
			ids = append(ids, id)
		}
	}
	return ids
}

func (is *store) Heads() map[ID]*Image {
	return is.imagesMap(false)
}

func (is *store) Map() map[ID]*Image {
	return is.imagesMap(true)
}

func (is *store) imagesMap(all bool) map[ID]*Image {
	is.Lock()
	defer is.Unlock()

	images := make(map[ID]*Image)

	for id := range is.images {
		if !all && len(is.children(id)) > 0 {
			continue
		}
		img, err := is.Get(id)
		if err != nil {
			logrus.Errorf("invalid image access: %q, error: %q", id, err)
			continue
		}
		images[id] = img
	}
	return images
}
