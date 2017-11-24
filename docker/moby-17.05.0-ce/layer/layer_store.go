package layer

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/docker/distribution"
	"github.com/docker/docker/daemon/graphdriver"
	"github.com/docker/docker/pkg/idtools"
	"github.com/docker/docker/pkg/plugingetter"
	"github.com/docker/docker/pkg/stringid"
	"github.com/opencontainers/go-digest"
	"github.com/vbatts/tar-split/tar/asm"
	"github.com/vbatts/tar-split/tar/storage"
)

// maxLayerDepth represents the maximum number of
// layers which can be chained together. 125 was
// chosen to account for the 127 max in some
// graphdrivers plus the 2 additional layers
// used to create a rwlayer.
const maxLayerDepth = 125

//docker daemon在初始化过程中，会初始化一个layerStore，那么layerStore是什么呢？从名字可以看出，是用来存储layer的，docker镜像时分层的，一层称为一个layer。
//docker的镜像在docker老版本中是由一个叫graph的数据结构进行管理的，现在换成了layerStore
//见http://licyhust.com/%E5%AE%B9%E5%99%A8%E6%8A%80%E6%9C%AF/2016/09/27/docker-image-data-structure/

//注意 roLayer mountedLayer 和 layerStore 的关系  layerStore 包含 roLayer mountedLayer成员

//MetadataStore 是/var/lib/docker/image/devicemapper/layerdb/目录中相关文件操作的接口， 该目录下面的文件内容存储在 layerStore 结构中
//StoreBackend 是/var/lib/docker/image/{driver}/imagedb 目录中相关文件操作的接口， 该目录下面的文件内容存储在 store 结构中
///var/lib/docker/image/{driver}/imagedb/content/sha256/下面的文件和/var/lib/docker/image/devicemapper/layerdb/sha256下面的文件通过(is *store) restore()关联起来

//Daemon.layerStore 成员实际上就是存储的layerStore结构， 数据源头由 Daemon.layerStore 指向
type layerStore struct {  //NewStoreFromGraphDriver 中初始化会使用该结构类型
	//MetadataStore为接口，主要为获得层基本信息的方法。
	// metadata是这个层的额外信息，不仅能够让docker获取运行和构建的信息， 也包括父层的层次信息（只读层和读写层都包含元数据）。

	/*
	store的数据类型为MetadataStore，主要用来存储每个layer的元数据，存储的目录位于/var/lib/docker/image/{driver}/layerdb，
	这里的driver包括aufs、devicemapper、overlay和btrfs。
	layerdb下面有三个目录，mounts、sha256和tmp，tmp目录主要存放临时性数据
	*/
	store  MetadataStore	  //store对应 fileMetadataStore  用于读取/var/lib/docker/image/devicemapper/layerdb/mounts/目录中的相关文件内容使用的函数接口
	driver graphdriver.Driver // driver:例如devicemapper，这里的driver对应 graphdriver\driver.go中的 Driver 结构

	//即map的键为ChainID（字母串），值为roLayer, store本质上是磁盘上保存了各个layer的元数据信息，当docker初始化时，它会利用
	//这些元数据文件在内存中构造各个layer，每个Layer都用一个roLayer结构体表示，即只读(ro)的layer
	//roLayer 存储镜像层信息，见loadLayer  mountedLayer 存储只读层(容器层)信息，见loadMount
	//ChinID为image/devicemapper/layerdb/sha256/xxx 中的xxx，roLayer为XXX中的各个文件内容存储在roLayer，赋值见 loadLayer
	//ChinID为image/devicemapper/layerdb/sha256/xxx 中的xxx实际是根据/var/lib/docker/image/devicemapper/imagedb/content/sha256/$ID文件内容中的diff_ids列表算出来的，可以参考 (is *store) restore()
	layerMap map[ChainID]*roLayer //镜像层 roLayer 中各层ID和roLayer对应关系存到这里面，赋值见 loadLayer，
	layerL   sync.Mutex

	//roLayer 存储镜像层信息，见loadLayer  mountedLayer 存储只读层(容器层)信息，见loadMount   mounts层创建见saveMount
	mounts map[string]*mountedLayer
	mountL sync.Mutex

	useTarSplit bool
}

