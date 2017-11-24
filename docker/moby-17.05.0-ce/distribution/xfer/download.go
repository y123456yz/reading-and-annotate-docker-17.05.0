package xfer

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/distribution"
	"github.com/docker/docker/image"
	"github.com/docker/docker/layer"
	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/pkg/ioutils"
	"github.com/docker/docker/pkg/progress"
	"golang.org/x/net/context"
)

const maxDownloadAttempts = 5

// LayerDownloadManager figures out which layers need to be downloaded, then
// registers and downloads those, taking into account dependencies between
// layers.
//NewLayerDownloadManager 中构造使用
type LayerDownloadManager struct {
	//对应 Daemon.layerStore ,也就是 type layerStore struct 结构
	//主要是/var/lib/docker/image/devicemapper/layerdb/相关目录的操作
	layerStore   layer.Store
	//被赋值为 transferManager 结构，赋值见NewLayerDownloadManager
	tm           TransferManager
	waitDuration time.Duration
}

// SetConcurrency sets the max concurrent downloads for each pull
func (ldm *LayerDownloadManager) SetConcurrency(concurrency int) {
	ldm.tm.SetConcurrency(concurrency)
}
// distribution/xfer/download.go 创建层下载管理实例
// NewLayerDownloadManager returns a new LayerDownloadManager.
func NewLayerDownloadManager(layerStore layer.Store, concurrencyLimit int, options ...func(*LayerDownloadManager)) *LayerDownloadManager {
	manager := LayerDownloadManager{
		layerStore:   layerStore,
		tm:           NewTransferManager(concurrencyLimit),
		waitDuration: time.Second,
	}
	for _, option := range options {
		option(&manager)
	}
	return &manager
}

//
type downloadTransfer struct {
	Transfer

	layerStore layer.Store
	layer      layer.Layer
	err        error
}

// result returns the layer resulting from the download, if the download
// and registration were successful.
func (d *downloadTransfer) result() (layer.Layer, error) {
	return d.layer, d.err
}

// A DownloadDescriptor references a layer that may need to be downloaded.
//v2LayerDescriptor 结构实现该接口方法，参考 (p *v2Puller) pullSchema2       DownloadDescriptorWithRegistered 包含该接口
type DownloadDescriptor interface { v2LayerDescriptor
	// Key returns the key used to deduplicate downloads.
	Key() string
	// ID returns the ID for display purposes.
	ID() string
	// DiffID should return the DiffID for this layer, or an error
	// if it is unknown (for example, if it has not been downloaded
	// before).
	DiffID() (layer.DiffID, error)
	// Download is called to perform the download.
	Download(ctx context.Context, progressOutput progress.Output) (io.ReadCloser, int64, error)
	// Close is called when the download manager is finished with this
	// descriptor and will not call Download again or read from the reader
	// that Download returned.
	Close()
}

// DownloadDescriptorWithRegistered is a DownloadDescriptor that has an
// additional Registered method which gets called after a downloaded layer is
// registered. This allows the user of the download manager to know the DiffID
// of each registered layer. This method is called if a cast to
// DownloadDescriptorWithRegistered is successful.
//使用参考 (ldm *LayerDownloadManager) makeDownloadFuncFromDownload
type DownloadDescriptorWithRegistered interface {
	DownloadDescriptor
	Registered(diffID layer.DiffID) //v2LayerDescriptor 实现该函数
}

