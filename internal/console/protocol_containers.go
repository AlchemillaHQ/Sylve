// SPDX-License-Identifier: BSD-2-Clause

package console

import "encoding/json"

const (
	OperationContainerRead  = "containers.read"
	OperationContainerWrite = "containers.write"
)

type ContainerPayload struct {
	Action  string          `json:"action"`
	OCID    uint            `json:"ocId,omitempty"`
	ID      uint            `json:"id,omitempty"`
	Pool    string          `json:"pool,omitempty"`
	Args    []string        `json:"args,omitempty"`
	Request json.RawMessage `json:"request,omitempty"`
	JSON    bool            `json:"json"`
}

type ContainerCommand struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
	Env  []string `json:"env"`
}

func LoadContainerFile[T any](path string) (T, error) {
	return loadStrictJSONFile[T](path, "container request")
}