// StoreOptions are the options used to create a new Store instance
type StoreOptions struct { //初始化赋值见 NewDaemon
	StorePath                 string  //  /var/lib/docker
	//该目录下的相关文件对应的函数接口见 fileMetadataStore
	MetadataStorePathTemplate string  //  /var/lib/docker/image/devicemapper/layerdb  赋值见NewDaemon ，该目录在NewFSMetadataStore中创建
	//生效使用见NewStoreFromOptions   --storage-driver 配置，有 devicemapper  aufs  overlay 等
	GraphDriver               string  //devicemapper  overlay等
	GraphDriverOptions        []string  //存储驱动对应的选项信息
	UIDMaps                   []idtools.IDMap
	GIDMaps                   []idtools.IDMap
	PluginGetter              plugingetter.PluginGetter
	ExperimentalEnabled       bool
}
// NewStoreFromOptions creates a new Store instance  // lay/layer_store.go 创建graphDriver实例，从driver创建layer仓库的实例
// NewStoreFromOptions creates a new Store instance
/*

*/ //NewDaemon 中执行   初始化/var/lib/docker/image/devicemapper/layerdb/相关操作的接口并初始化存储驱动devicemapper等
func NewStoreFromOptions(options StoreOptions) (Store, error) { //返回 layerStore 类型
	//NewDaemon->NewStoreFromOptions->graphdriver.New   //存储驱动的初始化
	//这里的driver也就是 driver.go中的 drivers map[string]InitFunc 获取到的，见GetDriver
	driver, err := graphdriver.New(options.GraphDriver, options.PluginGetter, graphdriver.Options{
		Root:                options.StorePath,
		DriverOptions:       options.GraphDriverOptions,
		UIDMaps:             options.UIDMaps,
		GIDMaps:             options.GIDMaps,
		ExperimentalEnabled: options.ExperimentalEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("error initializing graphdriver: %v", err)
	}
	logrus.Debugf("Using graph driver %s", driver)

	fms, err := NewFSMetadataStore(fmt.Sprintf(options.MetadataStorePathTemplate, driver))
	if err != nil {
		return nil, err
	}

	//fms结构包含 getLayerDirectory getLayerFilename等方法
	return NewStoreFromGraphDriver(fms, driver)
}

// NewStoreFromGraphDriver creates a new Store instance using the provided
// metadata store and graph driver. The metadata store will be used to restore
// the Store.
//加载/var/lib/docker/image/devicemapper/layerdb/mounts和/var/lib/docker/image/devicemapper/layerdb/sha256目录中的文件内容到layerStore
//store对应 fileMetadataStore    driver:例如devicemapper，这里的driver对应 graphdriver\driver.go中的 Driver结构
func NewStoreFromGraphDriver(store MetadataStore, driver graphdriver.Driver) (Store, error) {  //layerStore 返回
	caps := graphdriver.Capabilities{}
	if capDriver, ok := driver.(graphdriver.CapabilityDriver); ok {
		caps = capDriver.Capabilities()
	}

	ls := &layerStore{
		store:       store,
		driver:      driver,
		layerMap:    map[ChainID]*roLayer{},
		mounts:      map[string]*mountedLayer{},
		useTarSplit: !caps.ReproducesExactDiffs,
	}

	// 读取/var/lib/docker/image/devicemapper/layerdb/sha256目录中的文件夹存入ids数组中
	// /var/lib/docker/image/devicemapper/layerdb/mounts目录下面的文件夹名称存入 mounts 数组
	ids, mounts, err := store.List()  //fileMetadataStore->(fms *fileMetadataStore) List()
	if err != nil {
		return nil, err
	}

	///var/lib/docker/image/devicemapper/layerdb/sha256存的是镜像层信息
	// /var/lib/docker/image/devicemapper/layerdb/mounts存的是容器层信息

	for _, id := range ids { //镜像层id列表信息
		l, err := ls.loadLayer(id)
		if err != nil {
			logrus.Debugf("Failed to load layer %s: %s", id, err)
			continue
		}
		if l.parent != nil {
			l.parent.referenceCount++
		}
	}

	for _, mount := range mounts { //容器层列表信息
		if err := ls.loadMount(mount); err != nil {
			logrus.Debugf("Failed to load mount %s: %s", mount, err)
		}
	}

	return ls, nil
}

func (ls *layerStore) loadLayer(layer ChainID) (*roLayer, error) {
	cl, ok := ls.layerMap[layer] //如果 ChainID:roLayer已经存在，直接返回，如果没有对应的关系存在，则在后面加入
	if ok {
		return cl, nil
	}

	// 获取/var/lib/docker/image/devicemapper/layerdb/sha256/02f5128fe5a1deee94c98a1be63692a776693f09b0bb2bbfdf2ee74620071d54/diff中的文件内容，然后通过DiffID返回
	diff, err := ls.store.GetDiffID(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff id for %s: %s", layer, err)
	}

	///var/lib/docker/image/devicemapper/layerdb/sha256/02f5128fe5a1deee94c98a1be63692a776693f09b0bb2bbfdf2ee74620071d54/size
	size, err := ls.store.GetSize(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get size for %s: %s", layer, err)
	}

	///var/lib/docker/image/devicemapper/layerdb/sha256/02f5128fe5a1deee94c98a1be63692a776693f09b0bb2bbfdf2ee74620071d54/cache-id
	cacheID, err := ls.store.GetCacheID(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache id for %s: %s", layer, err)
	}

	///var/lib/docker/image/devicemapper/layerdb/sha256/02f5128fe5a1deee94c98a1be63692a776693f09b0bb2bbfdf2ee74620071d54/parent
	parent, err := ls.store.GetParent(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get parent for %s: %s", layer, err)
	}

	descriptor, err := ls.store.GetDescriptor(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get descriptor for %s: %s", layer, err)
	}

	//把 /var/lib/docker/image/devicemapper/layerdb/sha256/02f5128fe5a1deee94c98a1be63692a776693f09b0bb2bbfdf2ee74620071d54这层
	//获取到的信息存入到roLayer
	cl = &roLayer{
		chainID:    layer,
		diffID:     diff,
		size:       size,
		cacheID:    cacheID,
		layerStore: ls,
		references: map[Layer]struct{}{},
		descriptor: descriptor,
	}

	if parent != "" { //上层和下层通过这里的parent联系起来
		p, err := ls.loadLayer(parent)
		if err != nil {
			return nil, err
		}
		cl.parent = p
	}

	ls.layerMap[cl.chainID] = cl

	return cl, nil
}

// 读取/var/lib/docker/image/devicemapper/layerdb/mounts/containerId目录下面的文件内容存入 mountedLayer
func (ls *layerStore) loadMount(mount string) error {
	if _, ok := ls.mounts[mount]; ok { //已经存在于mounts中说明已经读取过了，不用走后面流程
		return nil
	}

	mountID, err := ls.store.GetMountID(mount)
	if err != nil {
		return err
	}

	initID, err := ls.store.GetInitID(mount)
	if err != nil {
		return err
	}

	parent, err := ls.store.GetMountParent(mount)
	if err != nil {
		return err
	}

	ml := &mountedLayer{
		name:       mount,
		mountID:    mountID,
		initID:     initID,
		layerStore: ls,
		references: map[RWLayer]*referencedRWLayer{},
	}

	if parent != "" {
		p, err := ls.loadLayer(parent) //注意:容器层的parent为镜像层
		if err != nil {
			return err
		}
		ml.parent = p

		p.referenceCount++ //镜像层被几个容器给引用了
	}

	ls.mounts[ml.name] = ml

	return nil
}

func (ls *layerStore) applyTar(tx MetadataTransaction, ts io.Reader, parent string, layer *roLayer) error {
	digester := digest.Canonical.Digester()
	tr := io.TeeReader(ts, digester.Hash())

	rdr := tr
	if ls.useTarSplit {
		tsw, err := tx.TarSplitWriter(true)
		if err != nil {
			return err
		}
		metaPacker := storage.NewJSONPacker(tsw)
		defer tsw.Close()

		// we're passing nil here for the file putter, because the ApplyDiff will
		// handle the extraction of the archive
		rdr, err = asm.NewInputTarStream(tr, metaPacker, nil)
		if err != nil {
			return err
		}
	}

	applySize, err := ls.driver.ApplyDiff(layer.cacheID, parent, rdr)
	if err != nil {
		return err
	}

	// Discard trailing data but ensure metadata is picked up to reconstruct stream
	io.Copy(ioutil.Discard, rdr) // ignore error as reader may be closed

	layer.size = applySize
	layer.diffID = DiffID(digester.Digest())

	logrus.Debugf("Applied tar %s to %s, size: %d", layer.diffID, layer.cacheID, applySize)

	return nil
}

func (ls *layerStore) Register(ts io.Reader, parent ChainID) (Layer, error) {
	return ls.registerWithDescriptor(ts, parent, distribution.Descriptor{})
}

func (ls *layerStore) registerWithDescriptor(ts io.Reader, parent ChainID, descriptor distribution.Descriptor) (Layer, error) {
	// err is used to hold the error which will always trigger
	// cleanup of creates sources but may not be an error returned
	// to the caller (already exists).
	var err error
	var pid string
	var p *roLayer
	if string(parent) != "" {
		p = ls.get(parent)
		if p == nil {
			return nil, ErrLayerDoesNotExist
		}
		pid = p.cacheID
		// Release parent chain if error
		defer func() {
			if err != nil {
				ls.layerL.Lock()
				ls.releaseLayer(p)
				ls.layerL.Unlock()
			}
		}()
		if p.depth() >= maxLayerDepth {
			err = ErrMaxDepthExceeded
			return nil, err
		}
	}

	// Create new roLayer
	layer := &roLayer{
		parent:         p,
		cacheID:        stringid.GenerateRandomID(),
		referenceCount: 1,
		layerStore:     ls,
		references:     map[Layer]struct{}{},
		descriptor:     descriptor,
	}

	if err = ls.driver.Create(layer.cacheID, pid, nil); err != nil {
		return nil, err
	}

	tx, err := ls.store.StartTransaction()
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			logrus.Debugf("Cleaning up layer %s: %v", layer.cacheID, err)
			if err := ls.driver.Remove(layer.cacheID); err != nil {
				logrus.Errorf("Error cleaning up cache layer %s: %v", layer.cacheID, err)
			}
			if err := tx.Cancel(); err != nil {
				logrus.Errorf("Error canceling metadata transaction %q: %s", tx.String(), err)
			}
		}
	}()

	if err = ls.applyTar(tx, ts, pid, layer); err != nil {
		return nil, err
	}

	if layer.parent == nil {
		layer.chainID = ChainID(layer.diffID)
	} else {
		layer.chainID = createChainIDFromParent(layer.parent.chainID, layer.diffID)
	}

	if err = storeLayer(tx, layer); err != nil {
		return nil, err
	}

	ls.layerL.Lock()
	defer ls.layerL.Unlock()

	if existingLayer := ls.getWithoutLock(layer.chainID); existingLayer != nil {
		// Set error for cleanup, but do not return the error
		err = errors.New("layer already exists")
		return existingLayer.getReference(), nil
	}

	if err = tx.Commit(layer.chainID); err != nil {
		return nil, err
	}

	ls.layerMap[layer.chainID] = layer

	return layer.getReference(), nil
}

