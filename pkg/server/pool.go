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

	ContainerCommittingLock map[string]*sync.Mutex
	containerStateMap       map[string]runtimeapi.ContainerState
	CommitStatusMap         map[string]types.CommitStatus

	queues map[string]chan types.Task
	mutex  sync.Mutex
	client runtimeapi.RuntimeServiceClient
}

func NewPool(capability int, client runtimeapi.RuntimeServiceClient, f func(task types.Task) error) (*Pool, error) {
	p := &Pool{
		Capability:              capability,
		queues:                  make(map[string]chan types.Task),
		containerStateMap:       make(map[string]runtimeapi.ContainerState),
		ContainerCommittingLock: make(map[string]*sync.Mutex),
		CommitStatusMap:         make(map[string]types.CommitStatus),
		client:                  client,
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
	for {
		if p.pool.IsClosed() {
			break
		}
	}
}

func (p *Pool) SubmitTask(task types.Task) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.CommitStatusMap[task.ContainerID] == types.RemoveCommit {
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
			p.containerStateMap[task.ContainerID] = task.ContainerState
			p.sendTask(task)
		}
	} else {
		p.sendTask(task)
	}
}

func (p *Pool) sendTask(task types.Task) {
	if _, exists := p.queues[task.ContainerID]; !exists {
		queue := make(chan types.Task, 5)
		p.queues[task.ContainerID] = queue
		p.ContainerCommittingLock[task.ContainerID] = &sync.Mutex{}
		go p.startConsumer(queue)
	}
	select {
	case p.queues[task.ContainerID] <- task:
		slog.Info("Add task to the queue", "ContainerID", task.ContainerID, "Kind", task.Kind)
	default:
		slog.Info("throw task when queue full", "ContainerID", task.ContainerID, "Kind", task.Kind)
	}
}

func (p *Pool) startConsumer(queue chan types.Task) {
	for {
		select {
		case task, ok := <-queue:
			if !ok {
				slog.Info("Queue closed", "ContainerID", task.ContainerID)
				return
			}
			p.ContainerCommittingLock[task.ContainerID].Lock()
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
	p.CommitStatusMap[containerID] = types.RemoveCommit
	delete(p.containerStateMap, containerID)
	if ch, exists := p.queues[containerID]; exists {
		close(ch)
		delete(p.queues, containerID)
		slog.Info("Queue destroyed", "ContainerID", containerID)
	}
	slog.Info("Tasks cleared", "ContainerID", containerID)
}

func (p *Pool) SetCommitStatus(containerID string, status types.CommitStatus) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if _, exists := p.CommitStatusMap[containerID]; !exists {
		p.CommitStatusMap[containerID] = status
	}
}

func (p *Pool) GetCommitStatus(containerID string) (bool, types.CommitStatus) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if status, exists := p.CommitStatusMap[containerID]; exists {
		return exists, status
	}
	return false, types.NoneCommit
}
