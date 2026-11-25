// Package sample is a unit-like extension of the server: ops declared on
// spec_server in its own Heron file, with generated handlers and client,
// mounted on the shell without changing it.
package sample

import (
	"context"
	"strings"

	"github.com/helightdev/specs-v0/internal/tools/server"
	"github.com/helightdev/specs-v0/internal/tools/server/api"
)

//go:generate go run ../../apigen -base ../../api/server.heron -ext sample.heron -go sample_gen.go -pkg sample -ts sample.gen.ts

type impl struct{}

func (impl) SampleEcho(ctx context.Context, name string, times int64) (*SampleEcho, error) {
	return &SampleEcho{Text: strings.Repeat(name, int(times))}, nil
}

// Extension is the sample's routes and a result provider that marks every
// task the sample's way.
func Extension() server.Extension {
	return server.Extension{
		Name:   "sample",
		Routes: Routes(impl{}),
		Results: []server.Results{func(ctx context.Context, w *server.Workspace, path string) []api.NodeResult {
			if path == "" {
				return nil
			}
			return []api.NodeResult{{Source: "sample", Path: path, Status: "ok", Summary: "sampled"}}
		}},
	}
}