//ls.layerMap[layer] 被根据/var/lib/docker/image/devicemapper/imagedb/content/sha256/$ID文件内容中的diff_ids列表
// 算出来的ChainID，被/var/lib/docker/image/devicemapper/imagedb/content/sha256/$ID引用
func (ls *layerStore) getWithoutLock(layer ChainID) *roLayer {
	l, ok := ls.layerMap[layer]
	if !ok {
		return nil
	}

	l.referenceCount++

	return l
}

func (ls *layerStore) get(l ChainID) *roLayer {
	ls.layerL.Lock()
	defer ls.layerL.Unlock()
	return ls.getWithoutLock(l)
}

//获取ChainID对应的layer信息
func (ls *layerStore) Get(l ChainID) (Layer, error) {
	ls.layerL.Lock()
	defer ls.layerL.Unlock()

	//ls.layerMap[layer] 被根据/var/lib/docker/image/devicemapper/imagedb/content/sha256/$ID文件内容中的diff_ids列表
	// 算出来的ChainID，被/var/lib/docker/image/devicemapper/imagedb/content/sha256/$ID引用
	layer := ls.getWithoutLock(l)
	if layer == nil {
		return nil, ErrLayerDoesNotExist
	}

	return layer.getReference(), nil
}

func (ls *layerStore) Map() map[ChainID]Layer {
	ls.layerL.Lock()
	defer ls.layerL.Unlock()

	layers := map[ChainID]Layer{}

	for k, v := range ls.layerMap {
		layers[k] = v
	}

	return layers
}

