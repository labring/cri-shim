package types

import (
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type Kind int

const (
	KindRemove Kind = iota
	KindStop
	KindStatus
)

type Task struct {
	Kind           Kind
	ContainerID    string
	ContainerState runtimeapi.ContainerState
}
