package image

import (
	"github.com/docker/docker/api/server/httputils"
	"github.com/docker/docker/api/server/router"
)

// imageRouter is a router to talk with the image controller
//NewRouter 中构造该类， initRouter->NewRouter 中对这些成员进行填充
type imageRouter struct {
	backend Backend  //daemon.Daemon 类型，赋值见 initRouter
	decoder httputils.ContainerDecoder //runconfig.ContainerDecoder 类型，赋值见initRouter
	routes  []router.Route //赋值见下面的 NewRouter
}

// NewRouter initializes a new image router
//initRouter 中调用
func NewRouter(backend Backend, decoder httputils.ContainerDecoder) router.Router {
	r := &imageRouter{
		backend: backend,
		decoder: decoder,
	}
	r.initRoutes()
	return r
}

// Routes returns the available routes to the image controller
func (r *imageRouter) Routes() []router.Route {
	return r.routes
}

// initRoutes initializes the routes in the image router
func (r *imageRouter) initRoutes() { //Docker镜像存储相关数据结构 见http://www.infocool.net/kb/OtherCloud/201705/346489.html
	r.routes = []router.Route{
		// GET
		router.NewGetRoute("/images/json", r.getImagesJSON),
		router.NewGetRoute("/images/search", r.getImagesSearch),
		router.NewGetRoute("/images/get", r.getImagesGet),
		router.NewGetRoute("/images/{name:.*}/get", r.getImagesGet),
		router.NewGetRoute("/images/{name:.*}/history", r.getImagesHistory),
		router.NewGetRoute("/images/{name:.*}/json", r.getImagesByName),
		// POST
		router.NewPostRoute("/commit", r.postCommit),
		router.NewPostRoute("/images/load", r.postImagesLoad),
		//docker pull走到这里     客户端 (cli *Client) ImagePull 和 服务端  r.postImagesCreate) 对应
		router.Cancellable(router.NewPostRoute("/images/create", r.postImagesCreate)),
		router.Cancellable(router.NewPostRoute("/images/{name:.*}/push", r.postImagesPush)),
		router.NewPostRoute("/images/{name:.*}/tag", r.postImagesTag),
		router.NewPostRoute("/images/prune", r.postImagesPrune),
		// DELETE
		router.NewDeleteRoute("/images/{name:.*}", r.deleteImages),
	}
}