// Download is a blocking function which ensures the requested layers are
// present in the layer store. It uses the string returned by the Key method to
// deduplicate downloads. If a given layer is not already known to present in
// the layer store, and the key is not used by an in-progress download, the
// Download method is called to get the layer tar data. Layers are then
// registered in the appropriate order.  The caller must call the returned
// release function once it is done with the returned RootFS object.
//RootFS
// (p *v2Puller) pullSchema2 中执行
//initialRootFS 对应一个新的RootFS结构，layers 对应manifest内容中的"layers"相关的layer信息(v2LayerDescriptor 结构存储)， ImagePullConfig.Config.ProgressOutput
func (ldm *LayerDownloadManager) Download(ctx context.Context, initialRootFS image.RootFS, layers []DownloadDescriptor, progressOutput progress.Output) (image.RootFS, func(), error) {
	var (
		topLayer       layer.Layer
		topDownload    *downloadTransfer
		watcher        *Watcher
		missingLayer   bool
		transferKey    = ""
		downloadsByKey = make(map[string]*downloadTransfer)
	)

	rootFS := initialRootFS
	/*  manifest文件内容 (ms *manifests) Get 函数获取manifest文件，并打印内容
	{
	   ......
	   "layers": [
	      {
		 "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
		 "size": 22492350,
		 "digest": "sha256:bc95e04b23c06ba1b9bf092d07d1493177b218e0340bd2ed49dac351c1e34313"
	      },
	      {
		 "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
		 "size": 21913353,
		 "digest": "sha256:a21d9ee25fc3dcef76028536e7191e44554a8088250d4c3ec884af23cef4f02a"
	      },
	      {
		 "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
		 "size": 202,
		 "digest": "sha256:9bda7d5afd399f51550422c49172f8c9169fc3ffdef2748b13cfbf6467661ac5"
	      }
	   ]
	}
	*/
	//下载个层 tar 包镜像文件
	//遍历manifest中的layer信息
	for _, descriptor := range layers { //descriptor 为 v2LayerDescriptor 结构，也就是上面的layers信息
/*
LayerDownloadManager.download key:v2:sha256:bc95e04b23c06ba1b9bf092d07d1493177b218e0340bd2ed49dac351c1e34313, transferKey:v2:sha256:bc95e04b23c06ba1b9bf092d07d1493177b218e0340bd2ed49dac351c1e34313, descriptorID:bc95e04b23c0
LayerDownloadManager.download DiffID:sha256:cec7521cdf36a6d4ad8f2e92e41e3ac1b6fa6e05be07fa53cc84a63503bc5700
LayerDownloadManager.download key:v2:sha256:a21d9ee25fc3dcef76028536e7191e44554a8088250d4c3ec884af23cef4f02a, transferKey:v2:sha256:bc95e04b23c06ba1b9bf092d07d1493177b218e0340bd2ed49dac351c1e34313v2:sha256:a21d9ee25fc3dcef76028536e7191e44554a8088250d4c3ec884af23cef4f02a, descriptorID:a21d9ee25fc3
*/
		key := descriptor.Key()
		transferKey += key //所有的key放到transferKey中通过 ： 分割

		if !missingLayer {
			missingLayer = true
			// (ld *v2LayerDescriptor) DiffID()
			//根据manifest中的digest算出对应的diffid
			diffID, err := descriptor.DiffID() //
			if err == nil {
				getRootFS := rootFS
				//添加到RootFS.DiffIDs数组
				getRootFS.Append(diffID)
				//(ls *layerStore) Get
				//var/lib/docker/image/devicemapper/imagedb/content/sha256/$ChainID是否存在
				//从这里可以看出上层的chainID是下层几个diffID算出来的，因为getRootFS是下面几层的diffID放一起的，所以这里计算包括了下面的几层diffID
				l, err := ldm.layerStore.Get(getRootFS.ChainID()) //(r *RootFS) ChainID()
				if err == nil {
					// Layer already exists.
					logrus.Debugf("Layer already exists: %s", descriptor.ID())
					progress.Update(progressOutput, descriptor.ID(), "Already exists")
					if topLayer != nil {
						layer.ReleaseAndLog(ldm.layerStore, topLayer)
					}
					topLayer = l
					//置位false说明本层chainID已经存在，继续检查上层chainID是否存在
					missingLayer = false
					//如果diffID层已经存在，则加入到rootFS中
					rootFS.Append(diffID)
					// Register this repository as a source of this layer.
					withRegistered, hasRegistered := descriptor.(DownloadDescriptorWithRegistered)
					if hasRegistered {
						//(ld *v2LayerDescriptor) Registered
						withRegistered.Registered(diffID)
					}
					continue
				}
			}
		}

		// Does this layer have the same data as a previous layer in
		// the stack? If so, avoid downloading it more than once.
		var topDownloadUncasted Transfer
		if existingDownload, ok := downloadsByKey[key]; ok {
			xferFunc := ldm.makeDownloadFuncFromDownload(descriptor, existingDownload, topDownload)
			defer topDownload.Transfer.Release(watcher)
			topDownloadUncasted, watcher = ldm.tm.Transfer(transferKey, xferFunc, progressOutput)
			topDownload = topDownloadUncasted.(*downloadTransfer)
			continue
		}

		// Layer is not known to exist - download and register it.
		progress.Update(progressOutput, descriptor.ID(), "Pulling fs layer")

		var xferFunc DoFunc
		if topDownload != nil {
			//调用makeDownloadFunc创建下载函数xferFunc，xferFunc函数内部初始化一个downloadTransfer 并返回
			xferFunc = ldm.makeDownloadFunc(descriptor, "", topDownload) //(ldm *LayerDownloadManager) makeDownloadFunc
			defer topDownload.Transfer.Release(watcher)
		} else {
			xferFunc = ldm.makeDownloadFunc(descriptor, rootFS.ChainID(), nil)
		}

		//Transfer函数位于/distribution/xfer/transfer.go，调用xferFunc创建 downloadTransfer并把通道信息传给xferFunc创建的协程，返回Transfer, Watcher
		//(tm *transferManager) Transfer
		topDownloadUncasted, watcher = ldm.tm.Transfer(transferKey, xferFunc, progressOutput)
		topDownload = topDownloadUncasted.(*downloadTransfer) //接口是不是downloadTransfer 类型
		downloadsByKey[key] = topDownload
	}

	if topDownload == nil {
		return rootFS, func() {
			if topLayer != nil {
				layer.ReleaseAndLog(ldm.layerStore, topLayer)
			}
		}, nil
	}

	// Won't be using the list built up so far - will generate it
	// from downloaded layers instead.
	rootFS.DiffIDs = []layer.DiffID{}

	defer func() {
		if topLayer != nil {
			layer.ReleaseAndLog(ldm.layerStore, topLayer)
		}
	}()

	select {
	case <-ctx.Done():
		topDownload.Transfer.Release(watcher)
		return rootFS, func() {}, ctx.Err()
	case <-topDownload.Done():
		break
	}

	l, err := topDownload.result()
	if err != nil {
		topDownload.Transfer.Release(watcher)
		return rootFS, func() {}, err
	}

	// Must do this exactly len(layers) times, so we don't include the
	// base layer on Windows.
	for range layers {
		if l == nil {
			topDownload.Transfer.Release(watcher)
			return rootFS, func() {}, errors.New("internal error: too few parent layers")
		}
		rootFS.DiffIDs = append([]layer.DiffID{l.DiffID()}, rootFS.DiffIDs...)
		l = l.Parent()
	}
	return rootFS, func() { topDownload.Transfer.Release(watcher) }, err
}

