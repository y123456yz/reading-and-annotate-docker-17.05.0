package server

import (
	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/api/server/httputils"
	"github.com/docker/docker/api/server/middleware"
)

// handlerWithGlobalMiddlewares wraps the handler function for a request with
// the server's global middlewares. The order of the middlewares is backwards,
// meaning that the first in the list will be evaluated last.
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
//(s *Server) makeHTTPHandler 中调用执行
func (s *Server) handlerWithGlobalMiddlewares(handler httputils.APIFunc) httputils.APIFunc {
	next := handler

	for _, m := range s.middlewares {
		next = m.WrapHandler(next)
	}

	if s.cfg.Logging && logrus.GetLevel() == logrus.DebugLevel {
		next = middleware.DebugRequestMiddleware(next)
	}

	return next
}
