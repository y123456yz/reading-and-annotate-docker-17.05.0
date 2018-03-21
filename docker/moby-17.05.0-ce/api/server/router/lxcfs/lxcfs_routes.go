package lxcfs

import (
	"fmt"
	"net/http"
    "github.com/docker/docker/api/server/httputils"

	"golang.org/x/net/context"
)

func (s *lxcfsRouter) getLxcfsInfo(ctx context.Context, w http.ResponseWriter, r *http.Request, vars map[string]string) error {
	info, err := s.backend.LxcfsInfo()
	if err != nil {
		return err
	}

	fmt.Printf("yang test ... info:%v\n", info)
	return httputils.WriteJSON(w, http.StatusOK, info)
}
