package middleware

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/api/server/httputils"
	"github.com/docker/docker/pkg/ioutils"
	"golang.org/x/net/context"
)

// DebugRequestMiddleware dumps the request to logger
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

/*
DEBU[12575] Calling GET /_ping
DEBU[12575] Calling GET /v1.29/containers/json?all=1
DEBU[12589] Calling GET /_ping
DEBU[12593] Calling GET /_ping
DEBU[12593] Calling POST /v1.29/containers/0c66c4ebc4a3/stop
DEBU[12601] Calling GET /_ping
DEBU[12601] Calling GET /v1.29/containers/json?all=1
DEBU[12626] Calling GET /_ping
DEBU[12626] Calling DELETE /v1.29/images/0c66c4ebc4a3?force=1
DEBU[12640] Calling GET /_ping
DEBU[12640] Calling GET /v1.29/containers/json?all=1
DEBU[12654] Calling GET /_ping
DEBU[12654] Calling DELETE /v1.29/images/0c66c4ebc4a3?force=1
*/
//handlerWithGlobalMiddlewares 中调用执行
func DebugRequestMiddleware(handler func(ctx context.Context, w http.ResponseWriter, r *http.Request, vars map[string]string) error) func(ctx context.Context, w http.ResponseWriter, r *http.Request, vars map[string]string) error {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, vars map[string]string) error {
		//各种客户端请求在这里打印 Calling GET /_ping   Calling DELETE等等
		logrus.Debugf("Calling %s %s", r.Method, r.RequestURI)

		if r.Method != "POST" {
			return handler(ctx, w, r, vars)
		}
		if err := httputils.CheckForJSON(r); err != nil {
			return handler(ctx, w, r, vars)
		}
		maxBodySize := 4096 // 4KB
		if r.ContentLength > int64(maxBodySize) {
			return handler(ctx, w, r, vars)
		}

		body := r.Body
		bufReader := bufio.NewReaderSize(body, maxBodySize)
		r.Body = ioutils.NewReadCloserWrapper(bufReader, func() error { return body.Close() })

		b, err := bufReader.Peek(maxBodySize)
		if err != io.EOF {
			// either there was an error reading, or the buffer is full (in which case the request is too large)
			return handler(ctx, w, r, vars)
		}

		var postForm map[string]interface{}
		if err := json.Unmarshal(b, &postForm); err == nil {
			maskSecretKeys(postForm)
			formStr, errMarshal := json.Marshal(postForm)
			if errMarshal == nil {
				logrus.Debugf("form data: %s", string(formStr))
			} else {
				logrus.Debugf("form data: %q", postForm)
			}
		}

		return handler(ctx, w, r, vars)
	}
}

func maskSecretKeys(inp interface{}) {
	if arr, ok := inp.([]interface{}); ok {
		for _, f := range arr {
			maskSecretKeys(f)
		}
		return
	}
	if form, ok := inp.(map[string]interface{}); ok {
	loop0:
		for k, v := range form {
			for _, m := range []string{"password", "secret", "jointoken", "unlockkey"} {
				if strings.EqualFold(m, k) {
					form[k] = "*****"
					continue loop0
				}
			}
			maskSecretKeys(v)
		}
	}
}
