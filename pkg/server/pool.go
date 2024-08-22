package server

import (
	"log/slog"
	"sync"

	"github.com/labring/cri-shim/pkg/types"
	"github.com/panjf2000/ants/v2"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// Pool is a pool of goroutines
type Pool struct {
	pool       *ants.PoolWithFunc
	Capability int

	containerCommitting map[string]chan struct{}
	containerStateMap   map[string]runtimeapi.ContainerState
	containerFinMap     map[string]bool

	queues map[string]chan types.Task
	mutex  sync.Mutex
	client runtimeapi.RuntimeServiceClient
}

func NewPool(capability int, client runtimeapi.RuntimeServiceClient, f func(task types.Task) error) (*Pool, error) {
	p := &Pool{
		Capability:          capability,
		queues:              make(map[string]chan types.Task),
		containerCommitting: make(map[string]chan struct{}),
		containerStateMap:   make(map[string]runtimeapi.ContainerState),
		containerFinMap:     make(map[string]bool),
		client:              client,
	}

	var err error
	if p.pool, err = ants.NewPoolWithFunc(capability, func(i interface{}) {
		n := i.(types.Task)
		f(n)
	}); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Pool) Close() {
	slog.Info("Close pool")
	p.pool.Release()
}

func (p *Pool) SubmitTask(task types.Task) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.containerFinMap[task.ContainerID] {
		slog.Info("Skip task, commit finished", "ContainerID", task.ContainerID, "Kind", task.Kind)
		return
	}
	if task.Kind == types.KindStatus {
		if task.ContainerState == runtimeapi.ContainerState_CONTAINER_UNKNOWN {
			slog.Debug("Skip task, invalid state", "ContainerID", task.ContainerID, "Kind", task.Kind, "State", task.ContainerState)
			return
		}
		lastState, exists := p.containerStateMap[task.ContainerID]
		if !exists {
			p.containerStateMap[task.ContainerID] = task.ContainerState
			slog.Debug("Skip task, no state", "ContainerID", task.ContainerID, "Kind", task.Kind)
		}
		if task.ContainerState == lastState {
			slog.Debug("Skip task, same state", "ContainerID", task.ContainerID, "Kind", task.Kind)
			return
		}

		if task.ContainerState == runtimeapi.ContainerState_CONTAINER_EXITED {
			slog.Info("Add task to the queue because container stopped", "ContainerID", task.ContainerID, "Kind", task.Kind)
			p.containerStateMap[task.ContainerID] = task.ContainerState
			p.getQueue(task.ContainerID) <- task
		}
	} else {
		slog.Info("Add task to the queue", "ContainerID", task.ContainerID, "Kind", task.Kind)
		p.getQueue(task.ContainerID) <- task
	}
}

func (p *Pool) getQueue(containerID string) chan types.Task {
	if _, exists := p.queues[containerID]; !exists {
		queue := make(chan types.Task, 20)
		p.queues[containerID] = queue
		go p.startConsumer(queue)
	}
	return p.queues[containerID]
}

func (p *Pool) startConsumer(queue chan types.Task) {
	for {
		select {
		case task, ok := <-queue:
			if !ok {
				slog.Info("Queue closed", "ContainerID", task.ContainerID)
				return
			}
			slog.Info("Start to process task", "ContainerID", task.ContainerID, "Kind", task.Kind)
			if err := p.pool.Invoke(task); err != nil {
				slog.Error("Error happen when container commit", "error", err)
			}
		}
	}
}

// ClearTasks clears all tasks in the queue associated with the given containerID without closing the channel
func (p *Pool) ClearTasks(containerID string) {
	slog.Info("Clear tasks", "ContainerID", containerID)
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.containerFinMap[containerID] = true
	delete(p.containerStateMap, containerID)
	if ch, exists := p.queues[containerID]; exists {
		close(ch)
		delete(p.queues, containerID)
		slog.Info("Queue destroyed", "ContainerID", containerID)
	}
	slog.Info("Tasks cleared", "ContainerID", containerID)
}
