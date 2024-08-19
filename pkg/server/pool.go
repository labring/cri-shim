package server

import (
	"github.com/labring/cri-shim/pkg/types"
	"github.com/panjf2000/ants/v2"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	"log/slog"
	"sync"
)

// Pool is a pool of goroutines
type Pool struct {
	pool       *ants.PoolWithFunc
	Capability int

	containerStateMap map[string]runtimeapi.ContainerState
	queues            map[string]chan types.Task
	mutex             sync.Mutex
	client            runtimeapi.RuntimeServiceClient
}

func NewPool(capability int, client runtimeapi.RuntimeServiceClient, f func(task types.Task) error) (*Pool, error) {
	p := &Pool{
		Capability:        capability,
		queues:            make(map[string]chan types.Task),
		containerStateMap: make(map[string]runtimeapi.ContainerState),
		client:            client,
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
	p.pool.Release()
}

func (p *Pool) SubmitTask(task types.Task) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if task.Kind == types.KindStatus {
		if state, exists := p.containerStateMap[task.ContainerID]; exists {
			if state == task.ContainerState {
				slog.Debug("Skip task, same state", "ContainerID", task.ContainerID, "Kind", task.Kind)
			} else if state != task.ContainerState && state != runtimeapi.ContainerState_CONTAINER_UNKNOWN && task.ContainerState != runtimeapi.ContainerState_CONTAINER_UNKNOWN {
				slog.Info("Add task to the queue because container state changed", "ContainerID", task.ContainerID, "Kind", task.Kind)
				p.containerStateMap[task.ContainerID] = task.ContainerState
				p.getQueue(task.ContainerID) <- task
			} else {
				slog.Error("Skip task, invalid state", "ContainerID", task.ContainerID, "Kind", task.Kind, "State", state, "NewState", task.ContainerState)
			}
		} else {
			slog.Debug("Skip task, no state", "ContainerID", task.ContainerID, "Kind", task.Kind)
		}
	} else {
		slog.Info("Add task to the queue", "ContainerID", task.ContainerID, "Kind", task.Kind)
		p.getQueue(task.ContainerID) <- task
	}
}

func (p *Pool) getQueue(containerID string) chan types.Task {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if _, exists := p.queues[containerID]; !exists {
		queue := make(chan types.Task, 10)
		p.queues[containerID] = queue
		go p.startConsumer(queue)
	}
	return p.queues[containerID]
}

func (p *Pool) startConsumer(queue chan types.Task) {
	for task := range queue {
		slog.Info("Start to process task", "ContainerID", task.ContainerID, "Kind", task.Kind)
		if err := p.pool.Invoke(task); err != nil {
			slog.Error("Error happen when container commit", "error", err)
		}
	}
}

// ClearTasks clears all tasks in the queue associated with the given containerID without closing the channel
func (p *Pool) ClearTasks(containerID string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if queue, exists := p.queues[containerID]; exists {
		for {
			select {
			case <-queue:
			default:
				slog.Info("All tasks cleared from the queue", "ContainerID", containerID)
				return
			}
		}
	}
	delete(p.containerStateMap, containerID)
	if ch, exists := p.queues[containerID]; exists {
		close(ch)
		delete(p.queues, containerID)
		slog.Info("Queue destroyed", "ContainerID", containerID)
	}
}
