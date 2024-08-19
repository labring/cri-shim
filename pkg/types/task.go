package types

import "context"

type Kind int

const (
	KindRemove Kind = iota
	KindStop
	KindStatus
)

type Task struct {
	Ctx         context.Context
	Kind        Kind
	ContainerID string
}
