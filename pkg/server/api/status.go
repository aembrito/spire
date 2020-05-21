package api

import (
	"fmt"

	"github.com/spiffe/spire/proto/spire-next/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CreateStatus creates a Status Proto
func CreateStatus(code codes.Code, format string, a ...interface{}) *types.Status {
	return &types.Status{
		Code:    int32(code),
		Message: fmt.Sprintf(format, a...),
	}
}

func StatusFromError(err error) *types.Status {
	if err == nil {
		return nil
	}
	// Parse error into grpc status, if status fails to parse it will return an status with `Unknown` status code
	s, _ := status.FromError(err)
	return CreateStatus(s.Code(), s.Message())

}