func (ls *layerStore) deleteLayer(layer *roLayer, metadata *Metadata) error {
	err := ls.driver.Remove(layer.cacheID)
	if err != nil {
		return err
	}

	err = ls.store.Remove(layer.chainID)
	if err != nil {
		return err
	}
	metadata.DiffID = layer.diffID
	metadata.ChainID = layer.chainID
	metadata.Size, err = layer.Size()
	if err != nil {
		return err
	}
	metadata.DiffSize = layer.size

	return nil
}

func (ls *layerStore) releaseLayer(l *roLayer) ([]Metadata, error) {
	depth := 0
	removed := []Metadata{}
	for {
		if l.referenceCount == 0 {
			panic("layer not retained")
		}
		l.referenceCount--
		if l.referenceCount != 0 {
			return removed, nil
		}

		if len(removed) == 0 && depth > 0 {
			panic("cannot remove layer with child")
		}
		if l.hasReferences() {
			panic("cannot delete referenced layer")
		}
		var metadata Metadata
		if err := ls.deleteLayer(l, &metadata); err != nil {
			return nil, err
		}

		delete(ls.layerMap, l.chainID)
		removed = append(removed, metadata)

		if l.parent == nil {
			return removed, nil
		}

		depth++
		l = l.parent
	}
}

func (ls *layerStore) Release(l Layer) ([]Metadata, error) {
	ls.layerL.Lock()
	defer ls.layerL.Unlock()
	layer, ok := ls.layerMap[l.ChainID()]
	if !ok {
		return []Metadata{}, nil
	}
	if !layer.hasReference(l) {
		return nil, ErrLayerNotRetained
	}

	layer.deleteReference(l)

	return ls.releaseLayer(layer)
}

