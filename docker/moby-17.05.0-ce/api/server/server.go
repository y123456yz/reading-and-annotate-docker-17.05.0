package server

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/api/errors"
	"github.com/docker/docker/api/server/httputils"
	"github.com/docker/docker/api/server/middleware"
	"github.com/docker/docker/api/server/router"
	"github.com/docker/docker/dockerversion"
	"github.com/gorilla/mux"
	"golang.org/x/net/context"
)

// versionMatcher defines a variable matcher to be parsed by the router
// when a request is about to be served.
const versionMatcher = "/v{version:[0-9.]+}"

// Config provides the configuration for the API server
//serverConfig 中使用
type Config struct { ///  server 的config文件创建以及一些赋值
	Logging     bool
	EnableCors  bool
	CorsHeaders string
	Version     string
	SocketGroup string
	TLSConfig   *tls.Config
}

// Server contains instance details for the server
//New函数返回了一个Server的实例，我理解一个Server可以应对不同的协议（http，tcp等等），所以Server有一个servers的数组，
// //其中的每一个元素对应服务一种协议的请求；

//下面的New中构造该结构，在initRouter中api server匹配处理各自url请求
type Server struct { //server.go中func (s *Server) Wait(waitChan chan error)中使用
	cfg           *Config
	servers       []*HTTPServer //赋值见下面的Accept
	//赋值见(s *Server) InitRouter， 生效见(s *Server) createMux()
	routers       []router.Router
	//赋值见(s *Server) InitRouter
	routerSwapper *routerSwapper //生效见serveAPI
	middlewares   []middleware.Middleware
}

// New returns a new instance of the server based on the specified configuration.
// It allocates resources which will be needed for ServeAPI(ports, unix-sockets).
func New(cfg *Config) *Server { //daemonCli.start(opts)中调用，见api := apiserver.New(serverConfig)
	return &Server{
		cfg: cfg,
	}
}

// UseMiddleware appends a new middleware to the request chain.
// This needs to be called before the API routes are configured.
//(cli *DaemonCli) initMiddlewares 中执行，赋值 Server.middlewares
func (s *Server) UseMiddleware(m middleware.Middleware) {
	s.middlewares = append(s.middlewares, m)
}

// Accept sets a listener the server accepts connections into.
func (s *Server) Accept(addr string, listeners ...net.Listener) {
	for _, listener := range listeners {
		httpServer := &HTTPServer{
			srv: &http.Server{
				Addr: addr,
			},
			l: listener,
		}
		s.servers = append(s.servers, httpServer) //往数组添加成员
	}
}

// Close closes servers and thus stop receiving requests
func (s *Server) Close() {
	for _, srv := range s.servers {
		if err := srv.Close(); err != nil {
			logrus.Error(err)
		}
	}
}

// serveAPI loops through all initialized servers and spawns goroutine
// with Serve method for each. It sets createMux() as Handler also.
//创建一个ServerApiWait阻塞通道， 采用一个go routine 来启动api的ServerAPI，ServerAPI的代码在api/server/server.go中
func (s *Server) serveAPI() error {
	var chErrors = make(chan error, len(s.servers))

	//每一个地址开启一个新的goroutine 启动一个server来对外提供服务，如果启动过程中有错误，则将错误信息放入chErrors通道中。
	// //最后便利chErrors的通道，如果有错误，则将错误作为返回值返回；
	for _, srv := range s.servers { //相应的客户端post请求的回调见 initRouter
		srv.srv.Handler = s.routerSwapper
		go func(srv *HTTPServer) {
			var err error
			logrus.Infof("API listen on %s", srv.l.Addr())
			if err = srv.Serve(); err != nil && strings.Contains(err.Error(), "use of closed network connection") {
				err = nil
			}
			chErrors <- err
		}(srv)
	}

	for i := 0; i < len(s.servers); i++ {
		err := <-chErrors
		if err != nil {
			return err
		}
	}

	return nil
}

// HTTPServer contains an instance of http server and the listener.
// srv *http.Server, contains configuration to create an http server and a mux router with all api end points.
// l   net.Listener, is a TCP or Socket listener that dispatches incoming request to the router.
type HTTPServer struct { //构造见server\server.go中的Accept
	srv *http.Server
	l   net.Listener
}

// Serve starts listening for inbound requests.
func (s *HTTPServer) Serve() error {
	return s.srv.Serve(s.l)
}

// Close closes the HTTPServer from listening for the inbound requests.
func (s *HTTPServer) Close() error {
	return s.l.Close()
}

