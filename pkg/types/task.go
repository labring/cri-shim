package types

import (
	"context"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type Kind int

const (
	KindRemove Kind = iota
	KindStop
	KindStatus
)

type Task struct {
	Ctx            context.Context
	Kind           Kind
	ContainerID    string
	ContainerState runtimeapi.ContainerState
}
