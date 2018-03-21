package lxcfs

import (
	"github.com/docker/docker/api/server/router"
)

// systemRouter provides information about the Docker system overall.
// It gathers information about host, daemon and container events.
type lxcfsRouter struct {
	backend Backend
	routes  []router.Route
}

// NewRouter initializes a new lxcfs router
func NewRouter(b Backend) router.Router {
	r := &lxcfsRouter{
		backend: b,
	}

	r.routes = []router.Route{
		router.NewGetRoute("/lxcfs/info", r.getLxcfsInfo),
	}

	return r
}

// Routes returns all the API routes dedicated to the docker system
func (s *lxcfsRouter) Routes() []router.Route {
	return s.routes
}
