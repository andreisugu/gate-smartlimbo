package smartlimbo

import (
	"slices"
	"sort"
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
	InFlight     bool
	Attempts     int
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
	if player == nil {
		return 0
	}
	qm.mu.Lock()
	defer qm.mu.Unlock()

	targetServer = strings.ToLower(strings.TrimSpace(targetServer))
	if targetServer == "" {
		return 0
	}
	id := player.ID()

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

// EnqueueFrontOrPreserve inserts or updates a player in targetServer's queue ordered by enqueuedAt
// so retried players retain their earlier queue spot. Returns the 1-indexed position.
func (qm *QueueManager) EnqueueFrontOrPreserve(targetServer string, player proxy.Player, enqueuedAt time.Time, attempts int) int {
	if player == nil {
		return 0
	}
	qm.mu.Lock()
	defer qm.mu.Unlock()

	targetServer = strings.ToLower(strings.TrimSpace(targetServer))
	if targetServer == "" {
		return 0
	}
	id := player.ID()

	if existing, found := qm.players[id]; found {
		qm.removeFromQueue(existing.TargetServer, id)
	}

	qp := &QueuedPlayer{
		Player:       player,
		TargetServer: targetServer,
		EnqueuedAt:   enqueuedAt,
		Attempts:     attempts,
	}
	qm.players[id] = qp

	list := append(qm.queues[targetServer], id)
	sort.SliceStable(list, func(i, j int) bool {
		pi := qm.players[list[i]]
		pj := qm.players[list[j]]
		if pi == nil || pj == nil {
			return false
		}
		return pi.EnqueuedAt.Before(pj.EnqueuedAt)
	})
	qm.queues[targetServer] = list

	for idx, entryID := range list {
		if entryID == id {
			return idx + 1
		}
	}
	return len(list)
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

// TryStartConnect marks a queued player as currently connecting to avoid concurrent attempts.
func (qm *QueueManager) TryStartConnect(id uuid.UUID) bool {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	qp, found := qm.players[id]
	if !found || qp.InFlight {
		return false
	}
	qp.InFlight = true
	qp.Attempts++
	return true
}

// FinishConnect completes an in-flight connection attempt. On success the player is removed;
// on failure their queue entry and original position are kept with InFlight cleared.
func (qm *QueueManager) FinishConnect(id uuid.UUID, succeeded bool) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	qp, found := qm.players[id]
	if !found {
		return
	}
	if succeeded {
		qm.removeFromQueue(qp.TargetServer, id)
		delete(qm.players, id)
		return
	}
	qp.InFlight = false
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

// AcquireNextBatch claims up to limit non-in-flight players in targetServer's queue in-place
// without popping them so they keep their position if reconnection fails.
func (qm *QueueManager) AcquireNextBatch(targetServer string, limit int) []*QueuedPlayer {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	targetServer = strings.ToLower(strings.TrimSpace(targetServer))
	list := qm.queues[targetServer]
	if len(list) == 0 || limit <= 0 {
		return nil
	}

	result := make([]*QueuedPlayer, 0, min(limit, len(list)))
	for _, id := range list {
		if len(result) >= limit {
			break
		}
		qp, found := qm.players[id]
		if !found || qp.InFlight {
			continue
		}
		qp.InFlight = true
		qp.Attempts++
		copyQP := *qp
		result = append(result, &copyQP)
	}
	return result
}

// GetNextBatch pops up to limit non-in-flight players from targetServer's queue.
func (qm *QueueManager) GetNextBatch(targetServer string, limit int) []*QueuedPlayer {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	targetServer = strings.ToLower(strings.TrimSpace(targetServer))
	list := qm.queues[targetServer]
	if len(list) == 0 || limit <= 0 {
		return nil
	}

	result := make([]*QueuedPlayer, 0, min(limit, len(list)))
	remaining := make([]uuid.UUID, 0, len(list))

	for _, id := range list {
		qp, found := qm.players[id]
		if !found {
			continue
		}
		if !qp.InFlight && len(result) < limit {
			result = append(result, qp)
			delete(qm.players, id)
		} else {
			remaining = append(remaining, id)
		}
	}

	if len(remaining) == 0 {
		delete(qm.queues, targetServer)
	} else {
		qm.queues[targetServer] = slices.Clone(remaining)
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
	sort.Strings(servers)
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
