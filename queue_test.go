package smartlimbo

import (
	"testing"

	"go.minekube.com/gate/pkg/edition/java/proxy"
	"go.minekube.com/gate/pkg/util/uuid"
)

type mockPlayer struct {
	proxy.Player
	id       uuid.UUID
	username string
}

func newMockPlayer(name string) proxy.Player {
	return &mockPlayer{
		id:       uuid.New(),
		username: name,
	}
}

func (m *mockPlayer) ID() uuid.UUID    { return m.id }
func (m *mockPlayer) Username() string { return m.username }

func TestQueueManagerFIFO(t *testing.T) {
	qm := NewQueueManager()

	p1 := newMockPlayer("Alice")
	p2 := newMockPlayer("Bob")
	p3 := newMockPlayer("Charlie")

	pos1 := qm.Enqueue("smp", p1)
	pos2 := qm.Enqueue("smp", p2)
	pos3 := qm.Enqueue("smp", p3)

	if pos1 != 1 || pos2 != 2 || pos3 != 3 {
		t.Fatalf("Unexpected positions: %d, %d, %d", pos1, pos2, pos3)
	}

	target, pos, total, found := qm.GetPosition(p2.ID())
	if !found || target != "smp" || pos != 2 || total != 3 {
		t.Fatalf("Expected Bob at pos 2/3 in smp, got target=%s pos=%d total=%d", target, pos, total)
	}

	// Pop batch of 2
	batch := qm.GetNextBatch("smp", 2)
	if len(batch) != 2 {
		t.Fatalf("Expected batch of 2, got %d", len(batch))
	}
	if batch[0].Player.Username() != "Alice" || batch[1].Player.Username() != "Bob" {
		t.Fatalf("Expected Alice and Bob first, got %s and %s", batch[0].Player.Username(), batch[1].Player.Username())
	}

	// Check Charlie is now #1
	_, posC, totalC, _ := qm.GetPosition(p3.ID())
	if posC != 1 || totalC != 1 {
		t.Fatalf("Expected Charlie at pos 1/1, got %d/%d", posC, totalC)
	}
}

func TestQueueRemoval(t *testing.T) {
	qm := NewQueueManager()
	p1 := newMockPlayer("Dan")
	p2 := newMockPlayer("Eve")

	qm.Enqueue("survival", p1)
	qm.Enqueue("survival", p2)

	target, removed := qm.Remove(p1.ID())
	if !removed || target != "survival" {
		t.Fatalf("Expected Dan to be removed from survival")
	}

	_, pos, total, found := qm.GetPosition(p2.ID())
	if !found || pos != 1 || total != 1 {
		t.Fatalf("Expected Eve to be shifted to #1, got pos=%d total=%d", pos, total)
	}
}