//注意这里返回的是函数，该函数在本函数中不会执行，只是简单的返回
//(s *Server) createMux 中做函数赋值，真正执行是由 github.com/gorilla/mux 异步触发
func (s *Server) makeHTTPHandler(handler httputils.APIFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Define the context that we'll pass around to share info
		// like the docker-request-id.
		//
		// The 'context' will be used for global data that should
		// apply to all requests. Data that is specific to the
		// immediate function being called should still be passed
		// as 'args' on the function call.
		ctx := context.WithValue(context.Background(), dockerversion.UAStringKey, r.Header.Get("User-Agent"))
		handlerFunc := s.handlerWithGlobalMiddlewares(handler)

		vars := mux.Vars(r)
		if vars == nil {
			vars = make(map[string]string)
		}

		if err := handlerFunc(ctx, w, r, vars); err != nil {
			statusCode := httputils.GetHTTPErrorStatusCode(err)
			if statusCode >= 500 {
				logrus.Errorf("Handler for %s %s returned error: %v", r.Method, r.URL.Path, err)
			}
			httputils.MakeErrorHandler(err)(w, r)
		}
	}
}

// InitRouter initializes the list of routers for the server.
// This method also enables the Go profiler if enableProfiler is true.
//dockerd\daemon.go 中的 func initRouter 中执行
func (s *Server) InitRouter(enableProfiler bool, routers ...router.Router) {
	s.routers = append(s.routers, routers...)

	//设置s.routers各种请求的回调
	m := s.createMux()
	if enableProfiler {
		profilerSetup(m)
	}
	s.routerSwapper = &routerSwapper{
		router: m,
	}
}