// makeDownloadFunc returns a function that performs the layer download and
// registration. If parentDownload is non-nil, it waits for that download to
// complete before the registration step, and registers the downloaded data
// on top of parentDownload's resulting layer. Otherwise, it registers the
// layer on top of the ChainID given by parentLayer.
//返回这个函数
//xferFunc内部创建一个协程处理progressChan通道，调用descriptor.Download(..) , 这个是pull_v2.go内部初始化的v2LayerDescriptor，返回 io.ReadCloser，
//再通过io.ReadCloser 创建 reader解压数据，利用 downloadTransfer 处理数据。
func (ldm *LayerDownloadManager) makeDownloadFunc(descriptor DownloadDescriptor, parentLayer layer.ChainID, parentDownload *downloadTransfer) DoFunc {
	return func(progressChan chan<- progress.Progress, start <-chan struct{}, inactive chan<- struct{}) Transfer {
		d := &downloadTransfer{
			Transfer:   NewTransfer(),
			layerStore: ldm.layerStore,
		}

		go func() {
			defer func() {
				close(progressChan)
			}()

			progressOutput := progress.ChanOutput(progressChan)

			select {
			case <-start:
			default:
				progress.Update(progressOutput, descriptor.ID(), "Waiting")
				<-start
			}

			if parentDownload != nil {
				// Did the parent download already fail or get
				// cancelled?
				select {
				case <-parentDownload.Done():
					_, err := parentDownload.result()
					if err != nil {
						d.err = err
						return
					}
				default:
				}
			}

			var (
				downloadReader io.ReadCloser
				size           int64
				err            error
				retries        int
			)

			defer descriptor.Close()

			for {
				//(ld *v2LayerDescriptor) Download  下载
				downloadReader, size, err = descriptor.Download(d.Transfer.Context(), progressOutput)
				if err == nil {
					break
				}

				// If an error was returned because the context
				// was cancelled, we shouldn't retry.
				select {
				case <-d.Transfer.Context().Done():
					d.err = err
					return
				default:
				}

				retries++
				if _, isDNR := err.(DoNotRetry); isDNR || retries == maxDownloadAttempts {
					logrus.Errorf("Download failed: %v", err)
					d.err = err
					return
				}

				logrus.Errorf("Download failed, retrying: %v", err)
				delay := retries * 5
				ticker := time.NewTicker(ldm.waitDuration)

			selectLoop:
				for {
					progress.Updatef(progressOutput, descriptor.ID(), "Retrying in %d second%s", delay, (map[bool]string{true: "s"})[delay != 1])
					select {
					case <-ticker.C:
						delay--
						if delay == 0 {
							ticker.Stop()
							break selectLoop
						}
					case <-d.Transfer.Context().Done():
						ticker.Stop()
						d.err = errors.New("download cancelled during retry delay")
						return
					}

				}
			}

			close(inactive)

			if parentDownload != nil {
				select {
				case <-d.Transfer.Context().Done():
					d.err = errors.New("layer registration cancelled")
					downloadReader.Close()
					return
				case <-parentDownload.Done():
				}

				l, err := parentDownload.result()
				if err != nil {
					d.err = err
					downloadReader.Close()
					return
				}
				parentLayer = l.ChainID()
			}

			reader := progress.NewProgressReader(ioutils.NewCancelReadCloser(d.Transfer.Context(), downloadReader), progressOutput, size, descriptor.ID(), "Extracting")
			defer reader.Close()

			inflatedLayerData, err := archive.DecompressStream(reader)
			if err != nil {
				d.err = fmt.Errorf("could not get decompression stream: %v", err)
				return
			}

			var src distribution.Descriptor
			if fs, ok := descriptor.(distribution.Describable); ok {
				src = fs.Descriptor()
			}
			if ds, ok := d.layerStore.(layer.DescribableStore); ok {
				d.layer, err = ds.RegisterWithDescriptor(inflatedLayerData, parentLayer, src)
			} else {
				d.layer, err = d.layerStore.Register(inflatedLayerData, parentLayer)
			}
			if err != nil {
				select {
				case <-d.Transfer.Context().Done():
					d.err = errors.New("layer registration cancelled")
				default:
					d.err = fmt.Errorf("failed to register layer: %v", err)
				}
				return
			}

			progress.Update(progressOutput, descriptor.ID(), "Pull complete")
			withRegistered, hasRegistered := descriptor.(DownloadDescriptorWithRegistered)
			if hasRegistered {
				withRegistered.Registered(d.layer.DiffID())
			}

			// Doesn't actually need to be its own goroutine, but
			// done like this so we can defer close(c).
			go func() {
				<-d.Transfer.Released()
				if d.layer != nil {
					layer.ReleaseAndLog(d.layerStore, d.layer)
				}
			}()
		}()

		return d
	}
}

