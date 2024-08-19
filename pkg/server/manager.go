package server

import (
	"context"
	"sync"
)

type CommitManager struct {
	waitCh map[string]chan struct{}
	mutex  sync.Mutex
}

// NewCommitManager initializes a new CommitManager
func NewCommitManager() *CommitManager {
	return &CommitManager{
		waitCh: make(map[string]chan struct{}, 3),
	}
}

// Set sets a key-value pair in the waitCh
func (cm *CommitManager) Set(key string) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	cm.waitCh[key] = make(chan struct{}, 3)
}

// Get chan a value from the waitCh
func (cm *CommitManager) Get(key string) (chan struct{}, bool) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	value, exists := cm.waitCh[key]
	return value, exists
}

// Remove deletes the chan from the waitCh
func (cm *CommitManager) Remove(key string) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	if ch, exists := cm.waitCh[key]; exists {
		close(ch)
		delete(cm.waitCh, key)
	}
}

// Notify sends a signal to waitCh
func (cm *CommitManager) Notify(id string) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	if ch, exists := cm.waitCh[id]; exists {
		ch <- struct{}{}
	}
}

// WaitForCommit waits for a signal on waitCh or context cancellation
func (cm *CommitManager) WaitForCommit(id string, ctx context.Context) error {
	if _, exit := cm.Get(id); !exit {
		return nil
	}
	select {
	case <-cm.waitCh[id]:
		cm.Remove(id)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