//create.go中的setRWLayer函数调用  //setRWLayer->CreateRWLayer
// //name容器id   parent为镜像层最顶层 chainID   opts为创建容器时指定的部分参数
//创建/var/lib/docker/image/overlay/layerdb/mounts/$mountID 目录及其下面的文件，同时创建存储该容器层的device

//1. 创建容器层device
//1. 向/var/lib/docker/image/devicemapper/layerdb/mounts/中的指定文件中写入对应的内容 例如，该目录下的以下文件内容init-id  mount-id  parent
//2. 创建/var/lib/docker/devicemapper/mnt/$mountID-INIT init层对应的device，然后在device中把INIT层的相关/etc/hostname /etc/resolve.conf等文件创建在该device空间
// setRWLayer->CreateRWLayer
func (ls *layerStore) CreateRWLayer(name string, parent ChainID, opts *CreateRWLayerOpts) (RWLayer, error) { //RWLayer实际上对应的是referencedRWLayer 类型
	var (
		storageOpt map[string]string
		initFunc   MountInit   //daemon.getLayerInit(),
		mountLabel string
	)

	if opts != nil {
		/*
		MountLabel: container.MountLabel,
		InitFunc:   daemon.getLayerInit(),
		StorageOpt: container.HostConfig.StorageOpt,
		*/
		mountLabel = opts.MountLabel   //opts赋值见 setRWLayer
		storageOpt = opts.StorageOpt
		initFunc = opts.InitFunc
	}

	ls.mountL.Lock()
	defer ls.mountL.Unlock()
	m, ok := ls.mounts[name] //该容器已经存在了，直接返回报错
	if ok {
		return nil, ErrMountNameConflict
	}

	var err error
	var pid string
	var p *roLayer
	if string(parent) != "" {
		p = ls.get(parent) //根据parent获取该parent对应的镜像层 roLayer 信息
		if p == nil {
			return nil, ErrLayerDoesNotExist
		}
		pid = p.cacheID //该parent镜像层对应的cacheID,cacheID中的metaID是该镜像层中真正存储数据的元信息地址，指向/var/lib/docker/devicemapper/metadata/$metaID

		// Release parent chain if error
		defer func() {
			if err != nil {
				ls.layerL.Lock()
				ls.releaseLayer(p)
				ls.layerL.Unlock()
			}
		}()
	}

	m = &mountedLayer { // /var/lib/docker/image/devicemapper/layerdb/mounts/$mountID
		name:       name, //容器ID
		parent:     p,  //镜像层chainID
		mountID:    ls.mountID(name),  //创建容器的时候根据容器ID随机生成容器mountID
		layerStore: ls,
		references: map[RWLayer]*referencedRWLayer{},
	}

	if initFunc != nil { //daemon.getLayerInit(),     准备INIT层
		// 创建/var/lib/docker/devicemapper/mnt/$mountID-INIT init层对应的device，然后在device中把INIT层的相关/etc/hostname /etc/resolve.conf等文件创建在该device空间
		pid, err = ls.initMount(m.mountID, pid, mountLabel, initFunc, storageOpt)
		if err != nil {
			return nil, err
		}
		m.initID = pid
	}

	createOpts := &graphdriver.CreateOpts{
		StorageOpt: storageOpt,
	}

	//准备容器层
	// /var/lib/docker/image/devicemapper/layerdb/mounts/$containerID/mount-id内容中的mount-id，也就是创建存储容器层文件系统的deviceID
	if err = ls.driver.CreateReadWrite(m.mountID, pid, createOpts); err != nil { //注意容器层的deviceid在该函数中没有mount,在docker start的时候才会mount
		return nil, err
	}

	//注意容器层的device mount挂载在外层函数 createContainerPlatformSpecificSettings 中实现，而INIT层的挂载在上面的initMount会做

	//向/var/lib/docker/image/devicemapper/layerdb/mounts/中的指定文件中写入对应的内容 例如，该目录下的以下文件内容init-id  mount-id  parent
	if err = ls.saveMount(m); err != nil {
		return nil, err
	}

	return m.getReference(), nil   //
}

