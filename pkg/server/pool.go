package server

import (
	"github.com/labring/cri-shim/pkg/types"
	"github.com/panjf2000/ants/v2"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	"log/slog"
	"sync"
	"time"
)

// Pool is a pool of goroutines
type Pool struct {
	pool       *ants.PoolWithFunc
	Capability int
	time       map[string]time.Time
	queues     map[string]chan types.Task
	mutex      sync.Mutex
	client     runtimeapi.RuntimeServiceClient
}

func NewPool(capability int, client runtimeapi.RuntimeServiceClient, f func(task types.Task) error) (*Pool, error) {
	p := &Pool{
		Capability: capability,
		time:       make(map[string]time.Time),
		queues:     make(map[string]chan types.Task),
		client:     client,
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
	lastTime, exists := p.getTime(task.ContainerID)
	// if the task is a remove task, a stop task, or the container does not commit before, or the last task is more than 10 minutes ago add the task to the queue
	if task.Kind == types.KindRemove || task.Kind == types.KindStop || !exists || time.Since(lastTime) > 10*time.Minute {
		slog.Info("Add task to the queue", "ContainerID", task.ContainerID, "Kind", task.Kind)
		p.setTime(task.ContainerID, time.Now())
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
		if err := p.pool.Invoke(task); err != nil {
			slog.Error("Error happen when container commit", "error", err)
		}
	}
}

func (p *Pool) getTime(containerID string) (time.Time, bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	value, exists := p.time[containerID]
	return value, exists
}

func (p *Pool) setTime(containerID string, t time.Time) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.time[containerID] = t
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
	if ch, exists := p.queues[containerID]; exists {
		close(ch)
		delete(p.queues, containerID)
		slog.Info("Queue destroyed", "ContainerID", containerID)
	}
}
