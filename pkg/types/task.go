package types

import "context"

type Task struct {
	Ctx         context.Context
	ContainerID string
	RemoveFlag  bool
}