func (ls *layerStore) GetRWLayer(id string) (RWLayer, error) {
	ls.mountL.Lock()
	defer ls.mountL.Unlock()
	mount, ok := ls.mounts[id]
	if !ok {
		return nil, ErrMountDoesNotExist
	}

	return mount.getReference(), nil
}

func (ls *layerStore) GetMountID(id string) (string, error) {
	ls.mountL.Lock()
	defer ls.mountL.Unlock()
	mount, ok := ls.mounts[id]
	if !ok {
		return "", ErrMountDoesNotExist
	}
	logrus.Debugf("GetMountID id: %s -> mountID: %s", id, mount.mountID)

	return mount.mountID, nil
}

func (ls *layerStore) ReleaseRWLayer(l RWLayer) ([]Metadata, error) {
	ls.mountL.Lock()
	defer ls.mountL.Unlock()
	m, ok := ls.mounts[l.Name()]
	if !ok {
		return []Metadata{}, nil
	}

	if err := m.deleteReference(l); err != nil {
		return nil, err
	}

	if m.hasReferences() {
		return []Metadata{}, nil
	}

	if err := ls.driver.Remove(m.mountID); err != nil {
		logrus.Errorf("Error removing mounted layer %s: %s", m.name, err)
		m.retakeReference(l)
		return nil, err
	}

	if m.initID != "" {
		if err := ls.driver.Remove(m.initID); err != nil {
			logrus.Errorf("Error removing init layer %s: %s", m.name, err)
			m.retakeReference(l)
			return nil, err
		}
	}

	if err := ls.store.RemoveMount(m.name); err != nil {
		logrus.Errorf("Error removing mount metadata: %s: %s", m.name, err)
		m.retakeReference(l)
		return nil, err
	}

	delete(ls.mounts, m.Name())

	ls.layerL.Lock()
	defer ls.layerL.Unlock()
	if m.parent != nil {
		return ls.releaseLayer(m.parent)
	}

	return []Metadata{}, nil
}

//向/var/lib/docker/image/devicemapper/layerdb/mounts/中的指定文件中写入对应的内容 例如，该目录下的以下文件内容init-id  mount-id  parent
func (ls *layerStore) saveMount(mount *mountedLayer) error {
	if err := ls.store.SetMountID(mount.name, mount.mountID); err != nil {
		return err
	}

	if mount.initID != "" {
		if err := ls.store.SetInitID(mount.name, mount.initID); err != nil {
			return err
		}
	}

	if mount.parent != nil {
		if err := ls.store.SetMountParent(mount.name, mount.parent.chainID); err != nil {
			return err
		}
	}

	ls.mounts[mount.name] = mount

	return nil
}