// makeDownloadFuncFromDownload returns a function that performs the layer
// registration when the layer data is coming from an existing download. It
// waits for sourceDownload and parentDownload to complete, and then
// reregisters the data from sourceDownload's top layer on top of
// parentDownload. This function does not log progress output because it would
// interfere with the progress reporting for sourceDownload, which has the same
// Key.
func (ldm *LayerDownloadManager) makeDownloadFuncFromDownload(descriptor DownloadDescriptor, sourceDownload *downloadTransfer, parentDownload *downloadTransfer) DoFunc {
	return func(progressChan chan<- progress.Progress, start <-chan struct{}, inactive chan<- struct{}) Transfer {
		d := &downloadTransfer{
			Transfer:   NewTransfer(),
			layerStore: ldm.layerStore,
		}

		go func() {
			defer func() {
				close(progressChan)
			}()

			<-start

			close(inactive)

			select {
			case <-d.Transfer.Context().Done():
				d.err = errors.New("layer registration cancelled")
				return
			case <-parentDownload.Done():
			}

			l, err := parentDownload.result()
			if err != nil {
				d.err = err
				return
			}
			parentLayer := l.ChainID()

			// sourceDownload should have already finished if
			// parentDownload finished, but wait for it explicitly
			// to be sure.
			select {
			case <-d.Transfer.Context().Done():
				d.err = errors.New("layer registration cancelled")
				return
			case <-sourceDownload.Done():
			}

			l, err = sourceDownload.result()
			if err != nil {
				d.err = err
				return
			}

			layerReader, err := l.TarStream()
			if err != nil {
				d.err = err
				return
			}
			defer layerReader.Close()

			var src distribution.Descriptor
			if fs, ok := l.(distribution.Describable); ok {
				src = fs.Descriptor()
			}
			if ds, ok := d.layerStore.(layer.DescribableStore); ok {
				d.layer, err = ds.RegisterWithDescriptor(layerReader, parentLayer, src)
			} else {
				d.layer, err = d.layerStore.Register(layerReader, parentLayer)
			}
			if err != nil {
				d.err = fmt.Errorf("failed to register layer: %v", err)
				return
			}

			withRegistered, hasRegistered := descriptor.(DownloadDescriptorWithRegistered)
			if hasRegistered {
				withRegistered.Registered(d.layer.DiffID())
			}

			// Doesn't actually need to be its own goroutine, but
			// done like this so we can defer close(c).
			go func() {
				<-d.Transfer.Released()
				if d.layer != nil {
					layer.ReleaseAndLog(d.layerStore, d.layer)
				}
			}()
		}()

		return d
	}
}