/*
DEBU[0001] Registering routers
DEBU[0001] Registering GET, /containers/{name:.*}/checkpoints
DEBU[0001] Registering POST, /containers/{name:.*}/checkpoints
DEBU[0001] Registering DELETE, /containers/{name}/checkpoints/{checkpoint}
DEBU[0001] Registering HEAD, /containers/{name:.*}/archive
DEBU[0001] Registering GET, /containers/json
DEBU[0001] Registering GET, /containers/{name:.*}/export
DEBU[0001] Registering GET, /containers/{name:.*}/changes
DEBU[0001] Registering GET, /containers/{name:.*}/json
DEBU[0001] Registering GET, /containers/{name:.*}/top
DEBU[0001] Registering GET, /containers/{name:.*}/logs
DEBU[0001] Registering GET, /containers/{name:.*}/stats
DEBU[0001] Registering GET, /containers/{name:.*}/attach/ws
DEBU[0001] Registering GET, /exec/{id:.*}/json
DEBU[0001] Registering GET, /containers/{name:.*}/archive
DEBU[0001] Registering POST, /containers/create
DEBU[0001] Registering POST, /containers/{name:.*}/kill
DEBU[0001] Registering POST, /containers/{name:.*}/pause
DEBU[0001] Registering POST, /containers/{name:.*}/unpause
DEBU[0001] Registering POST, /containers/{name:.*}/restart
DEBU[0001] Registering POST, /containers/{name:.*}/start
DEBU[0001] Registering POST, /containers/{name:.*}/stop
DEBU[0001] Registering POST, /containers/{name:.*}/wait
DEBU[0001] Registering POST, /containers/{name:.*}/resize
DEBU[0001] Registering POST, /containers/{name:.*}/attach
DEBU[0001] Registering POST, /containers/{name:.*}/copy
DEBU[0001] Registering POST, /containers/{name:.*}/exec
DEBU[0001] Registering POST, /exec/{name:.*}/start
DEBU[0001] Registering POST, /exec/{name:.*}/resize
DEBU[0001] Registering POST, /containers/{name:.*}/rename
DEBU[0001] Registering POST, /containers/{name:.*}/update
DEBU[0001] Registering POST, /containers/prune
DEBU[0001] Registering PUT, /containers/{name:.*}/archive
DEBU[0001] Registering DELETE, /containers/{name:.*}
DEBU[0001] Registering GET, /images/json
DEBU[0001] Registering GET, /images/search
DEBU[0001] Registering GET, /images/get
DEBU[0001] Registering GET, /images/{name:.*}/get
DEBU[0001] Registering GET, /images/{name:.*}/history
DEBU[0001] Registering GET, /images/{name:.*}/json
DEBU[0001] Registering POST, /commit
DEBU[0001] Registering POST, /images/load
DEBU[0001] Registering POST, /images/create
DEBU[0001] Registering POST, /images/{name:.*}/push
DEBU[0001] Registering POST, /images/{name:.*}/tag
DEBU[0001] Registering POST, /images/prune
DEBU[0001] Registering DELETE, /images/{name:.*}
DEBU[0001] Registering OPTIONS, /{anyroute:.*}
DEBU[0001] Registering GET, /_ping
DEBU[0001] Registering GET, /events
DEBU[0001] Registering GET, /info
DEBU[0001] Registering GET, /version
DEBU[0001] Registering GET, /system/df
DEBU[0001] Registering POST, /auth
DEBU[0001] Registering GET, /volumes
DEBU[0001] Registering GET, /volumes/{name:.*}
DEBU[0001] Registering POST, /volumes/create
DEBU[0001] Registering POST, /volumes/prune
DEBU[0001] Registering DELETE, /volumes/{name:.*}
DEBU[0001] Registering POST, /build
DEBU[0001] Registering POST, /swarm/init
DEBU[0001] Registering POST, /swarm/join
DEBU[0001] Registering POST, /swarm/leave
DEBU[0001] Registering GET, /swarm
DEBU[0001] Registering GET, /swarm/unlockkey
DEBU[0001] Registering POST, /swarm/update
DEBU[0001] Registering POST, /swarm/unlock
DEBU[0001] Registering GET, /services
DEBU[0001] Registering GET, /services/{id}
DEBU[0001] Registering POST, /services/create
DEBU[0001] Registering POST, /services/{id}/update
DEBU[0001] Registering DELETE, /services/{id}
DEBU[0001] Registering GET, /services/{id}/logs
DEBU[0001] Registering GET, /nodes
DEBU[0001] Registering GET, /nodes/{id}
DEBU[0001] Registering DELETE, /nodes/{id}
DEBU[0001] Registering POST, /nodes/{id}/update
DEBU[0001] Registering GET, /tasks
DEBU[0001] Registering GET, /tasks/{id}
DEBU[0001] Registering GET, /tasks/{id}/logs
DEBU[0001] Registering GET, /secrets
DEBU[0001] Registering POST, /secrets/create
DEBU[0001] Registering DELETE, /secrets/{id}
DEBU[0001] Registering GET, /secrets/{id}
DEBU[0001] Registering POST, /secrets/{id}/update
DEBU[0001] Registering GET, /plugins
DEBU[0001] Registering GET, /plugins/{name:.*}/json
DEBU[0001] Registering GET, /plugins/privileges
DEBU[0001] Registering DELETE, /plugins/{name:.*}
DEBU[0001] Registering POST, /plugins/{name:.*}/enable
DEBU[0001] Registering POST, /plugins/{name:.*}/disable
DEBU[0001] Registering POST, /plugins/pull
DEBU[0001] Registering POST, /plugins/{name:.*}/push
DEBU[0001] Registering POST, /plugins/{name:.*}/upgrade
DEBU[0001] Registering POST, /plugins/{name:.*}/set
DEBU[0001] Registering POST, /plugins/create
DEBU[0001] Registering GET, /networks
DEBU[0001] Registering GET, /networks/
DEBU[0001] Registering GET, /networks/{id:.+}
DEBU[0001] Registering POST, /networks/create
DEBU[0001] Registering POST, /networks/{id:.*}/connect
DEBU[0001] Registering POST, /networks/{id:.*}/disconnect
DEBU[0001] Registering POST, /networks/prune
DEBU[0001] Registering DELETE, /networks/{id:.*}
*/
// createMux initializes the main router the server uses.
//设置各种请求的回调
//设置s.routers各种请求的回调
func (s *Server) createMux() *mux.Router {
	m := mux.NewRouter()

	logrus.Debug("Registering routers")
	for _, apiRouter := range s.routers {
		for _, r := range apiRouter.Routes() {
			f := s.makeHTTPHandler(r.Handler())

			logrus.Debugf("Registering %s, %s", r.Method(), r.Path())
			//注册f回调
			m.Path(versionMatcher + r.Path()).Methods(r.Method()).Handler(f)
			m.Path(r.Path()).Methods(r.Method()).Handler(f)
		}
	}

	err := errors.NewRequestNotFoundError(fmt.Errorf("page not found"))
	notFoundHandler := httputils.MakeErrorHandler(err)
	m.HandleFunc(versionMatcher+"/{path:.*}", notFoundHandler)
	m.NotFoundHandler = notFoundHandler

	return m
}

// Wait blocks the server goroutine until it exits.
// It sends an error message if there is any error during
// the API execution.

//daemon.go中go api.Wait(serveAPIWait)执行
func (s *Server) Wait(waitChan chan error) {
	if err := s.serveAPI(); err != nil {
		logrus.Errorf("ServeAPI error: %v", err)
		waitChan <- err
		return
	}
	waitChan <- nil //触发外层的errAPI := <-serveAPIWait返回，然后daemon服务停止
}

// DisableProfiler reloads the server mux without adding the profiler routes.
func (s *Server) DisableProfiler() {
	s.routerSwapper.Swap(s.createMux())
}

// EnableProfiler reloads the server mux adding the profiler routes.
func (s *Server) EnableProfiler() {
	m := s.createMux()
	profilerSetup(m)
	s.routerSwapper.Swap(m)
}