//容器层挂载在 containerStart->conditionalMountOnStart->(daemon *Daemon) Mount
//INIT层挂载在 initMount

//graphID为容器层中的mountID
// parent为镜像层总最顶层的chinaID
// initFunc为daemon.getLayerInit(),
// setRWLayer->CreateRWLayer->initMount
// 创建/var/lib/docker/devicemapper/mnt/$mountID-INIT init层对应的device，然后在device中把INIT层的相关/etc/hostname /etc/resolve.conf等文件创建在该device空间
func (ls *layerStore) initMount(graphID, parent, mountLabel string, initFunc MountInit, storageOpt map[string]string) (string, error) {
	// Use "<graph-id>-init" to maintain compatibility with graph drivers
	// which are expecting this layer with this special name. If all
	// graph drivers can be updated to not rely on knowing about this layer
	// then the initID should be randomly generated.
	initID := fmt.Sprintf("%s-init", graphID) //容器对应的mountID+ "-init"

	createOpts := &graphdriver.CreateOpts {
		MountLabel: mountLabel,
		StorageOpt: storageOpt,
	}

	///var/lib/docker/image/devicemapper/layerdb/mounts/$containerID/中的init-id文件中的内容 对应的device
	if err := ls.driver.CreateReadWrite(initID, parent, createOpts); err != nil { //创建存储INIT层的 device
		return "", err
	}

	//挂载INIT等对应的device到/var/lib/docker/devicemapper/mnt/$mountID-INIT 目录下, 同时返回该目录赋值给p
	p, err := ls.driver.Get(initID, "")
	if err != nil {
		return "", err
	}

	//这样前面的device上面就会有init层相关的文件了
	if err := initFunc(p); err != nil {  //创建INIT层相关的文件，如"/etc/hosts" "/etc/resolve.conf"等  //setupInitLayer(initPath)
		ls.driver.Put(initID)
		return "", err
	}

	//  从/var/lib/docker/devicemapper/mnt/$mountID-INIT 下解挂设备
	if err := ls.driver.Put(initID); err != nil {
		return "", err
	}

	return initID, nil
}

func (ls *layerStore) getTarStream(rl *roLayer) (io.ReadCloser, error) {
	if !ls.useTarSplit {
		var parentCacheID string
		if rl.parent != nil {
			parentCacheID = rl.parent.cacheID
		}

		return ls.driver.Diff(rl.cacheID, parentCacheID)
	}

	r, err := ls.store.TarSplitReader(rl.chainID)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		err := ls.assembleTarTo(rl.cacheID, r, nil, pw)
		if err != nil {
			pw.CloseWithError(err)
		} else {
			pw.Close()
		}
	}()

	return pr, nil
}

func (ls *layerStore) assembleTarTo(graphID string, metadata io.ReadCloser, size *int64, w io.Writer) error {
	diffDriver, ok := ls.driver.(graphdriver.DiffGetterDriver)
	if !ok {
		diffDriver = &naiveDiffPathDriver{ls.driver}
	}

	defer metadata.Close()

	// get our relative path to the container
	fileGetCloser, err := diffDriver.DiffGetter(graphID)
	if err != nil {
		return err
	}
	defer fileGetCloser.Close()

	metaUnpacker := storage.NewJSONUnpacker(metadata)
	upackerCounter := &unpackSizeCounter{metaUnpacker, size}
	logrus.Debugf("Assembling tar data for %s", graphID)
	return asm.WriteOutputTarStream(fileGetCloser, upackerCounter, w)
}

func (ls *layerStore) Cleanup() error {
	return ls.driver.Cleanup()
}

func (ls *layerStore) DriverStatus() [][2]string {
	return ls.driver.Status()
}

func (ls *layerStore) DriverName() string {
	return ls.driver.String()
}

type naiveDiffPathDriver struct {
	graphdriver.Driver
}

type fileGetPutter struct {
	storage.FileGetter
	driver graphdriver.Driver
	id     string
}

func (w *fileGetPutter) Close() error {
	return w.driver.Put(w.id)
}

func (n *naiveDiffPathDriver) DiffGetter(id string) (graphdriver.FileGetCloser, error) {
	p, err := n.Driver.Get(id, "")
	if err != nil {
		return nil, err
	}
	return &fileGetPutter{storage.NewPathFileGetter(p), n.Driver, id}, nil
}
