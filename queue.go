package smartlimbo

import (
	"strings"
	"sync"
	"time"

	"go.minekube.com/gate/pkg/edition/java/proxy"
	"go.minekube.com/gate/pkg/util/uuid"
)

// QueuedPlayer stores a player waiting in a reconnect queue.
type QueuedPlayer struct {
	Player       proxy.Player
	TargetServer string
	EnqueuedAt   time.Time
}

// QueueManager manages per-server smart queues with thread safety.
type QueueManager struct {
	mu      sync.RWMutex
	queues  map[string][]uuid.UUID
	players map[uuid.UUID]*QueuedPlayer
}

// NewQueueManager creates a new QueueManager instance.
func NewQueueManager() *QueueManager {
	return &QueueManager{
		queues:  make(map[string][]uuid.UUID),
		players: make(map[uuid.UUID]*QueuedPlayer),
	}
}

// Enqueue adds a player to the queue for targetServer and returns their 1-indexed position.
func (qm *QueueManager) Enqueue(targetServer string, player proxy.Player) int {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	targetServer = strings.ToLower(strings.TrimSpace(targetServer))
	id := player.ID()

	// If player is already in a queue, remove them first
	if existing, found := qm.players[id]; found {
		qm.removeFromQueue(existing.TargetServer, id)
	}

	qp := &QueuedPlayer{
		Player:       player,
		TargetServer: targetServer,
		EnqueuedAt:   time.Now(),
	}

	qm.players[id] = qp
	qm.queues[targetServer] = append(qm.queues[targetServer], id)

	return len(qm.queues[targetServer])
}

// Remove removes a player from any queue they are in.
func (qm *QueueManager) Remove(id uuid.UUID) (string, bool) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	qp, found := qm.players[id]
	if !found {
		return "", false
	}

	target := qp.TargetServer
	qm.removeFromQueue(target, id)
	delete(qm.players, id)

	return target, true
}

func (qm *QueueManager) removeFromQueue(target string, id uuid.UUID) {
	list := qm.queues[target]
	newList := make([]uuid.UUID, 0, len(list))
	for _, entryID := range list {
		if entryID != id {
			newList = append(newList, entryID)
		}
	}
	if len(newList) == 0 {
		delete(qm.queues, target)
	} else {
		qm.queues[target] = newList
	}
}

// GetPosition returns the target server, 1-indexed position, total players, and whether the player is queued.
func (qm *QueueManager) GetPosition(id uuid.UUID) (targetServer string, position int, total int, found bool) {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	qp, exists := qm.players[id]
	if !exists {
		return "", 0, 0, false
	}

	list := qm.queues[qp.TargetServer]
	for idx, entryID := range list {
		if entryID == id {
			return qp.TargetServer, idx + 1, len(list), true
		}
	}

	return qp.TargetServer, 1, 1, true
}

// GetNextBatch pops up to limit players from targetServer's queue.
func (qm *QueueManager) GetNextBatch(targetServer string, limit int) []*QueuedPlayer {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	targetServer = strings.ToLower(strings.TrimSpace(targetServer))
	list := qm.queues[targetServer]
	if len(list) == 0 {
		return nil
	}

	count := limit
	if count > len(list) {
		count = len(list)
	}

	result := make([]*QueuedPlayer, 0, count)
	remaining := list[count:]

	for i := 0; i < count; i++ {
		id := list[i]
		if qp, found := qm.players[id]; found {
			result = append(result, qp)
			delete(qm.players, id)
		}
	}

	if len(remaining) == 0 {
		delete(qm.queues, targetServer)
	} else {
		qm.queues[targetServer] = remaining
	}

	return result
}

// GetActiveTargetServers returns all server names currently having waiting players.
func (qm *QueueManager) GetActiveTargetServers() []string {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	servers := make([]string, 0, len(qm.queues))
	for srv, list := range qm.queues {
		if len(list) > 0 {
			servers = append(servers, srv)
		}
	}
	return servers
}

// GetQueueCount returns the count of players in a specific queue.
func (qm *QueueManager) GetQueueCount(targetServer string) int {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	return len(qm.queues[strings.ToLower(strings.TrimSpace(targetServer))])
}

// TotalQueuedPlayers returns the total count of queued players across all servers.
func (qm *QueueManager) TotalQueuedPlayers() int {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	return len(qm.players)
}

// GetAllQueuesSummary returns a summary map of server name to queue length.
func (qm *QueueManager) GetAllQueuesSummary() map[string]int {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	summary := make(map[string]int, len(qm.queues))
	for srv, list := range qm.queues {
		summary[srv] = len(list)
	}
	return summary
}
